// Package firecrawl implements an extract.Extractor backed by the
// Firecrawl Scrape API (https://api.firecrawl.dev).
//
// Firecrawl is Frugal's premium extract tier — handles JS-rendered
// pages, anti-bot defenses, and PDF/binary content that go-readability
// can't touch. Operators pay per page (~$0.001).
package firecrawl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/routing"
)

// DefaultBaseURL is the Firecrawl production endpoint.
const DefaultBaseURL = "https://api.firecrawl.dev"

// maxResponseBodyBytes caps successful API response reads to avoid
// unbounded memory growth on malformed or malicious upstream responses.
const maxResponseBodyBytes int64 = 2 << 20 // 2 MiB

// Client implements extract.Extractor against Firecrawl.
type Client struct {
	apiKey      string
	baseURL     string
	httpClient  *http.Client
	costPerCall float64
}

// New constructs a Firecrawl client. apiKey is the operator's
// FIRECRAWL_API_KEY. baseURL defaults to DefaultBaseURL when empty.
// costPerCall is the per-page USD price the operator agreed to.
func New(apiKey, baseURL string, costPerCall float64) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		apiKey:      apiKey,
		baseURL:     strings.TrimRight(baseURL, "/"),
		costPerCall: costPerCall,
		httpClient:  &http.Client{Timeout: 60 * time.Second},
		// Firecrawl can take a while on heavy pages (JS rendering,
		// anti-bot waits) — generous timeout vs. the search-tier 15s.
	}
}

// Name reports the provider identifier — stable across releases.
func (c *Client) Name() string { return "firecrawl" }

// CostPerCall returns the configured per-page USD price.
func (c *Client) CostPerCall() float64 { return c.costPerCall }

// Extract calls POST /v1/scrape and maps the response into an
// extract.Result.
func (c *Client) Extract(ctx context.Context, q extract.Query) (extract.Result, error) {
	if q.URL == "" {
		return extract.Result{}, routing.Permanent(c.Name(), 0, fmt.Errorf("empty url"))
	}
	formats := q.Formats
	if len(formats) == 0 {
		formats = []string{"markdown"}
	}
	body, err := json.Marshal(firecrawlRequest{URL: q.URL, Formats: formats})
	if err != nil {
		return extract.Result{}, routing.Permanent(c.Name(), 0, fmt.Errorf("marshal request: %w", err))
	}

	var out extract.Result
	err = routing.DoWithRetry(ctx, 1+len(routing.DefaultBackoff), routing.DefaultBackoff, func() error {
		var attemptErr error
		out, attemptErr = c.doOnce(ctx, body)
		return attemptErr
	})
	return out, err
}

// doOnce runs one HTTP attempt against Firecrawl. The retry loop in
// Extract wraps this; the returned error is already a *routing.Error.
func (c *Client) doOnce(ctx context.Context, body []byte) (extract.Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/scrape", bytes.NewReader(body))
	if err != nil {
		return extract.Result{}, routing.Permanent(c.Name(), 0, fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return extract.Result{}, &routing.Error{
			Provider: c.Name(), Kind: routing.ClassifyNetwork(err), Err: err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return extract.Result{}, &routing.Error{
			Provider: c.Name(),
			Kind:     routing.ClassifyHTTPStatus(resp.StatusCode),
			Status:   resp.StatusCode,
			Err:      fmt.Errorf("%s", bytes.TrimSpace(snippet)),
		}
	}

	var parsed firecrawlResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBodyBytes)).Decode(&parsed); err != nil {
		return extract.Result{}, routing.Transient(c.Name(), resp.StatusCode, fmt.Errorf("decode response: %w", err))
	}

	return extract.Result{
		Markdown: parsed.Data.Markdown,
		HTML:     parsed.Data.HTML,
		Text:     parsed.Data.Markdown, // Firecrawl doesn't return a separate plain-text body
		Title:    parsed.Data.Metadata.Title,
		Byline:   parsed.Data.Metadata.Author,
		Links:    parsed.Data.Links,
		CostUSD:  c.costPerCall,
	}, nil
}

type firecrawlRequest struct {
	URL     string   `json:"url"`
	Formats []string `json:"formats,omitempty"`
}

type firecrawlResponse struct {
	Data struct {
		Markdown string   `json:"markdown,omitempty"`
		HTML     string   `json:"html,omitempty"`
		Links    []string `json:"links,omitempty"`
		Metadata struct {
			Title       string `json:"title,omitempty"`
			Description string `json:"description,omitempty"`
			Author      string `json:"author,omitempty"`
		} `json:"metadata"`
	} `json:"data"`
}
