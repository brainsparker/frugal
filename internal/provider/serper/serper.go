// Package serper implements a search.Searcher backed by the Serper search
// API (https://serper.dev) — a cheap-per-call Google SERP wrapper.
//
// Serper is the cost-optimized fallback: an order of magnitude cheaper per
// call than the You.com premium tier, with raw title/snippet payload (no
// LLM-tuned content summary). Frugal picks Serper for use cases where cost
// dominates and title+snippet recall is enough (factual-qa, simple
// fresh-facts lookups).
package serper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// DefaultBaseURL is the Serper production endpoint.
const DefaultBaseURL = "https://google.serper.dev"

const maxResponseBodyBytes = 1 << 20 // 1 MiB

// Client implements search.Searcher against Serper.
type Client struct {
	apiKey      string
	baseURL     string
	httpClient  *http.Client
	costPerCall float64
}

// New constructs a Serper client. apiKey is the operator's Serper key.
// baseURL defaults to DefaultBaseURL when empty. costPerCall is the
// per-search USD price the operator agreed to.
func New(apiKey, baseURL string, costPerCall float64) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		apiKey:      apiKey,
		baseURL:     baseURL,
		costPerCall: costPerCall,
		httpClient:  &http.Client{Timeout: 20 * time.Second},
	}
}

// Name reports the provider identifier — stable, used in tool-call
// metadata and recipe `provider:` overrides.
func (c *Client) Name() string { return "serper" }

// CostPerCall returns the configured per-search USD price.
func (c *Client) CostPerCall() float64 { return c.costPerCall }

// Search runs one Serper search. The response's `organic` array maps to
// search.Item; sitelinks, knowledge-graph cards, and ads are intentionally
// dropped (eval can promote them back if data shows they materially help).
// Transient HTTP / network failures are retried inside the driver via
// routing.DoWithRetry; permanent failures (auth, bad query) surface
// immediately as *routing.Error with Kind=Permanent.
func (c *Client) Search(ctx context.Context, q search.Query) (search.Results, error) {
	if q.Text == "" {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("empty query"))
	}
	num := q.MaxResults
	if num <= 0 {
		num = 5
	}
	if num > 20 {
		num = 20
	}
	body := serperRequest{Q: q.Text, Num: num}
	if q.Freshness != "" {
		// Serper uses tbs=qdr:d|w|m for time-window filtering.
		switch q.Freshness {
		case "day":
			body.TBS = "qdr:d"
		case "week":
			body.TBS = "qdr:w"
		case "month":
			body.TBS = "qdr:m"
		}
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("marshal request: %w", err))
	}

	var out search.Results
	err = routing.DoWithRetry(ctx, 1+len(routing.DefaultBackoff), routing.DefaultBackoff, func() error {
		var attemptErr error
		out, attemptErr = c.doOnce(ctx, buf)
		return attemptErr
	})
	return out, err
}

// doOnce runs one HTTP attempt. The retry loop in Search wraps this; the
// returned error is already classified into *routing.Error so
// DoWithRetry can stop on permanent failures.
func (c *Client) doOnce(ctx context.Context, buf []byte) (search.Results, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/search", bytes.NewReader(buf))
	if err != nil {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("X-API-KEY", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return search.Results{}, &routing.Error{
			Provider: c.Name(), Kind: routing.ClassifyNetwork(err), Err: err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return search.Results{}, &routing.Error{
			Provider: c.Name(),
			Kind:     routing.ClassifyHTTPStatus(resp.StatusCode),
			Status:   resp.StatusCode,
			Err:      fmt.Errorf("%s", bytes.TrimSpace(snippet)),
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return search.Results{}, routing.Transient(c.Name(), resp.StatusCode, fmt.Errorf("read response: %w", err))
	}
	if len(body) > maxResponseBodyBytes {
		return search.Results{}, routing.Transient(c.Name(), resp.StatusCode, fmt.Errorf("response exceeds %d bytes", maxResponseBodyBytes))
	}

	var parsed serperResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return search.Results{}, routing.Transient(c.Name(), resp.StatusCode, fmt.Errorf("decode response: %w", err))
	}

	items := make([]search.Item, 0, len(parsed.Organic))
	for _, r := range parsed.Organic {
		items = append(items, search.Item{
			Title:       r.Title,
			URL:         r.Link,
			Snippet:     r.Snippet,
			PublishedAt: r.Date,
		})
	}
	return search.Results{Items: items, CostUSD: c.costPerCall}, nil
}

type serperRequest struct {
	Q   string `json:"q"`
	Num int    `json:"num,omitempty"`
	TBS string `json:"tbs,omitempty"`
}

type serperResponse struct {
	Organic []struct {
		Title   string `json:"title"`
		Link    string `json:"link"`
		Snippet string `json:"snippet"`
		Date    string `json:"date,omitempty"`
	} `json:"organic"`
}
