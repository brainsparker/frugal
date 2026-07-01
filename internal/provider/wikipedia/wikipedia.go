// Package wikipedia implements a search.Searcher backed by the Wikimedia
// REST search API (https://en.wikipedia.org/w/rest.php/v1/search/page).
// Free, official, no API key.
//
// Wikipedia fills a coverage hole in the zero-key tier: Marginalia's
// indie-web index whiffs on mainstream entity and reference queries
// ("CrewAI", "Rust borrow checker") that Wikipedia answers directly.
// With zero-hit fall-through in the router, the chain visits both $0
// providers before spending a paid call. It is not a general web SERP —
// current-events and how-to queries still want Marginalia, SearXNG, or
// a paid provider — which is why it sits after Marginalia in the
// canonical order rather than replacing it.
//
// Etiquette: Wikimedia's robot policy asks API consumers to send a
// descriptive User-Agent with a contact URL. The driver sets
// "frugal (+https://frugal.sh)" by default, matching the Marginalia
// driver's convention.
package wikipedia

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

// DefaultBaseURL is the English Wikipedia REST endpoint root. Operators
// can point base_url at another language edition (or a private MediaWiki
// with the REST extension) in models.yaml.
const DefaultBaseURL = "https://en.wikipedia.org"

// DefaultUserAgent identifies frugal per the Wikimedia robot policy.
const DefaultUserAgent = "frugal (+https://frugal.sh)"

// searchmatchTag strips the <span class="searchmatch"> highlight markup
// (and any other tag) the REST API embeds in excerpts. Snippets are
// plain text on the wire to the agent.
var searchmatchTag = regexp.MustCompile(`<[^>]*>`)

// Client implements search.Searcher against the Wikimedia REST API.
type Client struct {
	baseURL    string
	userAgent  string
	httpClient *http.Client
}

// New constructs a Wikipedia client. baseURL defaults to DefaultBaseURL
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
func (c *Client) Name() string { return "wikipedia" }

// CostPerCall is always zero. Wikipedia is donation-funded and free
// for non-abusive use.
func (c *Client) CostPerCall() float64 { return 0 }

// Search runs one Wikipedia query. Transient HTTP / network failures
// are retried inside the driver; the router falls back to another
// provider if all retries fail.
//
// The REST search endpoint has no time-window parameter, so a freshness
// constraint is served best-effort with a warning attached rather than
// declined: on a zero-key install Wikipedia and Marginalia are the whole
// chain, and erroring would turn every freshness-scoped search into a
// hard failure. The warning tells the agent the window was ignored so it
// can re-query pinned to a provider that honors it (serper, youcom).
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
	if err == nil && q.Freshness != "" {
		out.Warnings = append(out.Warnings,
			"wikipedia: freshness window ignored (provider has no time filter)")
	}
	return out, err
}

// doOnce runs one HTTP attempt. The retry loop in Search wraps this;
// the returned error is already a *routing.Error.
func (c *Client) doOnce(ctx context.Context, q search.Query) (search.Results, error) {
	n := q.MaxResults
	if n <= 0 {
		n = 5
	}
	if n > 20 {
		n = 20
	}
	endpoint := c.baseURL + "/w/rest.php/v1/search/page?q=" +
		url.QueryEscape(q.Text) + "&limit=" + strconv.Itoa(n)

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

	var parsed wikipediaResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return search.Results{}, routing.Transient(c.Name(), resp.StatusCode, fmt.Errorf("decode response: %w", err))
	}

	items := make([]search.Item, 0, len(parsed.Pages))
	for _, p := range parsed.Pages {
		items = append(items, search.Item{
			Title:   p.Title,
			URL:     c.pageURL(p.Key),
			Snippet: cleanExcerpt(p.Excerpt, p.Description),
			// The search endpoint doesn't expose revision timestamps.
		})
	}
	return search.Results{Items: items, CostUSD: 0}, nil
}

// pageURL builds the canonical article URL from a page key. Keys are
// already underscore-form; url.URL.EscapedPath escapes what needs
// escaping while preserving the slashes subpage keys contain.
func (c *Client) pageURL(key string) string {
	u := url.URL{Path: "/wiki/" + key}
	return c.baseURL + u.EscapedPath()
}

// cleanExcerpt turns the API's highlighted-HTML excerpt into a plain-text
// snippet: strip tags, unescape entities. When the excerpt is empty the
// short description (e.g. "Open-source AI agent framework") stands in.
func cleanExcerpt(excerpt, description string) string {
	s := strings.TrimSpace(html.UnescapeString(searchmatchTag.ReplaceAllString(excerpt, "")))
	if s == "" {
		return strings.TrimSpace(description)
	}
	return s
}

// wikipediaResponse is the subset of the REST search response we
// consume. The API also returns id, matched_title, anchor, and
// thumbnail, which we drop on the floor for now.
type wikipediaResponse struct {
	Pages []struct {
		Key         string `json:"key"`
		Title       string `json:"title"`
		Excerpt     string `json:"excerpt"`
		Description string `json:"description"`
	} `json:"pages"`
}
