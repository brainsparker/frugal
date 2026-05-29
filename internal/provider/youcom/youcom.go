// Package youcom implements a search.Searcher backed by the You.com
// Search API (https://ydc-index.io/v1/search).
//
// You.com is Frugal's premium search tier — LLM-tuned snippets and a
// $100 onboarding credit (~20,000 free calls at the $0.005 list price)
// make it a natural fallback when the free / local providers don't
// return a result and Serper is rate-limited or out of quota.
//
// Note: You.com also offers an MCP server at api.you.com/mcp with a
// no-key free tier (?profile=free, 100 queries/day, search only). That
// path bypasses this driver — agents can talk to it directly via MCP.
// We could add a youcom-mcp Searcher that proxies through it for the
// free-tier price (=0); deferred as a follow-up since it requires an
// MCP-client implementation rather than a REST call.
package youcom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// DefaultBaseURL is the You.com Search API production host. The full
// endpoint is DefaultBaseURL + "/v1/search" per the docs at
// https://you.com/docs/api-reference/search/v1-search.
const DefaultBaseURL = "https://ydc-index.io"

// Keep JSON decoding bounded so a malformed or hostile upstream response
// cannot force unbounded memory growth inside the provider.
const maxResponseBodyBytes = 2 << 20 // 2 MiB

// Client implements search.Searcher against You.com.
type Client struct {
	apiKey      string
	baseURL     string
	httpClient  *http.Client
	costPerCall float64
}

// New constructs a You.com client. apiKey is the operator's You.com
// developer key (header: X-API-Key). baseURL defaults to DefaultBaseURL
// when empty — overridable for tests against httptest. costPerCall is
// the per-search USD price the operator agreed to.
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

// Name reports the provider identifier — stable across releases, used
// in MCP tool-call metadata and recipe YAML `provider:` overrides.
func (c *Client) Name() string { return "youcom" }

// CostPerCall returns the configured per-search USD price.
func (c *Client) CostPerCall() float64 { return c.costPerCall }

// Search runs one You.com search. Returns search.Results with the
// per-call cost set to the configured price (You.com doesn't echo a
// per-call cost in the response). Transient HTTP / network failures are
// retried inside the driver via routing.DoWithRetry; permanent failures
// (auth, bad query) surface immediately as *routing.Error with
// Kind=Permanent.
func (c *Client) Search(ctx context.Context, q search.Query) (search.Results, error) {
	if q.Text == "" {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("empty query"))
	}
	n := q.MaxResults
	if n <= 0 {
		n = 5
	}
	if n > 20 {
		n = 20
	}

	// You.com is GET with the query in URL parameters.
	params := url.Values{}
	params.Set("query", q.Text)
	params.Set("num_web_results", strconv.Itoa(n))
	if q.Freshness != "" {
		// You.com freshness flag: "day" | "week" | "month" | "year".
		params.Set("freshness", q.Freshness)
	}
	endpoint := c.baseURL + "/v1/search?" + params.Encode()

	var out search.Results
	err := routing.DoWithRetry(ctx, 1+len(routing.DefaultBackoff), routing.DefaultBackoff, func() error {
		var attemptErr error
		out, attemptErr = c.doOnce(ctx, endpoint)
		return attemptErr
	})
	return out, err
}

// doOnce runs one HTTP attempt against You.com. The retry loop in
// Search wraps this; the returned error is already classified into
// *routing.Error so DoWithRetry can stop on permanent failures.
func (c *Client) doOnce(ctx context.Context, endpoint string) (search.Results, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("X-API-Key", c.apiKey)
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

	var parsed youcomResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBodyBytes)).Decode(&parsed); err != nil {
		// 200 with malformed body — give the retry loop another shot.
		return search.Results{}, routing.Transient(c.Name(), resp.StatusCode, fmt.Errorf("decode response: %w", err))
	}

	items := make([]search.Item, 0, len(parsed.Hits))
	for _, h := range parsed.Hits {
		snippet := h.Description
		if snippet == "" && len(h.Snippets) > 0 {
			snippet = h.Snippets[0]
		}
		items = append(items, search.Item{
			Title:       h.Title,
			URL:         h.URL,
			Snippet:     snippet,
			PublishedAt: h.PageAge,
		})
	}
	return search.Results{Items: items, CostUSD: c.costPerCall}, nil
}

// youcomResponse is the subset of the You.com Search API response we
// consume. The API returns `hits` with title/url/description/snippets
// plus a published-date-ish `page_age`. Extra fields (favicon, type,
// snippet_source) drop on the floor.
type youcomResponse struct {
	Hits []struct {
		Title       string   `json:"title"`
		URL         string   `json:"url"`
		Description string   `json:"description,omitempty"`
		Snippets    []string `json:"snippets,omitempty"`
		PageAge     string   `json:"page_age,omitempty"`
	} `json:"hits"`
}
