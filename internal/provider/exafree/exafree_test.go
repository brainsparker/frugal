package exafree

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// searchText is the web_search_exa text block as captured from a live
// keyless call (trimmed): records separated by a `---` rule, header
// lines, then a Highlights body. The third record has no URL and must
// be dropped.
const searchText = "Title: Comparing agentic AI frameworks - AWS Prescriptive Guidance\n" +
	"URL: https://docs.aws.amazon.com/prescriptive-guidance/latest/agentic-ai-frameworks/comparing-agentic-ai-frameworks.html\n" +
	"Published: N/A\n" +
	"Author: N/A\n" +
	"Highlights:\n" +
	"When selecting an agentic AI framework for autonomous agent development, consider how each option aligns with your specific requirements.\n\n" +
	"| Framework | AWS integration |\n| --- | --- |\n| AutoGen | Weak |\n" +
	"\n---\n\n" +
	"Title: The best AI agent frameworks in 2026\n" +
	"URL: https://www.langchain.com/resources/ai-agent-frameworks\n" +
	"Published: 2026-05-02T00:00:00.000Z\n" +
	"Author: LangChain\n" +
	"Highlights:\n" +
	"LangGraph is the framework for stateful agents.\n" +
	"\n---\n\n" +
	"Title: no url, must be dropped\n" +
	"Published: N/A\n" +
	"Highlights:\n" +
	"orphan body\n"

// fetchText is the web_fetch_exa text block as captured live: a `# Title`
// line, a URL line, an Author line, a blank line, then Markdown.
const fetchText = "# frugal, the open routing layer for AI tools\n" +
	"URL: https://frugal.sh\n" +
	"Author: frugal.sh\n" +
	"\n" +
	"frugal, the open routing layer for AI tools\n\n" +
	"# frugal\n\n" +
	"One Go binary. Search, extract, and browse across providers.\n"

// sseResult wraps a tool result the way the live endpoint streams it:
// one `message` event whose data is the JSON-RPC response.
func sseResult(text string, isError bool) string {
	res := map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
	}
	if isError {
		res["isError"] = true
	}
	b, _ := json.Marshal(map[string]any{"result": res, "jsonrpc": "2.0", "id": 1})
	return "event: message\ndata: " + string(b) + "\n\n"
}

func sseRPCError(code int, msg string) string {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": code, "message": msg}})
	return "event: message\ndata: " + string(b) + "\n\n"
}

func TestSearch_HappyPath_SSE(t *testing.T) {
	var gotBody []byte
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: got %s want POST", r.Method)
		}
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseResult(searchText, false))
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.Search(context.Background(), search.Query{Text: "AI agent framework comparison", MaxResults: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 items (the url-less hit dropped), got %d: %+v", len(res.Items), res.Items)
	}
	if res.Items[0].Title != "Comparing agentic AI frameworks - AWS Prescriptive Guidance" {
		t.Errorf("item[0] title = %q", res.Items[0].Title)
	}
	if !strings.HasPrefix(res.Items[0].Snippet, "When selecting an agentic AI framework") {
		t.Errorf("item[0] snippet = %q", res.Items[0].Snippet)
	}
	if res.Items[0].PublishedAt != "" {
		t.Errorf("N/A published must fold to empty; got %q", res.Items[0].PublishedAt)
	}
	if res.Items[1].URL != "https://www.langchain.com/resources/ai-agent-frameworks" || res.Items[1].PublishedAt != "2026-05-02T00:00:00.000Z" {
		t.Errorf("item[1] = %+v", res.Items[1])
	}
	if res.Items[1].Snippet != "LangGraph is the framework for stateful agents." {
		t.Errorf("item[1] snippet = %q", res.Items[1].Snippet)
	}
	if res.CostUSD != 0 {
		t.Errorf("keyless tier must cost 0; got %v", res.CostUSD)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("no freshness asked, no warning expected; got %v", res.Warnings)
	}

	var rpc rpcRequest
	if err := json.Unmarshal(gotBody, &rpc); err != nil {
		t.Fatalf("request body is not JSON-RPC: %v\n%s", err, gotBody)
	}
	if rpc.Method != "tools/call" || rpc.Params.Name != searchTool {
		t.Errorf("rpc = %s %s", rpc.Method, rpc.Params.Name)
	}
	if rpc.Params.Arguments["query"] != "AI agent framework comparison" {
		t.Errorf("query arg = %v", rpc.Params.Arguments["query"])
	}
	if obj, _ := rpc.Params.Arguments["objective"].(string); !strings.Contains(obj, "AI agent framework comparison") {
		t.Errorf("objective is required by the tool schema and must restate the query; got %q", obj)
	}
	if n, _ := rpc.Params.Arguments["numResults"].(float64); n != 3 {
		t.Errorf("numResults = %v", rpc.Params.Arguments["numResults"])
	}
	if got := gotHeaders.Get("Accept"); !strings.Contains(got, "text/event-stream") {
		t.Errorf("Accept must admit SSE; got %q", got)
	}
	if got := gotHeaders.Get("Mcp-Name"); got != searchTool {
		t.Errorf("Mcp-Name = %q", got)
	}
	if got := gotHeaders.Get("User-Agent"); got != DefaultUserAgent {
		t.Errorf("User-Agent = %q", got)
	}
}

func TestSearch_PlainJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{
			"content": []map[string]any{{"type": "text", "text": searchText}},
		}})
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	res, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("got %d items", len(res.Items))
	}
}

func TestSearch_FreshnessWarns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseResult(searchText, false))
	}))
	defer srv.Close()
	res, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x", Freshness: "week"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "freshness window ignored") {
		t.Errorf("want one freshness warning; got %v", res.Warnings)
	}
}

func TestSearch_ClampsResultsAndSnippet(t *testing.T) {
	long := strings.Repeat("word ", 400) // 2000 chars
	text := "Title: Long\nURL: https://example.com/long\nHighlights:\n" + long
	var gotN float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc rpcRequest
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &rpc)
		gotN, _ = rpc.Params.Arguments["numResults"].(float64)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseResult(text, false))
	}))
	defer srv.Close()
	res, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x", MaxResults: 50})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotN != 20 {
		t.Errorf("numResults must clamp to 20; got %v", gotN)
	}
	if l := len(res.Items[0].Snippet); l > maxSnippetChars+3 {
		t.Errorf("snippet must be clamped to about %d chars; got %d", maxSnippetChars, l)
	}
	if !strings.HasSuffix(res.Items[0].Snippet, "...") {
		t.Errorf("clamped snippet must end with an ellipsis; got %q", res.Items[0].Snippet[len(res.Items[0].Snippet)-10:])
	}
}

func TestSearch_DefaultCount(t *testing.T) {
	var gotN float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc rpcRequest
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &rpc)
		gotN, _ = rpc.Params.Arguments["numResults"].(float64)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseResult(searchText, false))
	}))
	defer srv.Close()
	if _, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if gotN != 5 {
		t.Errorf("zero MaxResults must default to 5; got %v", gotN)
	}
}

func TestSearch_EmptyQueryIsPermanent(t *testing.T) {
	_, err := New("http://127.0.0.1:1").Search(context.Background(), search.Query{Text: "  "})
	if !routing.IsPermanent(err) {
		t.Fatalf("want permanent error, got %v", err)
	}
}

func TestSearch_HTTP429IsCooldownSignal(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Retry-After", "2")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := New(srv.URL)
	c.httpClient = srv.Client()
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	var re *routing.Error
	if !errors.As(err, &re) {
		t.Fatalf("want *routing.Error, got %T %v", err, err)
	}
	if re.Status != http.StatusTooManyRequests || re.Kind != routing.KindTransient {
		t.Errorf("429 must surface as transient status 429; got %+v", re)
	}
	if re.RetryAfter == 0 {
		t.Errorf("Retry-After must be parsed; got %v", re.RetryAfter)
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Errorf("a free provider retries transient failures; got %d calls", calls)
	}
}

func TestSearch_RPCErrorRateLimitMapsTo429(t *testing.T) {
	// The shape Parallel's keyless server uses; Exa is handled the same
	// way if it ever answers a quota refusal as a JSON-RPC error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, strings.TrimSuffix(strings.TrimPrefix(sseRPCError(-32000, "You've hit the free-tier rate limit for Search MCP."), "event: message\ndata: "), "\n\n"))
	}))
	defer srv.Close()
	_, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x"})
	var re *routing.Error
	if !errors.As(err, &re) || re.Status != http.StatusTooManyRequests {
		t.Fatalf("rate-limit rpc error must map to status 429; got %v", err)
	}
}

func TestSearch_RPCErrorOtherIsPermanent(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseRPCError(-32602, "Invalid params"))
	}))
	defer srv.Close()
	_, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x"})
	if !routing.IsPermanent(err) || !strings.Contains(err.Error(), "rpc error -32602") {
		t.Fatalf("want permanent rpc error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("permanent errors must not retry; got %d calls", calls)
	}
}

func TestSearch_ToolErrorRateLimitMapsTo429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseResult("Rate limit exceeded for anonymous usage. Add an API key for higher limits.", true))
	}))
	defer srv.Close()
	_, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x"})
	var re *routing.Error
	if !errors.As(err, &re) || re.Status != http.StatusTooManyRequests {
		t.Fatalf("rate-limit tool error must map to status 429; got %v", err)
	}
}

func TestSearch_NoResultsIsEmptyNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseResult("No results found.", false))
	}))
	defer srv.Close()
	res, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x"})
	if err != nil {
		t.Fatalf("a zero-hit answer is not an error: %v", err)
	}
	if len(res.Items) != 0 {
		t.Errorf("got %d items", len(res.Items))
	}
}

func TestSearch_UnrecognizedBodyIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseResult("<html>unexpected</html>", false))
	}))
	defer srv.Close()
	_, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x"})
	if !routing.IsTransient(err) {
		t.Fatalf("a non-empty body with no recognizable hits must be transient so the chain falls over; got %v", err)
	}
}

func TestSearch_5xxIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream down", http.StatusBadGateway)
	}))
	defer srv.Close()
	_, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x"})
	if !routing.IsTransient(err) {
		t.Fatalf("want transient, got %v", err)
	}
}

func TestSearch_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New("http://127.0.0.1:1").Search(ctx, search.Query{Text: "x"})
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestExtract_HappyPath(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseResult(fetchText, false))
	}))
	defer srv.Close()
	res, err := New(srv.URL).Extract(context.Background(), extract.Query{URL: "https://frugal.sh"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Title != "frugal, the open routing layer for AI tools" {
		t.Errorf("title = %q", res.Title)
	}
	if res.Byline != "frugal.sh" {
		t.Errorf("byline = %q", res.Byline)
	}
	if !strings.HasPrefix(res.Markdown, "frugal, the open routing layer") || !strings.Contains(res.Markdown, "One Go binary.") {
		t.Errorf("markdown = %q", res.Markdown)
	}
	if strings.Contains(res.Markdown, "URL: https://frugal.sh") {
		t.Errorf("preamble must be stripped from the body; got %q", res.Markdown[:80])
	}
	if res.Text != res.Markdown {
		t.Errorf("markdown doubles as text")
	}
	if res.HTML != "" {
		t.Errorf("no html produced by default; got %d bytes", len(res.HTML))
	}
	if res.CostUSD != 0 {
		t.Errorf("cost = %v", res.CostUSD)
	}
	var rpc rpcRequest
	if err := json.Unmarshal(gotBody, &rpc); err != nil {
		t.Fatal(err)
	}
	if rpc.Params.Name != fetchTool {
		t.Errorf("tool = %q", rpc.Params.Name)
	}
	urls, _ := rpc.Params.Arguments["urls"].([]any)
	if len(urls) != 1 || urls[0] != "https://frugal.sh" {
		t.Errorf("urls arg = %v", rpc.Params.Arguments["urls"])
	}
	if mc, _ := rpc.Params.Arguments["maxCharacters"].(float64); mc != fetchMaxCharacters {
		t.Errorf("maxCharacters must ask for the whole page (tool default is 3000); got %v", mc)
	}
}

func TestExtract_HTMLOnlyFormatGetsMarkdownInHTMLSlot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseResult(fetchText, false))
	}))
	defer srv.Close()
	res, err := New(srv.URL).Extract(context.Background(), extract.Query{URL: "https://frugal.sh", Formats: []string{"html"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.HTML == "" || res.Markdown != "" {
		t.Errorf("html-only ask should land content in HTML; got md=%d html=%d", len(res.Markdown), len(res.HTML))
	}
}

func TestExtract_NotFoundIsFatal(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseResult("Error fetching URL(s): https://frugal.sh/missing: CRAWL_NOT_FOUND", true))
	}))
	defer srv.Close()
	_, err := New(srv.URL).Extract(context.Background(), extract.Query{URL: "https://frugal.sh/missing"})
	if !routing.IsFatal(err) {
		t.Fatalf("CRAWL_NOT_FOUND must be fatal so the chain stops; got %v", err)
	}
	var re *routing.Error
	if errors.As(err, &re) && re.Status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", re.Status)
	}
	if calls != 1 {
		t.Errorf("fatal errors must not retry; got %d calls", calls)
	}
}

func TestExtract_OtherCrawlErrorIsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseResult("Error fetching URL(s): https://example.com/wall: CRAWL_BLOCKED", true))
	}))
	defer srv.Close()
	_, err := New(srv.URL).Extract(context.Background(), extract.Query{URL: "https://example.com/wall"})
	if !routing.IsPermanent(err) || routing.IsFatal(err) {
		t.Fatalf("a blocked crawl is permanent (fall through), not fatal; got %v", err)
	}
}

func TestExtract_EmptyContentIsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseResult("# Shell\nURL: https://example.com/spa\n\n", false))
	}))
	defer srv.Close()
	_, err := New(srv.URL).Extract(context.Background(), extract.Query{URL: "https://example.com/spa"})
	if !routing.IsPermanent(err) || !strings.Contains(err.Error(), "empty content") {
		t.Fatalf("want permanent empty-content error, got %v", err)
	}
}

func TestExtract_BadURLIsFatal(t *testing.T) {
	for _, u := range []string{"", "   ", "not a url", "/relative/path"} {
		_, err := New("http://127.0.0.1:1").Extract(context.Background(), extract.Query{URL: u})
		if !routing.IsFatal(err) {
			t.Errorf("%q: want fatal, got %v", u, err)
		}
	}
}

func TestExtract_RateLimitToolErrorMapsTo429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseResult("Too many requests from this IP. Please try again later.", true))
	}))
	defer srv.Close()
	_, err := New(srv.URL).Extract(context.Background(), extract.Query{URL: "https://frugal.sh"})
	var re *routing.Error
	if !errors.As(err, &re) || re.Status != http.StatusTooManyRequests {
		t.Fatalf("want status 429, got %v", err)
	}
}

func TestParseFetchText_NoPreamble(t *testing.T) {
	p := parseFetchText("Just a body with no headers.\n\nSecond paragraph.")
	if p.Title != "" || p.URL != "" {
		t.Errorf("unexpected preamble parsed: %+v", p)
	}
	if !strings.HasPrefix(p.Content, "Just a body") {
		t.Errorf("content = %q", p.Content)
	}
}

func TestParseSearchText_MissingTitleFallsBackToURL(t *testing.T) {
	items := parseSearchText("Title: N/A\nURL: https://modelcontextprotocol.io/specification/2026-07-28/changelog\nPublished: N/A\nAuthor: N/A\nHighlights:\n1. Remove protocol-level sessions.\n")
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	if items[0].Title != items[0].URL {
		t.Errorf("an N/A title must fall back to the URL; got %q", items[0].Title)
	}
}

func TestParseSearchText_UnknownHeadersSkipped(t *testing.T) {
	items := parseSearchText("Title: T\nURL: https://e.com\nScore: 0.9\nID: abc\nHighlights:\nbody here\n")
	if len(items) != 1 || items[0].Snippet != "body here" {
		t.Fatalf("items = %+v", items)
	}
}

func TestName_IsStableIdentifier(t *testing.T) {
	c := New("")
	if c.Name() != "exa-free" || Name != "exa-free" {
		t.Errorf("Name = %q", c.Name())
	}
	if c.endpoint != DefaultBaseURL {
		t.Errorf("empty baseURL must default to %s; got %s", DefaultBaseURL, c.endpoint)
	}
	if New("https://example.com/mcp/").endpoint != "https://example.com/mcp" {
		t.Errorf("trailing slash must be stripped")
	}
	var _ search.Searcher = c
	var _ extract.Extractor = c
}
