// Package exafree implements a search.Searcher and an extract.Extractor
// backed by Exa's hosted MCP endpoint (https://mcp.exa.ai/mcp) on its
// keyless tier. Free, public, no API key, no account: the same neural
// index Exa sells over REST, reached through the hosted server that
// Exa publishes for coding agents, subject to free-tier rate limits.
//
// Two rungs come out of one driver because the hosted server exposes
// two tools that map one-to-one onto frugal capabilities:
//
//   - `web_search_exa` is the zero-key chain's second major-index search
//     rung. youcom-free leads the public-free tier so existing chains do
//     not reorder; exa-free follows it, ahead of Marginalia and Wikipedia,
//     so a keyless install keeps a real web index when the You.com daily
//     allowance is spent instead of dropping straight to the two narrow
//     indexes. Operators who prefer Exa first say so with
//     `routing.search.order: [exa-free]`.
//   - `web_fetch_exa` is a free hosted reader, the same shape as Jina
//     Reader: Exa fetches the page server-side and returns clean Markdown.
//     It sits after jina in the canonical extract order (local first, then
//     the established hosted rung, then this one) and before the paid
//     Firecrawl tier, so a page Jina cannot read still gets a free attempt
//     before anything is billed.
//
// Transport: the endpoint is stateless. It issues no Mcp-Session-Id and
// answers a bare tools/call POST without an initialize handshake, the
// shape the 2026-07-28 MCP revision standardizes on. Each call is one
// HTTP POST of a JSON-RPC tools/call; the server answers as an SSE
// stream (one `message` event carrying the response) or as a plain JSON
// document. Both are handled, the same way the youcom-free driver does.
//
// Results arrive as a text content block, not structuredContent. Search
// hits are records separated by a `---` rule, each with `Title:`, `URL:`,
// `Published:`, `Author:` header lines and a `Highlights:` body; a fetch
// is a `# Title` line, a `URL:` line, an optional `Author:` line, a blank
// line, then the page as Markdown. The parsers here are written against
// captures from live keyless calls and are deliberately tolerant: an
// unrecognized header line is skipped, a hit with no URL is dropped.
//
// Quota: the free tier rate-limits. An HTTP 429, a JSON-RPC error whose
// message reads as a rate limit, or a tool error result that does are all
// reported as a *routing.Error with Status 429 so the routing guard cools
// the provider down and the next rung serves.
//
// Privacy note for operators: like youcom-free and Jina, this rung sends
// the query (search) or the target URL (extract) to a third party.
// Tombstone the entry (`exa-free: {enabled: false}`) under either
// providers table, or deny it in the routing policy, to keep that
// capability off Exa.
package exafree

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

// DefaultBaseURL is Exa's hosted MCP endpoint. The keyless tier needs no
// query parameter and no header beyond the MCP ones.
const DefaultBaseURL = "https://mcp.exa.ai/mcp"

// DefaultUserAgent identifies frugal to the endpoint, same convention
// the other keyless drivers follow.
const DefaultUserAgent = "frugal (+https://frugal.sh)"

// Name is the provider identifier both rungs register under. Stable
// across releases: routing policy order / deny lists and the `provider:`
// tool argument pin by it. The same name under search_providers and
// extract_providers is two independent rungs (separate budget, separate
// deny), matching how the config treats every provider name.
const Name = "exa-free"

const (
	searchTool = "web_search_exa"
	fetchTool  = "web_fetch_exa"
)

// protocolVersion is the MCP revision the driver declares on each POST.
// Advisory for a stateless call; older servers ignore it.
const protocolVersion = "2025-06-18"

// maxSnippetChars bounds the snippet frugal__search returns per hit.
// Exa's highlights run to several thousand characters per result, which
// is the point of Exa for an agent reading one page but not for a SERP:
// frugal__search reports est_tokens for the list and a wide result list
// of full highlights would dwarf every other provider's. The first
// highlight passages are kept, cut on a word boundary; the agent calls
// frugal__extract for the rest.
const maxSnippetChars = 600

// fetchMaxCharacters is the per-page character budget asked of
// web_fetch_exa. The tool defaults to 3,000, which is a highlight, not a
// page. frugal applies its own limits.max_chars server-side, so this
// asks for the whole article and lets the configured cap decide.
const fetchMaxCharacters = 200000

// maxResponseBodyBytes caps response reads so a misbehaving upstream
// cannot exhaust memory.
const maxResponseBodyBytes int64 = 8 << 20

// rateLimitSigns matches the wording hosted servers use when a free
// tier is spent, whether it arrives as a JSON-RPC error or a tool error
// result. The HTTP status is the primary signal; this is the backstop
// for a 200 that carries a quota refusal in the body.
var rateLimitSigns = regexp.MustCompile(`(?i)(\b429\b|rate.?limit|quota|too many requests|limit (reached|exceeded))`)

// Client implements search.Searcher and extract.Extractor against the
// hosted keyless endpoint.
type Client struct {
	endpoint   string
	userAgent  string
	httpClient *http.Client
}

// New constructs a client. baseURL defaults to DefaultBaseURL when
// empty; overridable for tests against httptest.
func New(baseURL string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		endpoint:   baseURL,
		userAgent:  DefaultUserAgent,
		httpClient: &http.Client{Timeout: 45 * time.Second},
	}
}

// Name reports the provider identifier.
func (c *Client) Name() string { return Name }

// CostPerCall is always zero: the keyless tier bills nothing.
func (c *Client) CostPerCall() float64 { return 0 }

// Search runs one hosted search. Transient failures (network, 5xx, 429)
// are retried inside the driver via routing.DoWithRetry on the free
// schedule; the router falls back to the next provider when all retries
// fail. The keyless tool has no time filter, so a freshness window is
// reported as a warning rather than silently dropped.
func (c *Client) Search(ctx context.Context, q search.Query) (search.Results, error) {
	if strings.TrimSpace(q.Text) == "" {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("empty query"))
	}
	var out search.Results
	err := routing.DoWithRetry(ctx, routing.RetryAttemptsFor(c.CostPerCall()), routing.DefaultBackoff, func() error {
		var attemptErr error
		out, attemptErr = c.searchOnce(ctx, q)
		return attemptErr
	})
	if err == nil && q.Freshness != "" {
		out.Warnings = append(out.Warnings,
			Name+": freshness window ignored (keyless Exa search has no time filter)")
	}
	return out, err
}

// Extract runs one hosted fetch for q.URL. A target the crawler reports
// as not found is Fatal (the page is gone for every extractor); any other
// crawl failure is Permanent so the chain falls through to the next rung.
func (c *Client) Extract(ctx context.Context, q extract.Query) (extract.Result, error) {
	target := strings.TrimSpace(q.URL)
	if target == "" {
		return extract.Result{}, routing.Fatal(c.Name(), 0, fmt.Errorf("empty url"))
	}
	if u, err := url.Parse(target); err != nil || u.Scheme == "" || u.Host == "" {
		return extract.Result{}, routing.Fatal(c.Name(), 0, fmt.Errorf("parse url: %q is not an absolute http(s) URL", target))
	}
	var out extract.Result
	err := routing.DoWithRetry(ctx, routing.RetryAttemptsFor(c.CostPerCall()), routing.DefaultBackoff, func() error {
		var attemptErr error
		out, attemptErr = c.fetchOnce(ctx, target, q.Formats)
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

// callToolResult is the MCP tools/call result shape the driver consumes.
// The hosted server speaks in text blocks; structuredContent is not
// sent today and is not relied on.
type callToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}

func (c *Client) searchOnce(ctx context.Context, q search.Query) (search.Results, error) {
	n := q.MaxResults
	if n <= 0 {
		n = 5
	}
	if n > 20 {
		n = 20
	}
	args := map[string]any{
		"query": q.Text,
		// The tool schema requires an objective. frugal__search carries
		// only the query, so the objective restates it: the agent that
		// wants a sharper one calls Exa directly.
		"objective":  "Find the pages that best answer: " + q.Text,
		"numResults": n,
	}
	text, status, err := c.call(ctx, searchTool, args, "search")
	if err != nil {
		return search.Results{}, err
	}
	items := parseSearchText(text)
	if len(items) == 0 && strings.TrimSpace(text) != "" && !looksLikeNoResults(text) {
		// A non-empty body that parses to nothing is a format change on
		// the server side, not a zero-hit query. Say so and let the
		// retry loop try once more before falling over.
		return search.Results{}, routing.Transient(c.Name(), status, fmt.Errorf("decode tool result: no hits recognized in %d-char text block", len(text)))
	}
	return search.Results{Items: items, CostUSD: 0}, nil
}

func (c *Client) fetchOnce(ctx context.Context, target string, formats []string) (extract.Result, error) {
	args := map[string]any{
		"urls":          []string{target},
		"maxCharacters": fetchMaxCharacters,
	}
	text, status, err := c.call(ctx, fetchTool, args, "extract")
	if err != nil {
		return extract.Result{}, err
	}
	page := parseFetchText(text)
	content := strings.TrimSpace(page.Content)
	if content == "" {
		// A rendered page with nothing left: Permanent so a scraper with
		// a different pipeline gets a turn, same as go-readability's and
		// Jina's empty-content rule.
		return extract.Result{}, routing.Permanent(c.Name(), status, fmt.Errorf("empty content after fetch"))
	}
	res := extract.Result{
		Title:   page.Title,
		Byline:  page.Author,
		CostUSD: 0,
	}
	// The hosted tool returns Markdown only. Markdown doubles as the
	// plain-text read, the shortcut the Jina and Firecrawl drivers take;
	// html is never produced, so a caller asking only for html gets the
	// Markdown in that slot rather than nothing.
	if wantsOnly(formats, "html") {
		res.HTML = content
	} else {
		res.Markdown = content
		res.Text = content
	}
	return res, nil
}

// call runs one tools/call POST and returns the joined text content of
// a successful result, with the HTTP status for error annotation.
// Errors are already *routing.Error values.
func (c *Client) call(ctx context.Context, tool string, args map[string]any, capability string) (string, int, error) {
	var rpc rpcRequest
	rpc.JSONRPC = "2.0"
	rpc.ID = 1
	rpc.Method = "tools/call"
	rpc.Params.Name = tool
	rpc.Params.Arguments = args
	body, err := json.Marshal(rpc)
	if err != nil {
		return "", 0, routing.Permanent(c.Name(), 0, fmt.Errorf("encode request: %w", err))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, routing.Permanent(c.Name(), 0, fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	// Header-based routing hints from the 2026-07-28 revision. Gateways
	// that meter on them get the method and tool name without parsing
	// the body; servers on earlier revisions ignore them.
	req.Header.Set("Mcp-Method", rpc.Method)
	req.Header.Set("Mcp-Name", tool)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, &routing.Error{Provider: c.Name(), Kind: routing.ClassifyNetwork(err), Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(snippet))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			msg += " (Exa keyless tier is rate limited; the chain falls through to the next " + capability + " rung)"
		}
		return "", resp.StatusCode, &routing.Error{
			Provider:   c.Name(),
			Kind:       routing.ClassifyHTTPStatus(resp.StatusCode),
			RetryAfter: routing.ParseRetryAfter(resp.Header),
			Status:     resp.StatusCode,
			Err:        fmt.Errorf("%s", msg),
		}
	}

	msg, err := readResponse(resp)
	if err != nil {
		return "", resp.StatusCode, routing.Transient(c.Name(), resp.StatusCode, err)
	}
	if msg.Error != nil {
		if rateLimitSigns.MatchString(msg.Error.Message) {
			// Quota refusal delivered as a JSON-RPC error. Report it as a
			// 429 so the guard's cooldown applies.
			return "", resp.StatusCode, rateLimited(c.Name(), msg.Error.Message)
		}
		// A protocol-level error is a fact about this request against
		// this server (unknown tool, invalid params): no retry, but the
		// router still falls through to the next provider.
		return "", resp.StatusCode, routing.Permanent(c.Name(), resp.StatusCode,
			fmt.Errorf("rpc error %d: %s", msg.Error.Code, truncate(msg.Error.Message, 300)))
	}
	if msg.Result == nil {
		return "", resp.StatusCode, routing.Transient(c.Name(), resp.StatusCode, fmt.Errorf("response carried neither result nor error"))
	}
	text := contentText(msg.Result)
	if msg.Result.IsError {
		text = strings.TrimSpace(text)
		switch {
		case rateLimitSigns.MatchString(text):
			return "", resp.StatusCode, rateLimited(c.Name(), text)
		case strings.Contains(text, "CRAWL_NOT_FOUND"):
			// The crawler reports the target itself is gone: no other
			// extractor can fix that, so the chain stops here without
			// spending a paid call.
			return "", resp.StatusCode, &routing.Error{
				Provider: c.Name(),
				Kind:     routing.KindFatal,
				Status:   http.StatusNotFound,
				Err:      fmt.Errorf("target not found: %s", truncate(text, 300)),
			}
		}
		return "", resp.StatusCode, routing.Permanent(c.Name(), resp.StatusCode,
			fmt.Errorf("tool error: %s", truncate(text, 300)))
	}
	return text, resp.StatusCode, nil
}

func rateLimited(provider, detail string) *routing.Error {
	return &routing.Error{
		Provider: provider,
		Kind:     routing.KindTransient,
		Status:   http.StatusTooManyRequests,
		Err:      fmt.Errorf("rate limited: %s", truncate(strings.TrimSpace(detail), 300)),
	}
}

// readResponse decodes the JSON-RPC response from either a plain JSON
// body or an SSE stream. In the SSE case the server may emit
// notifications before the result; those are skipped and the first
// message carrying a result or error wins.
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

// recordSeparator is the rule web_search_exa places between hits.
var recordSeparator = regexp.MustCompile(`(?m)^\s*---\s*$`)

// parseSearchText turns the web_search_exa text block into items. Each
// record is header lines (`Title:`, `URL:`, `Published:`, `Author:`)
// followed by a `Highlights:` (or `Text:`) line and the body. Header
// values of `N/A` are treated as absent. A record without a URL is
// dropped; a record that is only a body is dropped too.
func parseSearchText(text string) []search.Item {
	var items []search.Item
	for _, rec := range recordSeparator.Split(text, -1) {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		var it search.Item
		var body []string
		inBody := false
		for _, line := range strings.Split(rec, "\n") {
			if inBody {
				body = append(body, line)
				continue
			}
			key, val, ok := headerLine(line)
			if !ok {
				// Headers end at the first line that is not one. Whatever
				// follows is body, even without an explicit marker.
				inBody = true
				body = append(body, line)
				continue
			}
			switch key {
			case "title":
				it.Title = val
			case "url":
				it.URL = val
			case "published":
				it.PublishedAt = val
			case "author":
				// Not carried on search.Item today.
			case "highlights", "text", "summary", "content":
				inBody = true
				if val != "" {
					body = append(body, val)
				}
			default:
				// Unknown header (Score:, ID:, ...): skip it.
			}
		}
		if it.URL == "" {
			continue
		}
		if it.Title == "" {
			// Exa reports `Title: N/A` for some pages (observed on the MCP
			// spec site). Every other search rung always carries a title,
			// and agents key on it, so the URL stands in rather than an
			// empty string.
			it.Title = it.URL
		}
		it.Snippet = clampSnippet(strings.TrimSpace(strings.Join(body, "\n")), maxSnippetChars)
		items = append(items, it)
	}
	return items
}

// headerLine reports whether line is `Key: value` with a short,
// alphabetic key, returning the lowercased key and the value with `N/A`
// folded to empty.
func headerLine(line string) (key, val string, ok bool) {
	i := strings.Index(line, ":")
	if i <= 0 || i > 20 {
		return "", "", false
	}
	k := strings.TrimSpace(line[:i])
	for _, r := range k {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == ' ') {
			return "", "", false
		}
	}
	v := strings.TrimSpace(line[i+1:])
	if strings.EqualFold(v, "N/A") {
		v = ""
	}
	return strings.ToLower(k), v, true
}

// fetchedPage is what parseFetchText recovers from a web_fetch_exa block.
type fetchedPage struct {
	Title   string
	URL     string
	Author  string
	Content string
}

// parseFetchText splits the `# Title` / `URL:` / `Author:` preamble from
// the Markdown body. The preamble ends at the first blank line; a block
// that never had one is all body.
func parseFetchText(text string) fetchedPage {
	var p fetchedPage
	lines := strings.Split(strings.TrimLeft(text, "\n"), "\n")
	i := 0
	for ; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			i++
			break
		}
		if i == 0 && strings.HasPrefix(line, "# ") {
			p.Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			continue
		}
		key, val, ok := headerLine(line)
		if !ok {
			// Not a header after all: the body started without a blank
			// line. Keep this line.
			break
		}
		switch key {
		case "url":
			p.URL = val
		case "author":
			p.Author = val
		case "title":
			if p.Title == "" {
				p.Title = val
			}
		case "published":
			// Dropped: extract.Result has no publication field.
		default:
			// Unknown preamble header: skip.
		}
	}
	if i < len(lines) {
		p.Content = strings.Join(lines[i:], "\n")
	}
	return p
}

// looksLikeNoResults reports whether a search text block is the server
// saying it found nothing, so an empty item list is a true zero-hit
// answer rather than a parse failure.
func looksLikeNoResults(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	return strings.HasPrefix(t, "no results") || strings.Contains(t, "no results found") || strings.Contains(t, "0 results")
}

// clampSnippet cuts s to at most n bytes on a rune-safe word boundary.
func clampSnippet(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for !utf8.ValidString(cut) && len(cut) > 0 {
		cut = cut[:len(cut)-1]
	}
	if i := strings.LastIndexAny(cut, " \n\t"); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " \n\t.,;:") + "..."
}

func wantsOnly(formats []string, want string) bool {
	if len(formats) == 0 {
		return false
	}
	for _, f := range formats {
		if !strings.EqualFold(strings.TrimSpace(f), want) {
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
