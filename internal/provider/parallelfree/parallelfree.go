// Package parallelfree implements a search.Searcher and an
// extract.Extractor backed by Parallel's hosted MCP server on its
// keyless tier (https://mcp.parallel.ai/mcp). Free, public, no API key,
// no account: the server's `web_search` tool is Parallel's Search API
// (fast mode) and its `web_fetch` tool is Parallel's Extract API, both
// open to unauthenticated callers at reduced rate limits. Parallel MCP
// 2.0 shipped on 2026-11-05 and made both tools free by default:
// https://parallel.ai/blog/parallel-mcp-2 and
// https://docs.parallel.ai/integrations/mcp/quickstart.md.
//
// One provider name, two capability tables. `parallel-free` under
// search_providers registers the search rung; the same name under
// extract_providers registers the fetch rung. They share this client
// and its transport; routing policy, deny lists, and daily budgets
// treat them independently, like every other name that appears in two
// tables.
//
// Where it sits. Search: after youcom-free (so existing zero-key chains
// keep their first rung) and before Marginalia and Wikipedia. It is a
// second general web index for a keyless install, which matters once
// You.com's roughly 100 queries per day are spent and the chain would
// otherwise drop to the indie and reference indexes. Extract: after
// goreadability (local) and Jina Reader (the established free hosted
// render) and before paid Firecrawl. Parallel fetches from its own page
// cache with a live fetch fallback, so it is a fast third free try
// before any money is spent.
//
// Transport: the endpoint answers a bare tools/call POST without an
// initialize handshake and without requiring a session, the shape the
// 2026-07-28 MCP revision standardizes on. Each call is one HTTP POST
// of a JSON-RPC tools/call; the response is a single JSON document or
// an SSE stream carrying the result. Both are handled. The driver sends
// only the query or URL. It does not send Parallel's optional
// `session_id` (which would link an agent's calls together server-side)
// or `client_model`.
//
// Honest limits, verified against the live endpoint on 2026-11-06:
// `web_search` has no result-count or freshness argument on the keyless
// tier, so the driver clamps the list client-side and folds a freshness
// hint into the search objective (Parallel's documented way to express
// a recency preference without a hard filter). `web_fetch` with
// full_content returns the page as Markdown capped at about 25,000
// characters per URL; a longer page comes back cut at that point with
// no marker, which is why Jina stays ahead of this rung for extract.
// Parallel documents the keyless tier as "lower rate limits" without a
// number; a 429 is reported with its status so the routing guard opens
// the cooldown and the next rung serves.
package parallelfree

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// DefaultBaseURL is Parallel's hosted MCP endpoint. No query parameters:
// the server ignores everything but `login` and `tools`, and a trailing
// slash is a 404, so the driver normalizes one away.
const DefaultBaseURL = "https://mcp.parallel.ai/mcp"

// DefaultUserAgent identifies frugal to the endpoint, same convention
// the other keyless drivers follow.
const DefaultUserAgent = "frugal (+https://frugal.sh)"

// Tool names the hosted server advertises.
const (
	searchTool = "web_search"
	fetchTool  = "web_fetch"
)

// protocolVersion is the MCP revision the driver declares on each POST.
// Advisory for a stateless call; the server accepts the header and
// older servers ignore it.
const protocolVersion = "2025-06-18"

// Per-tool request timeouts. Search is a cached-index lookup and
// answers in about a second; fetch may run a live page fetch and gets
// the same generous budget the Jina and Firecrawl drivers use.
const (
	searchTimeout = 30 * time.Second
	fetchTimeout  = 60 * time.Second
)

// maxSnippetChars bounds the per-hit snippet. Parallel returns long
// excerpts (often 2,000+ characters per hit, enough to answer without a
// fetch) but frugal__search is a result list, not a reader, and ten
// full excerpts would put six thousand tokens on the agent's receipt
// for one search. The first excerpt is kept, cut on a word boundary.
const maxSnippetChars = 600

// maxResults is the most hits the driver returns. The keyless tool has
// no count argument and answers with about ten; the clamp mirrors the
// other drivers' provider-side maxima.
const maxResults = 20

// maxResponseBodyBytes caps response reads. A full-content fetch is
// about 25 KB per URL on this tier; the cap is generous so a good
// answer is never truncated by the driver.
const maxResponseBodyBytes int64 = 8 << 20 // 8 MiB

// rateLimitSigns matches the wording a hosted MCP server uses in a tool
// error result when a quota is spent. The HTTP status is the primary
// signal; this is the backstop for a 200 that carries a quota refusal
// in the body.
var rateLimitSigns = regexp.MustCompile(`(?i)(\b429\b|rate.?limit|quota|too many requests|limit (reached|exceeded))`)

// Client implements search.Searcher and extract.Extractor against the
// keyless Parallel MCP endpoint.
type Client struct {
	endpoint   string
	userAgent  string
	httpClient *http.Client
}

var (
	_ search.Searcher   = (*Client)(nil)
	_ extract.Extractor = (*Client)(nil)
)

// New constructs a client. baseURL defaults to DefaultBaseURL when
// empty; overridable for tests against httptest. Trailing slashes are
// stripped (the live server answers 404 to them).
func New(baseURL string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		endpoint:  baseURL,
		userAgent: DefaultUserAgent,
		// No client-level timeout: each call sets its own via context
		// (searchTimeout / fetchTimeout) so one client serves both tools.
		httpClient: &http.Client{},
	}
}

// Name reports the provider identifier. Stable across releases: routing
// policy order / deny lists and the `provider:` tool argument pin by it.
// The "-free" suffix follows youcom-free: it names the keyless tier,
// so a future keyed Parallel driver can be a separate, separately
// deniable provider.
func (c *Client) Name() string { return "parallel-free" }

// CostPerCall is always zero: the keyless tier bills nothing and, per
// Parallel's docs, reports no usage.
func (c *Client) CostPerCall() float64 { return 0 }

// Search runs one hosted web_search. Transient failures (network, 5xx,
// 429) are retried inside the driver via routing.DoWithRetry; the
// router falls back to the next provider when all retries fail.
func (c *Client) Search(ctx context.Context, q search.Query) (search.Results, error) {
	text := strings.TrimSpace(q.Text)
	if text == "" {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("empty query"))
	}
	var out search.Results
	err := routing.DoWithRetry(ctx, routing.RetryAttemptsFor(c.CostPerCall()), routing.DefaultBackoff, func() error {
		var attemptErr error
		out, attemptErr = c.searchOnce(ctx, text, q)
		return attemptErr
	})
	return out, err
}

// Extract runs one hosted web_fetch for q.URL with full_content on.
// Transient failures are retried inside the driver; permanent failures
// fall through to the next extractor at the router. A dead target (the
// site itself returned 404/410) is Fatal so the chain stops instead of
// paying a scraper to rediscover a dead link.
func (c *Client) Extract(ctx context.Context, q extract.Query) (extract.Result, error) {
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
	if !wantsMarkdown(q.Formats) {
		// Parallel returns Markdown only. A caller that asked for html
		// alone needs a rung that renders it; fall through without a
		// network call.
		return extract.Result{}, routing.Permanent(c.Name(), 0, fmt.Errorf("only markdown is available from this provider"))
	}

	var out extract.Result
	err = routing.DoWithRetry(ctx, routing.RetryAttemptsFor(c.CostPerCall()), routing.DefaultBackoff, func() error {
		var attemptErr error
		out, attemptErr = c.fetchOnce(ctx, target)
		return attemptErr
	})
	return out, err
}

// rpcRequest is the JSON-RPC 2.0 envelope for one tools/call.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"params"`
}

// rpcMessage is the subset of a JSON-RPC 2.0 message the driver reads.
// Notifications carry Method and no ID; the response carries Result or
// Error.
type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result *callToolResult `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// callToolResult is the MCP tools/call result shape the driver
// consumes: structuredContent when the server sends it (the live
// server does), the text content block as a fallback (the same JSON
// document, serialized).
type callToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// searchPayload is the web_search output: results[] ordered by
// relevance, each with url, title, publish_date, and excerpts[].
// search_id, session_id, warnings, and usage are ignored.
type searchPayload struct {
	Results []struct {
		URL         string   `json:"url"`
		Title       string   `json:"title"`
		PublishDate string   `json:"publish_date"`
		Excerpts    []string `json:"excerpts"`
	} `json:"results"`
}

// fetchPayload is the web_fetch output: results[] for URLs that
// extracted, errors[] for URLs that did not (with the site's HTTP
// status when there was one).
type fetchPayload struct {
	Results []struct {
		URL         string   `json:"url"`
		Title       string   `json:"title"`
		PublishDate string   `json:"publish_date"`
		Excerpts    []string `json:"excerpts"`
	} `json:"results"`
	Errors []struct {
		URL            string `json:"url"`
		ErrorType      string `json:"error_type"`
		HTTPStatusCode int    `json:"http_status_code"`
		Content        string `json:"content"`
	} `json:"errors"`
}

// searchOnce runs one web_search attempt. The retry loop in Search
// wraps this; the returned error is already a *routing.Error.
func (c *Client) searchOnce(ctx context.Context, text string, q search.Query) (search.Results, error) {
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	objective := text
	var warnings []string
	if hint := freshnessHint(q.Freshness); hint != "" {
		// The keyless tool has no freshness filter. Parallel's docs say
		// to put a recency preference in the objective; the driver does
		// that and tells the agent it is a preference, not a filter.
		objective = text + ". " + hint
		warnings = append(warnings, fmt.Sprintf("parallel-free has no freshness filter; the %q window was passed as a preference in the search objective, not enforced", q.Freshness))
	}
	args := map[string]any{
		"objective":      objective,
		"search_queries": []string{text},
	}
	msg, status, err := c.call(ctx, searchTool, args)
	if err != nil {
		return search.Results{}, err
	}
	var payload searchPayload
	if err := decodePayload(msg.Result, &payload); err != nil {
		return search.Results{}, routing.Transient(c.Name(), status, fmt.Errorf("decode tool result: %w", err))
	}

	n := q.MaxResults
	if n <= 0 {
		n = 5
	}
	if n > maxResults {
		n = maxResults
	}
	items := make([]search.Item, 0, n)
	for _, h := range payload.Results {
		if h.URL == "" {
			continue
		}
		snippet := ""
		if len(h.Excerpts) > 0 {
			snippet = clip(strings.TrimSpace(h.Excerpts[0]), maxSnippetChars)
		}
		items = append(items, search.Item{
			Title:       h.Title,
			URL:         h.URL,
			Snippet:     snippet,
			PublishedAt: h.PublishDate,
		})
		if len(items) == n {
			break
		}
	}
	return search.Results{Items: items, CostUSD: 0, Warnings: warnings}, nil
}

// fetchOnce runs one web_fetch attempt for a single URL. The retry loop
// in Extract wraps this; the returned error is already a *routing.Error.
func (c *Client) fetchOnce(ctx context.Context, target string) (extract.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	args := map[string]any{
		"urls":         []string{target},
		"full_content": true,
	}
	msg, status, err := c.call(ctx, fetchTool, args)
	if err != nil {
		return extract.Result{}, err
	}
	var payload fetchPayload
	if err := decodePayload(msg.Result, &payload); err != nil {
		return extract.Result{}, routing.Transient(c.Name(), status, fmt.Errorf("decode tool result: %w", err))
	}

	// The server answers 200 even when the TARGET failed, and reports
	// the site's status in errors[]. Mirror the Jina and go-readability
	// rule: 404/410 from the site is Fatal (the page is gone for every
	// extractor); anything else is Permanent (a 403 is often an anti-bot
	// wall a different renderer can get past), so the chain falls
	// through to the next rung.
	for _, e := range payload.Errors {
		if e.URL != "" && !sameURL(e.URL, target) {
			continue
		}
		kind := routing.KindPermanent
		if e.HTTPStatusCode == http.StatusNotFound || e.HTTPStatusCode == http.StatusGone {
			kind = routing.KindFatal
		}
		cause := e.ErrorType
		if cause == "" {
			cause = "extract failed"
		}
		if e.HTTPStatusCode > 0 {
			cause = fmt.Sprintf("%s (target returned http %d)", cause, e.HTTPStatusCode)
		}
		return extract.Result{}, &routing.Error{
			Provider: c.Name(),
			Kind:     kind,
			Status:   e.HTTPStatusCode,
			Err:      fmt.Errorf("%s", cause),
		}
	}

	for _, r := range payload.Results {
		if r.URL != "" && !sameURL(r.URL, target) && len(payload.Results) > 1 {
			continue
		}
		content := strings.TrimSpace(strings.Join(r.Excerpts, "\n\n"))
		if content == "" {
			// A fetch with nothing left after extraction: Permanent so a
			// rung with a different pipeline gets a turn.
			return extract.Result{}, routing.Permanent(c.Name(), status, fmt.Errorf("empty content after fetch"))
		}
		return extract.Result{
			Title:    r.Title,
			Markdown: content,
			// One format per request; Markdown doubles as the plain-text
			// read, the same shortcut the Jina and Firecrawl drivers take.
			Text:    content,
			CostUSD: 0,
		}, nil
	}
	return extract.Result{}, routing.Permanent(c.Name(), status, fmt.Errorf("fetch returned no result for %s", target))
}

// call performs one tools/call POST and returns the JSON-RPC response
// carrying a non-error tool result. Every failure is a *routing.Error.
// The int is the HTTP status, for callers that classify later errors.
func (c *Client) call(ctx context.Context, tool string, args map[string]any) (*rpcMessage, int, error) {
	var rpc rpcRequest
	rpc.JSONRPC = "2.0"
	rpc.ID = 1
	rpc.Method = "tools/call"
	rpc.Params.Name = tool
	rpc.Params.Arguments = args
	body, err := json.Marshal(rpc)
	if err != nil {
		return nil, 0, routing.Permanent(c.Name(), 0, fmt.Errorf("encode request: %w", err))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, routing.Permanent(c.Name(), 0, fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	// Header-based routing hints from the 2026-07-28 revision. The
	// server lists both among its accepted headers; gateways that meter
	// on them get the method and tool name without parsing the body.
	req.Header.Set("Mcp-Method", rpc.Method)
	req.Header.Set("Mcp-Name", tool)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, &routing.Error{
			Provider: c.Name(), Kind: routing.ClassifyNetwork(err), Err: err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(snippet))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			msg += " (keyless tier is rate limited; Parallel publishes no number)"
		}
		return nil, resp.StatusCode, &routing.Error{
			Provider:   c.Name(),
			Kind:       routing.ClassifyHTTPStatus(resp.StatusCode),
			RetryAfter: routing.ParseRetryAfter(resp.Header),
			Status:     resp.StatusCode,
			Err:        fmt.Errorf("%s", msg),
		}
	}

	msg, err := readResponse(resp)
	if err != nil {
		// 200 with a body we can't read as a JSON-RPC response: give the
		// retry loop another shot, same as a malformed REST body.
		return nil, resp.StatusCode, routing.Transient(c.Name(), resp.StatusCode, err)
	}
	if msg.Error != nil {
		// A protocol-level error is a fact about this request against
		// this server (unknown tool, invalid params): no retry, but the
		// router still falls through to the next provider.
		return nil, resp.StatusCode, routing.Permanent(c.Name(), resp.StatusCode,
			fmt.Errorf("rpc error %d: %s", msg.Error.Code, msg.Error.Message))
	}
	if msg.Result == nil {
		return nil, resp.StatusCode, routing.Transient(c.Name(), resp.StatusCode, fmt.Errorf("response carried neither result nor error"))
	}
	if msg.Result.IsError {
		text := strings.TrimSpace(contentText(msg.Result))
		if rateLimitSigns.MatchString(text) {
			// Quota refusal delivered as a tool error rather than an HTTP
			// 429. Report it as a 429 so the guard's cooldown applies.
			return nil, resp.StatusCode, &routing.Error{
				Provider: c.Name(),
				Kind:     routing.KindTransient,
				Status:   http.StatusTooManyRequests,
				Err:      fmt.Errorf("rate limited: %s", truncate(text, 300)),
			}
		}
		return nil, resp.StatusCode, routing.Permanent(c.Name(), resp.StatusCode,
			fmt.Errorf("tool error: %s", truncate(text, 300)))
	}
	return msg, resp.StatusCode, nil
}

// decodePayload reads the tool's output document from structuredContent
// when present, else from the text content block (which carries the
// same JSON, serialized).
func decodePayload(r *callToolResult, into any) error {
	if len(r.StructuredContent) > 0 && string(r.StructuredContent) != "null" {
		return json.Unmarshal(r.StructuredContent, into)
	}
	text := strings.TrimSpace(contentText(r))
	if text == "" {
		return fmt.Errorf("tool result carried no content")
	}
	return json.Unmarshal([]byte(text), into)
}

// readResponse decodes the JSON-RPC response from either a plain JSON
// body or an SSE stream. In the SSE case the server may emit
// notifications before the result; those are skipped and the first
// message carrying a result or error wins. Bodies are capped so a
// misbehaving upstream cannot exhaust memory.
func readResponse(resp *http.Response) (*rpcMessage, error) {
	body := io.LimitReader(resp.Body, maxResponseBodyBytes)
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType == "text/event-stream" {
		return readSSE(body)
	}
	var msg rpcMessage
	if err := json.NewDecoder(body).Decode(&msg); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &msg, nil
}

// readSSE walks a server-sent-events body: `data:` lines accumulate into
// one event, a blank line terminates it. Each event's data is one
// JSON-RPC message.
func readSSE(r io.Reader) (*rpcMessage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), int(maxResponseBodyBytes))
	var data []string
	flush := func() (*rpcMessage, bool) {
		if len(data) == 0 {
			return nil, false
		}
		joined := strings.Join(data, "\n")
		data = data[:0]
		var msg rpcMessage
		if err := json.Unmarshal([]byte(joined), &msg); err != nil {
			return nil, false // not JSON-RPC (keepalive comment, etc.); keep scanning
		}
		if msg.Result != nil || msg.Error != nil {
			return &msg, true
		}
		return nil, false // notification
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if msg, ok := flush(); ok {
				return msg, nil
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		default:
			// event:, id:, retry:, comments. Not needed to locate the result.
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read event stream: %w", err)
	}
	if msg, ok := flush(); ok {
		return msg, nil
	}
	return nil, fmt.Errorf("event stream ended without a JSON-RPC response")
}

// contentText joins the text content blocks of a tool result.
func contentText(r *callToolResult) string {
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// freshnessHint renders frugal's freshness bucket as the sentence the
// driver appends to the search objective. Empty for "" or an unknown
// value.
func freshnessHint(freshness string) string {
	switch strings.ToLower(strings.TrimSpace(freshness)) {
	case "day":
		return "Prefer results published in the past day."
	case "week":
		return "Prefer results published in the past week."
	case "month":
		return "Prefer results published in the past month."
	}
	return ""
}

// wantsMarkdown reports whether the requested formats admit a Markdown
// answer: an empty list (default), or any list that names markdown or
// text. Only an html-only ask is refused.
func wantsMarkdown(formats []string) bool {
	if len(formats) == 0 {
		return true
	}
	for _, f := range formats {
		switch strings.ToLower(strings.TrimSpace(f)) {
		case "markdown", "text", "":
			return true
		}
	}
	return false
}

// sameURL compares two URLs loosely: scheme-insensitive, host
// case-insensitive, trailing slash ignored. The server echoes the URL
// it fetched, which may be normalized.
func sameURL(a, b string) bool {
	norm := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.TrimPrefix(s, "https://")
		s = strings.TrimPrefix(s, "http://")
		s = strings.TrimRight(s, "/")
		if i := strings.IndexByte(s, '/'); i >= 0 {
			return strings.ToLower(s[:i]) + s[i:]
		}
		return strings.ToLower(s)
	}
	return norm(a) == norm(b)
}

// clip cuts s to at most n bytes on a rune and word boundary, appending
// an ellipsis when anything was removed.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if i := strings.LastIndexAny(s[:cut], " \n\t"); i > n/2 {
		cut = i
	}
	return strings.TrimRight(s[:cut], " \n\t.,;:") + "..."
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
