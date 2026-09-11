// Package brave implements a search.Searcher backed by the Brave Search
// API (https://brave.com/search/api), the one independent Western web
// index still sold self-serve to developers.
//
// Brave is Frugal's independent-index rung: results come from Brave's own
// crawl, not a Google or Bing wrapper, so it answers differently from
// Serper on the same query and keeps a chain useful when the operator
// wants no Google dependency. List price is $0.005 per request, the same
// as You.com; every Brave plan carries $5 of monthly credit (about 1,000
// requests) as long as the project attributes Brave publicly. Operators
// who want to stay inside that credit pair this driver with a
// daily_budget_usd cap in models.yaml (0.16/day is roughly 1,000
// requests per month).
package brave

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// DefaultBaseURL is the Brave Search API production host. The web search
// endpoint is DefaultBaseURL + "/res/v1/web/search" per
// https://api-dashboard.search.brave.com/app/documentation/web-search/get-started.
const DefaultBaseURL = "https://api.search.brave.com"

const maxResponseBodyBytes = 1 << 20 // 1 MiB

// maxCount is the largest `count` the web search endpoint accepts.
const maxCount = 20

// Client implements search.Searcher against Brave Search.
type Client struct {
	apiKey      string
	baseURL     string
	httpClient  *http.Client
	costPerCall float64
}

// New constructs a Brave client. apiKey is the operator's subscription
// token (header: X-Subscription-Token). baseURL defaults to
// DefaultBaseURL when empty (overridable for tests against httptest).
// costPerCall is the per-search USD price the operator agreed to.
func New(apiKey, baseURL string, costPerCall float64) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		apiKey:      apiKey,
		baseURL:     strings.TrimRight(baseURL, "/"),
		costPerCall: costPerCall,
		httpClient:  &http.Client{Timeout: 20 * time.Second},
	}
}

// Name reports the provider identifier. It is stable and used in tool-call
// metadata, routing-policy order / deny lists, and `provider:` pins.
func (c *Client) Name() string { return "brave" }

// CostPerCall returns the configured per-search USD price.
func (c *Client) CostPerCall() float64 { return c.costPerCall }

// Search runs one Brave web search. Only the `web.results` array maps to
// search.Item; news, video, discussion, and infobox verticals are filtered
// out server-side (result_filter=web) so the payload stays small and the
// item shape matches the other search drivers. Transient HTTP / network
// failures are retried inside the driver via routing.DoWithRetry;
// permanent failures (auth, bad query) surface immediately as
// *routing.Error with Kind=Permanent. A 429 classifies as rate-limited so
// the routing guard can cool the provider down.
func (c *Client) Search(ctx context.Context, q search.Query) (search.Results, error) {
	if q.Text == "" {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("empty query"))
	}
	count := q.MaxResults
	if count <= 0 {
		count = 5
	}
	if count > maxCount {
		count = maxCount
	}

	params := url.Values{}
	params.Set("q", q.Text)
	params.Set("count", strconv.Itoa(count))
	// Brave decorates snippets with highlight markup by default; ask for
	// plain text so agents don't have to strip <strong> tags. The parse
	// step still strips defensively in case the flag is ignored.
	params.Set("text_decorations", "false")
	params.Set("result_filter", "web")
	if fresh := freshnessParam(q.Freshness); fresh != "" {
		params.Set("freshness", fresh)
	}
	endpoint := c.baseURL + "/res/v1/web/search?" + params.Encode()

	var out search.Results
	err := routing.DoWithRetry(ctx, 1+len(routing.DefaultBackoff), routing.DefaultBackoff, func() error {
		var attemptErr error
		out, attemptErr = c.doOnce(ctx, endpoint)
		return attemptErr
	})
	return out, err
}

// freshnessParam maps Frugal's coarse time window onto Brave's freshness
// codes: pd (past day), pw (past week), pm (past month). Unknown values
// fall back to no filter, the same behavior as the Serper driver.
func freshnessParam(f string) string {
	switch f {
	case "day":
		return "pd"
	case "week":
		return "pw"
	case "month":
		return "pm"
	}
	return ""
}

// doOnce runs one HTTP attempt. The retry loop in Search wraps this; the
// returned error is already classified into *routing.Error so
// DoWithRetry can stop on permanent failures.
func (c *Client) doOnce(ctx context.Context, endpoint string) (search.Results, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("X-Subscription-Token", c.apiKey)
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

	var parsed braveResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return search.Results{}, routing.Transient(c.Name(), resp.StatusCode, fmt.Errorf("decode response: %w", err))
	}

	items := make([]search.Item, 0, len(parsed.Web.Results))
	for _, r := range parsed.Web.Results {
		if r.URL == "" {
			continue
		}
		items = append(items, search.Item{
			Title:       cleanText(r.Title),
			URL:         r.URL,
			Snippet:     cleanText(r.Description),
			PublishedAt: r.PageAge,
		})
	}
	return search.Results{Items: items, CostUSD: c.costPerCall}, nil
}

var tagPattern = regexp.MustCompile(`<[^>]+>`)

// cleanText strips any residual highlight markup and decodes HTML
// entities Brave leaves in titles and descriptions (&#x27;, &amp;, …), so
// the agent sees plain text like every other driver returns.
func cleanText(s string) string {
	if s == "" {
		return s
	}
	return strings.TrimSpace(html.UnescapeString(tagPattern.ReplaceAllString(s, "")))
}

// braveResponse is the subset of the web search response Frugal reads.
// Brave returns page_age as an ISO-8601 timestamp when it knows the
// publish date and omits it otherwise; `age` (a human string like
// "2 days ago") is intentionally ignored.
type braveResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
			PageAge     string `json:"page_age,omitempty"`
		} `json:"results"`
	} `json:"web"`
}
