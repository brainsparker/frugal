// Package deepseek implements a search.Searcher and an extract.Extractor
// backed by the DeepSeek Search API and Fetch API
// (https://api-docs.deepseek.com/guides/search), both served from
// DeepSeek's in-house search engine and both billed at $0.75 per 1,000
// successful calls, the lowest list price of any paid rung Frugal routes
// to. Billing starts October 31, 2026 at 16:00 UTC; before that the
// endpoints answer for free to any account with a key.
//
// Why two drivers in one package: the endpoints share a base URL, a
// bearer key (DEEPSEEK_API_KEY, the same key the chat API uses), an
// error envelope, and a price. They differ only in the request body and
// the shape of the answer, so the plumbing is written once.
//
// Search slots into the cheap chain between the free rungs and Serper
// ($0.001): a keyless install that adds one key moves its first paid
// fallback from $1.00 to $0.75 per thousand queries. Fetch slots between
// Jina Reader ($0) and Firecrawl ($0.001) as a hosted Markdown reader
// for the pages go-readability and Jina come up empty on. Fetch does not
// claim a JS renderer or anti-bot handling, so Firecrawl stays the rung
// of last resort.
//
// Query operators: DeepSeek accepts site:, -site:, quoted phrases, lang:,
// after: and before: inside the query string itself. Frugal's freshness
// window (day | week | month) is mapped onto after:YYYY-MM-DD so the
// filter is exact rather than dropped with a warning. The query is
// capped at 128 Unicode characters server-side; a longer query is cut on
// a word boundary and the cut is reported in Warnings so the agent knows
// the provider saw less than it sent.
package deepseek

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
	"unicode/utf8"

	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// DefaultBaseURL is the DeepSeek API host. Search is DefaultBaseURL +
// searchPath, Fetch is DefaultBaseURL + fetchPath.
const DefaultBaseURL = "https://api.deepseek.com"

const (
	searchPath = "/v1/tools/search"
	fetchPath  = "/v1/tools/fetch"
)

// Name is the stable provider identifier both drivers report. One name
// under two capability tables is deliberate: routing policy order / deny
// lists, `provider:` pins, and daily_budget_usd are all scoped per
// capability, so `deny: [deepseek]` under routing.search leaves the
// extract rung alone, and each table keeps its own budget counter.
const Name = "deepseek"

// DefaultCostPerCall is the list price per successful call, $0.75 per
// 1,000, for both endpoints.
const DefaultCostPerCall = 0.00075

const (
	// maxQueryRunes is the server-side cap on the query string.
	maxQueryRunes = 128
	// maxCount is the largest `count` the search endpoint accepts.
	maxCount = 50
	// maxURLLen is the longest URL the fetch endpoint accepts.
	maxURLLen = 8192
	// maxSearchBodyBytes bounds a search response (10 to 50 short records).
	maxSearchBodyBytes = 1 << 20 // 1 MiB
	// maxFetchBodyBytes bounds a fetch response; the body is one page as
	// Markdown. Same cap the Jina and Firecrawl drivers use.
	maxFetchBodyBytes = 8 << 20 // 8 MiB
)

// Search implements search.Searcher against POST /v1/tools/search.
type Search struct {
	apiKey      string
	baseURL     string
	httpClient  *http.Client
	costPerCall float64
	// now is the clock the freshness window is anchored to. Injected so
	// tests can pin the date the after: operator renders.
	now func() time.Time
}

// NewSearch constructs a search client. apiKey is the operator's
// DeepSeek key, sent as a bearer token. baseURL defaults to
// DefaultBaseURL when empty (overridable for tests against httptest).
// costPerCall is the per-call USD price the operator declared.
func NewSearch(apiKey, baseURL string, costPerCall float64) *Search {
	return &Search{
		apiKey:      strings.TrimSpace(apiKey),
		baseURL:     normalizeBase(baseURL),
		costPerCall: costPerCall,
		httpClient:  &http.Client{Timeout: 20 * time.Second},
		now:         time.Now,
	}
}

// Name reports the provider identifier.
func (c *Search) Name() string { return Name }

// CostPerCall returns the configured per-search USD price.
func (c *Search) CostPerCall() float64 { return c.costPerCall }

// Search runs one DeepSeek web search. Each result maps to a
// search.Item: title, url, the UI-length snippet (falling back to the
// model-length summary when the snippet is empty), and the publication
// date when DeepSeek knows it. The rerank_score, authority tier, summary,
// and site metadata drop on the floor today; Item has no field for them
// and promoting any of them waits on an eval showing they change
// downstream answer quality. Transient failures are retried in-driver via
// routing.DoWithRetry (paid prices disable that, same as every paid
// rung); permanent failures surface at once as *routing.Error. A 429
// carries its status so the routing guard opens the cooldown window; a
// 402 (insufficient balance) is permanent and the chain falls through.
func (c *Search) Search(ctx context.Context, q search.Query) (search.Results, error) {
	text := strings.TrimSpace(q.Text)
	if text == "" {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("empty query"))
	}
	count := q.MaxResults
	if count <= 0 {
		count = 5
	}
	if count > maxCount {
		count = maxCount
	}

	query, warnings := buildQuery(text, q.Freshness, c.now())
	buf, err := json.Marshal(searchRequest{Query: query, Count: count, NeedContent: false})
	if err != nil {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("marshal request: %w", err))
	}

	var out search.Results
	err = routing.DoWithRetry(ctx, routing.RetryAttemptsFor(c.CostPerCall()), routing.DefaultBackoff, func() error {
		var attemptErr error
		out, attemptErr = c.doOnce(ctx, buf)
		return attemptErr
	})
	if err != nil {
		return search.Results{}, err
	}
	out.Warnings = append(out.Warnings, warnings...)
	return out, nil
}

// buildQuery renders the query string DeepSeek sees: the operator's text
// plus an after: operator for the freshness window, trimmed to the
// 128-character cap. The operator is always kept whole and the text is
// what gets cut, on a word boundary, so a long query never silently
// loses its time filter. The returned warnings are empty when nothing
// was altered.
func buildQuery(text, freshness string, now time.Time) (string, []string) {
	suffix := freshnessOperator(freshness, now)
	budget := maxQueryRunes - utf8.RuneCountInString(suffix)
	var warnings []string
	if utf8.RuneCountInString(text) > budget {
		text = truncateRunes(text, budget)
		warnings = append(warnings, fmt.Sprintf(
			"deepseek: query cut to %d characters (provider limit %d); results reflect the shortened query",
			utf8.RuneCountInString(text), maxQueryRunes))
	}
	return text + suffix, warnings
}

// freshnessOperator maps Frugal's coarse window onto DeepSeek's after:
// operator, rendered as a leading-space suffix ready to append to the
// query. Unknown values produce no operator, matching the Serper and
// Brave drivers' fall-through to an unfiltered search.
func freshnessOperator(f string, now time.Time) string {
	var since time.Time
	switch f {
	case "day":
		since = now.AddDate(0, 0, -1)
	case "week":
		since = now.AddDate(0, 0, -7)
	case "month":
		since = now.AddDate(0, -1, 0)
	default:
		return ""
	}
	return " after:" + since.UTC().Format("2006-01-02")
}

// truncateRunes cuts s to at most n runes, backing off to the last space
// so a word is never split in half. When there is no space to back off
// to, the hard cut stands.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	cut := string(runes[:n])
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut)
}

// doOnce runs one HTTP attempt of a search. The retry loop in Search
// wraps this; the returned error is already a *routing.Error.
func (c *Search) doOnce(ctx context.Context, buf []byte) (search.Results, error) {
	body, status, err := post(ctx, c.httpClient, c.baseURL+searchPath, c.apiKey, buf, maxSearchBodyBytes, c.Name())
	if err != nil {
		return search.Results{}, err
	}
	var parsed searchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return search.Results{}, routing.Transient(c.Name(), status, fmt.Errorf("decode response: %w", err))
	}
	items := make([]search.Item, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		if r.URL == "" {
			continue
		}
		snippet := strings.TrimSpace(r.Snippet)
		if snippet == "" {
			snippet = strings.TrimSpace(r.Summary)
		}
		items = append(items, search.Item{
			Title:       strings.TrimSpace(r.Title),
			URL:         r.URL,
			Snippet:     snippet,
			PublishedAt: r.Date,
		})
	}
	return search.Results{Items: items, CostUSD: c.costPerCall}, nil
}

type searchRequest struct {
	Query       string `json:"query"`
	Count       int    `json:"count,omitempty"`
	NeedContent bool   `json:"need_content"`
}

// searchResponse is the subset of the Search API answer the driver
// reads. rerank_score (0 to 3), authority (-1 to 4), summary (600 to 800
// characters written for a model), site_name, site_icon, and content are
// also present on the wire.
type searchResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Snippet string `json:"snippet"`
		Summary string `json:"summary"`
		Date    string `json:"date"`
	} `json:"results"`
}

// Fetch implements extract.Extractor against POST /v1/tools/fetch.
type Fetch struct {
	apiKey      string
	baseURL     string
	httpClient  *http.Client
	costPerCall float64
}

// NewFetch constructs a fetch client. Same arguments as NewSearch; the
// two usually share one key.
func NewFetch(apiKey, baseURL string, costPerCall float64) *Fetch {
	return &Fetch{
		apiKey:      strings.TrimSpace(apiKey),
		baseURL:     normalizeBase(baseURL),
		costPerCall: costPerCall,
		// A hosted fetch of a slow origin can take a while; the same
		// generous budget the Jina and Firecrawl drivers use.
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// Name reports the provider identifier.
func (c *Fetch) Name() string { return Name }

// CostPerCall returns the configured per-fetch USD price.
func (c *Fetch) CostPerCall() float64 { return c.costPerCall }

// Extract runs one Fetch call for q.URL and returns the page as
// Markdown. Fetch renders a single shape, so Markdown doubles as the
// plain-text read (the Jina and Firecrawl drivers take the same
// shortcut); an html-only request still gets Markdown and Text rather
// than nothing. A URL the endpoint would reject outright (no scheme or
// host, embedded credentials, over 8,192 characters) is refused locally
// so the chain never pays for a request DeepSeek documents as a 400.
//
// Error mapping: a 404 from Fetch means it "could not retrieve usable
// page content", which covers a dead URL but also a site that blocked
// the fetcher, an unsupported content type, or a page with nothing
// extractable. Because the endpoint does not say which, the driver
// classifies it Permanent rather than Fatal: a different renderer may
// still get the page, so the chain falls through instead of stopping. A
// 403 is DeepSeek's own security policy refusing the target, also
// Permanent. 429 carries its status for the cooldown guard.
func (c *Fetch) Extract(ctx context.Context, q extract.Query) (extract.Result, error) {
	target := strings.TrimSpace(q.URL)
	if target == "" {
		return extract.Result{}, routing.Fatal(c.Name(), 0, fmt.Errorf("empty url"))
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme == "" || u.Host == "" {
		if err == nil {
			err = fmt.Errorf("missing scheme or host")
		}
		return extract.Result{}, routing.Fatal(c.Name(), 0, fmt.Errorf("parse url: %w", err))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return extract.Result{}, routing.Permanent(c.Name(), 0, fmt.Errorf("fetch accepts http and https urls only, got %s", u.Scheme))
	}
	if u.User != nil {
		return extract.Result{}, routing.Permanent(c.Name(), 0, fmt.Errorf("fetch rejects urls with embedded credentials"))
	}
	if len(target) > maxURLLen {
		return extract.Result{}, routing.Permanent(c.Name(), 0, fmt.Errorf("url exceeds %d characters", maxURLLen))
	}

	buf, err := json.Marshal(fetchRequest{URL: target})
	if err != nil {
		return extract.Result{}, routing.Permanent(c.Name(), 0, fmt.Errorf("marshal request: %w", err))
	}

	var out extract.Result
	err = routing.DoWithRetry(ctx, routing.RetryAttemptsFor(c.CostPerCall()), routing.DefaultBackoff, func() error {
		var attemptErr error
		out, attemptErr = c.doOnce(ctx, buf)
		return attemptErr
	})
	return out, err
}

// doOnce runs one HTTP attempt of a fetch.
func (c *Fetch) doOnce(ctx context.Context, buf []byte) (extract.Result, error) {
	body, status, err := post(ctx, c.httpClient, c.baseURL+fetchPath, c.apiKey, buf, maxFetchBodyBytes, c.Name())
	if err != nil {
		return extract.Result{}, err
	}
	var parsed fetchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return extract.Result{}, routing.Permanent(c.Name(), status, fmt.Errorf("decode response: %w", err))
	}
	content := strings.TrimSpace(parsed.Content)
	if content == "" {
		// Permanent, not Fatal: the page may well exist and a renderer
		// with a different pipeline gets the next turn, the same rule
		// go-readability and Jina follow on empty content.
		return extract.Result{}, routing.Permanent(c.Name(), status, fmt.Errorf("empty content"))
	}
	return extract.Result{
		Title:    strings.TrimSpace(parsed.Title),
		Markdown: content,
		Text:     content,
		CostUSD:  c.costPerCall,
	}, nil
}

type fetchRequest struct {
	URL string `json:"url"`
}

type fetchResponse struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// errorEnvelope is DeepSeek's OpenAI-compatible error body. Only the
// message is read; it names the offending field on a 400 and the reason
// on a 402/403, which is what the agent needs to see in the failover
// error list.
type errorEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// post performs one authenticated JSON POST shared by both drivers and
// returns the body of a 200 response. Non-200 responses are classified
// into *routing.Error (429 and 5xx transient, other 4xx permanent) with
// the server's error message as the cause when the body carries one.
func post(ctx context.Context, client *http.Client, endpoint, apiKey string, payload []byte, maxBody int64, provider string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, routing.Permanent(provider, 0, fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, &routing.Error{Provider: provider, Kind: routing.ClassifyNetwork(err), Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, resp.StatusCode, &routing.Error{
			Provider:   provider,
			Kind:       routing.ClassifyHTTPStatus(resp.StatusCode),
			RetryAfter: routing.ParseRetryAfter(resp.Header),
			Status:     resp.StatusCode,
			Err:        fmt.Errorf("%s", errorMessage(snippet)),
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, resp.StatusCode, routing.Transient(provider, resp.StatusCode, fmt.Errorf("read response: %w", err))
	}
	if int64(len(body)) > maxBody {
		return nil, resp.StatusCode, routing.Permanent(provider, resp.StatusCode, fmt.Errorf("response exceeds %d bytes", maxBody))
	}
	return body, resp.StatusCode, nil
}

// errorMessage pulls the human-readable message out of DeepSeek's error
// envelope, falling back to the raw (trimmed) body when the response is
// not the documented JSON shape.
func errorMessage(body []byte) string {
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && strings.TrimSpace(env.Error.Message) != "" {
		return strings.TrimSpace(env.Error.Message)
	}
	return string(bytes.TrimSpace(body))
}

func normalizeBase(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return baseURL
}
