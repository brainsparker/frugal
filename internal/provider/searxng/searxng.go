// Package searxng implements a search.Searcher backed by a SearXNG
// instance — an open-source, self-hosted meta-search engine that
// aggregates results from many public engines without an API key.
//
// SearXNG fills the "free / local" column the README has always promised:
// CostPerCall is zero, so the router prefers it over paid providers when
// configured. Falls back to paid providers automatically (the router's
// fallback chain) if the operator's SearXNG instance is down or rate-
// limits the call — the whole point of the fallback chain.
//
// Operators stand up their own instance (Docker image: `searxng/searxng`)
// and point Frugal at it via the SEARXNG_URL env var or models.yaml
// base_url. See https://docs.searxng.org/ for setup.
package searxng

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// Client implements search.Searcher against a SearXNG instance.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New constructs a SearXNG client pointed at baseURL. Trailing slashes are
// stripped. Returns nil if baseURL is empty — callers can use that to
// gate registration ("only register searxng when an endpoint is set").
func New(baseURL string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// Name reports the provider identifier — stable, used in tool-call
// metadata and `provider:` tool-argument overrides.
func (c *Client) Name() string { return "searxng" }

// CostPerCall is always zero. SearXNG is self-hosted; the operator pays
// for the VPS, not per call.
func (c *Client) CostPerCall() float64 { return 0 }

// Search runs one SearXNG query. Returns search.Results with CostUSD=0.
// Transient HTTP / network failures are retried inside the driver; the
// router will fall back to a paid provider if all retries fail.
func (c *Client) Search(ctx context.Context, q search.Query) (search.Results, error) {
	if q.Text == "" {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("empty query"))
	}
	var out search.Results
	err := routing.DoWithRetry(ctx, 1+len(routing.DefaultBackoff), routing.DefaultBackoff, func() error {
		var attemptErr error
		out, attemptErr = c.doOnce(ctx, q)
		return attemptErr
	})
	return out, err
}

// doOnce runs one HTTP attempt. The retry loop in Search wraps this; the
// returned error is already a *routing.Error.
func (c *Client) doOnce(ctx context.Context, q search.Query) (search.Results, error) {
	// SearXNG accepts both GET and POST for /search; POST keeps the URL
	// clean and bypasses some servers' query-string length limits.
	form := url.Values{}
	form.Set("q", q.Text)
	form.Set("format", "json")
	form.Set("safesearch", "0")
	if q.Freshness != "" {
		// SearXNG maps to `time_range=day|week|month|year`; pass through.
		form.Set("time_range", q.Freshness)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/search", strings.NewReader(form.Encode()))
	if err != nil {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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

	var parsed searxResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return search.Results{}, routing.Transient(c.Name(), resp.StatusCode, fmt.Errorf("decode response: %w", err))
	}

	max := q.MaxResults
	if max <= 0 {
		max = 5
	}
	if max > 20 {
		max = 20
	}
	items := make([]search.Item, 0, max)
	for i, r := range parsed.Results {
		if i >= max {
			break
		}
		items = append(items, search.Item{
			Title:       r.Title,
			URL:         r.URL,
			Snippet:     firstNonEmpty(r.Content, r.Snippet),
			PublishedAt: r.PublishedDate,
		})
	}
	return search.Results{Items: items, CostUSD: 0}, nil
}

type searxResponse struct {
	Results []struct {
		Title         string `json:"title"`
		URL           string `json:"url"`
		Content       string `json:"content,omitempty"`
		Snippet       string `json:"snippet,omitempty"`
		PublishedDate string `json:"publishedDate,omitempty"`
	} `json:"results"`
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
