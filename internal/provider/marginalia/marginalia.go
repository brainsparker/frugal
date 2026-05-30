// Package marginalia implements a search.Searcher backed by the
// Marginalia Search API (https://search.marginalia.nu), a community-run,
// indie-web-focused search engine. Free, public, no API key.
//
// Marginalia fills out the "free / local" search column alongside
// SearXNG, giving the router a second $0 provider with different result
// characteristics — Marginalia's index leans toward small, hand-curated
// sites and tends to return content SearXNG misses on long-tail
// queries. When both are configured, OrderByCost is stable on ties so
// config order decides which is hit first.
//
// Etiquette: Marginalia is donation-funded and asks API consumers to
// identify themselves with a User-Agent. The driver sets
// "frugal/<version> (+https://frugal.sh)" by default.
package marginalia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

const maxResponseBodyBytes int64 = 1 << 20 // 1 MiB safety cap

// DefaultBaseURL is the Marginalia public API endpoint.
const DefaultBaseURL = "https://api.marginalia.nu"

// DefaultUserAgent is what Marginalia sees if the caller doesn't override.
// The (+URL) form is the convention Marginalia's docs ask for.
const DefaultUserAgent = "frugal (+https://frugal.sh)"

// Client implements search.Searcher against Marginalia.
type Client struct {
	baseURL    string
	userAgent  string
	httpClient *http.Client
}

// New constructs a Marginalia client. baseURL defaults to DefaultBaseURL
// when empty — overridable for tests against httptest. Trailing slashes
// are stripped.
func New(baseURL string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL:    baseURL,
		userAgent:  DefaultUserAgent,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// Name reports the provider identifier — stable across releases, used
// in MCP tool-call metadata and recipe YAML `provider:` overrides.
func (c *Client) Name() string { return "marginalia" }

// CostPerCall is always zero. Marginalia is donation-funded and free
// for non-abusive use.
func (c *Client) CostPerCall() float64 { return 0 }

// Search runs one Marginalia query. Transient HTTP / network failures
// are retried inside the driver; the router falls back to another
// provider if all retries fail.
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

// doOnce runs one HTTP attempt. The retry loop in Search wraps this;
// the returned error is already a *routing.Error.
func (c *Client) doOnce(ctx context.Context, q search.Query) (search.Results, error) {
	// Marginalia's URL form: /search/<query>?index=1&count=N
	// The query goes in the path, URL-encoded.
	n := q.MaxResults
	if n <= 0 {
		n = 5
	}
	if n > 20 {
		n = 20
	}
	endpoint := c.baseURL + "/search/" + url.PathEscape(q.Text) +
		"?index=1&count=" + strconv.Itoa(n)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("User-Agent", c.userAgent)
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

	var parsed marginaliaResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBodyBytes)).Decode(&parsed); err != nil {
		return search.Results{}, routing.Transient(c.Name(), resp.StatusCode, fmt.Errorf("decode response: %w", err))
	}

	items := make([]search.Item, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		items = append(items, search.Item{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Description,
			// Marginalia doesn't expose published_date in its result rows.
		})
	}
	return search.Results{Items: items, CostUSD: 0}, nil
}

// marginaliaResponse is the subset of the Marginalia API response we
// consume. Marginalia includes additional fields (rankingScore, format,
// features) that we drop on the floor for now.
type marginaliaResponse struct {
	Results []struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description"`
	} `json:"results"`
}
