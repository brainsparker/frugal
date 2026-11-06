package parallelfree

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

// searchDoc is the web_search output the hosted server returns, shaped
// after a live keyless call on 2026-11-06 (structuredContent and the
// text block carry the same document). The third hit has no URL and
// must be dropped; the second has a long excerpt that must be clipped.
var searchDoc = `{"search_id":"search_74238a0d4157ae87280d185ebff6534b","results":[
  {"url":"https://arize.com/guides/ai-agent-handbook/agent-frameworks","title":"AI agent frameworks compared","publish_date":"2026-07-30","excerpts":["A developer's guide to agent loops, tools, state, memory."]},
  {"url":"https://camunda.com/learn/ai-agent-frameworks","title":"AI Agent Frameworks: How They Work","publish_date":"2026-10-21","excerpts":["` + longExcerpt + `","second excerpt ignored"]},
  {"title":"no url, must be dropped","excerpts":["x"]},
  {"url":"https://example.com/fourth","title":"Fourth","excerpts":[]}
],"warnings":null,"metadata":{},"session_id":"session_8a911eb27c7a4afaa20d0d9dc98d07c0"}`

// longExcerpt is well past maxSnippetChars so the clip rule is exercised.
var longExcerpt = strings.Repeat("word ", 300)

// fetchDoc is the web_fetch output for one URL with full_content on.
const fetchDoc = `{"extract_id":"extract_1bfde6a411d4dfbbdad174d7733ed12c","results":[
  {"url":"https://frugal.sh","title":"frugal: the open routing layer for AI tools","publish_date":null,"excerpts":["# frugal\n\nThe open routing layer for AI tools.","## It's just a router.\n\nOne Go binary."]}
],"errors":[],"warnings":[{"type":"warning","message":"Neither objective nor search_queries were provided","detail":null}],"metadata":{},"session_id":"session_1bfde6a411d4dfbbdad174d7733ed12c"}`

// sseBody wraps a result document the way the live endpoint streams
// it: an `event: message` line and a single-line `data:` JSON-RPC
// response. The document is compacted first because SSE has no
// continuation lines without a field name.
func sseBody(result string) string {
	var buf json.RawMessage
	if err := json.Unmarshal([]byte(result), &buf); err == nil {
		b, _ := json.Marshal(buf)
		result = string(b)
	}
	return "event: message\n" +
		`data: {"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":` + jsonString(result) + `}],"isError":false,"structuredContent":` + result + `}}` + "\n\n"
}

// jsonBody is the same response as a plain JSON document, without the
// structuredContent field so the text-block fallback is exercised.
func jsonBody(result string) string {
	return `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":` + jsonString(result) + `}],"isError":false}}`
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestSearch_HappyPath_SSE(t *testing.T) {
	var gotBody []byte
	var gotHeaders http.Header
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: got %s want POST", r.Method)
		}
		gotPath = r.URL.Path
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseBody(searchDoc))
	}))
	defer srv.Close()

	c := New(srv.URL + "/mcp/")
	res, err := c.Search(context.Background(), search.Query{Text: "AI agent framework comparison", MaxResults: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotPath != "/mcp" {
		t.Errorf("trailing slash must be stripped (live server 404s on it); got path %q", gotPath)
	}
	if len(res.Items) != 3 {
		t.Fatalf("expected 3 items (the url-less hit dropped), got %d: %+v", len(res.Items), res.Items)
	}
	if res.Items[0].Title != "AI agent frameworks compared" || res.Items[0].Snippet != "A developer's guide to agent loops, tools, state, memory." {
		t.Errorf("item[0] = %+v", res.Items[0])
	}
	if res.Items[0].PublishedAt != "2026-07-30" {
		t.Errorf("item[0] published: got %q", res.Items[0].PublishedAt)
	}
	if n := len(res.Items[1].Snippet); n > maxSnippetChars+3 || !strings.HasSuffix(res.Items[1].Snippet, "...") {
		t.Errorf("long excerpt must be clipped to about %d chars with an ellipsis; got %d chars ending %q", maxSnippetChars, n, res.Items[1].Snippet[max(0, n-10):])
	}
	if res.Items[2].Snippet != "" {
		t.Errorf("a hit with no excerpts gets an empty snippet; got %q", res.Items[2].Snippet)
	}
	if res.CostUSD != 0 {
		t.Errorf("cost must be 0, got %v", res.CostUSD)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("no freshness asked, no warning expected; got %v", res.Warnings)
	}

	// Request shape: a JSON-RPC tools/call for web_search carrying the
	// query as both objective and the single search query, and nothing
	// else (no session_id, no client_model).
	var rpc rpcRequest
	if err := json.Unmarshal(gotBody, &rpc); err != nil {
		t.Fatalf("request body is not JSON-RPC: %v\n%s", err, gotBody)
	}
	if rpc.Method != "tools/call" || rpc.Params.Name != "web_search" {
		t.Errorf("request = %s %s, want tools/call web_search", rpc.Method, rpc.Params.Name)
	}
	if rpc.Params.Arguments["objective"] != "AI agent framework comparison" {
		t.Errorf("objective = %v", rpc.Params.Arguments["objective"])
	}
	if qs, _ := rpc.Params.Arguments["search_queries"].([]any); len(qs) != 1 || qs[0] != "AI agent framework comparison" {
		t.Errorf("search_queries = %v", rpc.Params.Arguments["search_queries"])
	}
	for _, forbidden := range []string{"session_id", "client_model"} {
		if _, ok := rpc.Params.Arguments[forbidden]; ok {
			t.Errorf("driver must not send %s", forbidden)
		}
	}
	if got := gotHeaders.Get("Mcp-Name"); got != "web_search" {
		t.Errorf("Mcp-Name header = %q", got)
	}
	if got := gotHeaders.Get("Mcp-Method"); got != "tools/call" {
		t.Errorf("Mcp-Method header = %q", got)
	}
	if got := gotHeaders.Get("Accept"); !strings.Contains(got, "text/event-stream") {
		t.Errorf("Accept must admit SSE; got %q", got)
	}
	if got := gotHeaders.Get("User-Agent"); got != DefaultUserAgent {
		t.Errorf("User-Agent = %q", got)
	}
}

func TestSearch_PlainJSON_TextFallback_AndClamp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, jsonBody(searchDoc))
	}))
	defer srv.Close()

	res, err := New(srv.URL).Search(context.Background(), search.Query{Text: "q", MaxResults: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("max_results must clamp client-side (the tool has no count argument); got %d", len(res.Items))
	}
	if res.Items[1].URL != "https://camunda.com/learn/ai-agent-frameworks" {
		t.Errorf("clamp must keep relevance order; item[1] = %+v", res.Items[1])
	}
}

func TestSearch_FreshnessFoldedIntoObjectiveWithWarning(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseBody(searchDoc))
	}))
	defer srv.Close()

	res, err := New(srv.URL).Search(context.Background(), search.Query{Text: "mcp spec changes", Freshness: "week"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	var rpc rpcRequest
	_ = json.Unmarshal(gotBody, &rpc)
	obj, _ := rpc.Params.Arguments["objective"].(string)
	if !strings.HasPrefix(obj, "mcp spec changes") || !strings.Contains(obj, "past week") {
		t.Errorf("objective should carry the freshness preference; got %q", obj)
	}
	if qs, _ := rpc.Params.Arguments["search_queries"].([]any); len(qs) != 1 || qs[0] != "mcp spec changes" {
		t.Errorf("search_queries must stay the bare query; got %v", rpc.Params.Arguments["search_queries"])
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "not enforced") {
		t.Errorf("agent must be told the window is a preference; got %v", res.Warnings)
	}
}

func TestSearch_EmptyQueryIsPermanent(t *testing.T) {
	_, err := New("http://127.0.0.1:1").Search(context.Background(), search.Query{Text: "   "})
	var re *routing.Error
	if !errors.As(err, &re) || re.Kind != routing.KindPermanent {
		t.Fatalf("want Permanent routing.Error, got %v", err)
	}
}

func TestSearch_ZeroHitsIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseBody(`{"search_id":"s","results":[],"session_id":"x"}`))
	}))
	defer srv.Close()
	res, err := New(srv.URL).Search(context.Background(), search.Query{Text: "zxqv"})
	if err != nil {
		t.Fatalf("zero hits must return an empty list, not an error (the router handles fall-through): %v", err)
	}
	if len(res.Items) != 0 {
		t.Errorf("got %d items", len(res.Items))
	}
}

func TestSearch_HTTP429_CarriesStatusAndRetryAfter(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, "rate limit exceeded")
	}))
	defer srv.Close()

	_, err := New(srv.URL).Search(context.Background(), search.Query{Text: "q"})
	var re *routing.Error
	if !errors.As(err, &re) {
		t.Fatalf("want routing.Error, got %T %v", err, err)
	}
	if re.Status != http.StatusTooManyRequests {
		t.Errorf("Status = %d, want 429 so the guard opens a cooldown", re.Status)
	}
	if re.Kind != routing.KindTransient {
		t.Errorf("Kind = %v, want Transient", re.Kind)
	}
	if re.RetryAfter <= 0 {
		t.Errorf("RetryAfter should be parsed from the header; got %v", re.RetryAfter)
	}
	if !strings.Contains(re.Error(), "rate limited") {
		t.Errorf("error should explain the keyless tier; got %q", re.Error())
	}
	if n := atomic.LoadInt32(&calls); n < 2 {
		t.Errorf("a free provider retries transient failures; got %d call(s)", n)
	}
}

func TestSearch_ToolErrorWithQuotaWordingIs429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"Rate limit exceeded for anonymous users. Sign in for higher limits."}],"isError":true}}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL).Search(context.Background(), search.Query{Text: "q"})
	var re *routing.Error
	if !errors.As(err, &re) || re.Status != http.StatusTooManyRequests || re.Kind != routing.KindTransient {
		t.Fatalf("a quota refusal in a tool error must surface as a 429 transient; got %v", err)
	}
}

func TestSearch_OtherToolErrorIsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"invalid search_queries"}],"isError":true}}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL).Search(context.Background(), search.Query{Text: "q"})
	var re *routing.Error
	if !errors.As(err, &re) || re.Kind != routing.KindPermanent {
		t.Fatalf("want Permanent, got %v", err)
	}
}

func TestSearch_RPCErrorIsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"Unknown tool: web_search"}}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL).Search(context.Background(), search.Query{Text: "q"})
	var re *routing.Error
	if !errors.As(err, &re) || re.Kind != routing.KindPermanent || !strings.Contains(re.Error(), "-32602") {
		t.Fatalf("want Permanent carrying the rpc code, got %v", err)
	}
}

func TestSearch_5xxIsTransientAndRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseBody(searchDoc))
	}))
	defer srv.Close()
	res, err := New(srv.URL).Search(context.Background(), search.Query{Text: "q"})
	if err != nil {
		t.Fatalf("second attempt should succeed: %v", err)
	}
	if len(res.Items) == 0 || atomic.LoadInt32(&calls) != 2 {
		t.Errorf("items=%d calls=%d", len(res.Items), calls)
	}
}

func TestSearch_SSENotificationsBeforeResultAreSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": keepalive\n\n")
		_, _ = io.WriteString(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":1}}\n\n")
		_, _ = io.WriteString(w, sseBody(searchDoc))
	}))
	defer srv.Close()
	res, err := New(srv.URL).Search(context.Background(), search.Query{Text: "q"})
	if err != nil || len(res.Items) != 3 {
		t.Fatalf("items=%d err=%v", len(res.Items), err)
	}
}

func TestSearch_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(srv.URL).Search(ctx, search.Query{Text: "q"})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestExtract_HappyPath(t *testing.T) {
	var gotBody []byte
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseBody(fetchDoc))
	}))
	defer srv.Close()

	res, err := New(srv.URL).Extract(context.Background(), extract.Query{URL: "https://frugal.sh"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Title != "frugal: the open routing layer for AI tools" {
		t.Errorf("title = %q", res.Title)
	}
	if !strings.HasPrefix(res.Markdown, "# frugal") || !strings.Contains(res.Markdown, "\n\n## It's just a router.") {
		t.Errorf("excerpts must be joined into one markdown document; got %q", res.Markdown)
	}
	if res.Text != res.Markdown {
		t.Errorf("text should mirror markdown (one format per request)")
	}
	if res.HTML != "" {
		t.Errorf("no html is available from this provider; got %q", res.HTML)
	}
	if res.CostUSD != 0 {
		t.Errorf("cost = %v, want 0", res.CostUSD)
	}

	var rpc rpcRequest
	if err := json.Unmarshal(gotBody, &rpc); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if rpc.Params.Name != "web_fetch" {
		t.Errorf("tool = %q, want web_fetch", rpc.Params.Name)
	}
	if urls, _ := rpc.Params.Arguments["urls"].([]any); len(urls) != 1 || urls[0] != "https://frugal.sh" {
		t.Errorf("urls = %v", rpc.Params.Arguments["urls"])
	}
	if fc, _ := rpc.Params.Arguments["full_content"].(bool); !fc {
		t.Errorf("full_content must be true: frugal__extract is a reader, not an excerpt tool")
	}
	if got := gotHeaders.Get("Mcp-Name"); got != "web_fetch" {
		t.Errorf("Mcp-Name header = %q", got)
	}
}

func TestExtract_TargetNotFoundIsFatal(t *testing.T) {
	doc := `{"extract_id":"e","results":[],"errors":[{"url":"https://frugal.sh/gone","error_type":"http_error","http_status_code":404,"content":null}],"session_id":"s"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseBody(doc))
	}))
	defer srv.Close()
	_, err := New(srv.URL).Extract(context.Background(), extract.Query{URL: "https://frugal.sh/gone"})
	var re *routing.Error
	if !errors.As(err, &re) || re.Kind != routing.KindFatal || re.Status != http.StatusNotFound {
		t.Fatalf("a dead target must be Fatal so no paid rung is tried; got %v", err)
	}
}

func TestExtract_TargetForbiddenIsPermanent(t *testing.T) {
	doc := `{"extract_id":"e","results":[],"errors":[{"url":"https://example.com/wall","error_type":"http_error","http_status_code":403,"content":"blocked"}],"session_id":"s"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, jsonBody(doc))
	}))
	defer srv.Close()
	_, err := New(srv.URL).Extract(context.Background(), extract.Query{URL: "https://example.com/wall"})
	var re *routing.Error
	if !errors.As(err, &re) || re.Kind != routing.KindPermanent {
		t.Fatalf("an anti-bot wall must fall through to the next renderer; got %v", err)
	}
	if !strings.Contains(re.Error(), "403") {
		t.Errorf("error should name the target status; got %q", re.Error())
	}
}

func TestExtract_EmptyContentIsPermanent(t *testing.T) {
	doc := `{"extract_id":"e","results":[{"url":"https://example.com/app","title":"App","excerpts":["   "]}],"errors":[],"session_id":"s"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseBody(doc))
	}))
	defer srv.Close()
	_, err := New(srv.URL).Extract(context.Background(), extract.Query{URL: "https://example.com/app"})
	var re *routing.Error
	if !errors.As(err, &re) || re.Kind != routing.KindPermanent {
		t.Fatalf("want Permanent on empty content, got %v", err)
	}
}

func TestExtract_HTMLOnlyAskFallsThroughWithoutNetwork(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
	}))
	defer srv.Close()
	_, err := New(srv.URL).Extract(context.Background(), extract.Query{URL: "https://example.com", Formats: []string{"html"}})
	var re *routing.Error
	if !errors.As(err, &re) || re.Kind != routing.KindPermanent {
		t.Fatalf("want Permanent, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Errorf("no request should be made for a format the provider cannot return")
	}
	// markdown alongside html is fine: the caller gets markdown.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseBody(fetchDoc))
	}))
	defer srv2.Close()
	if _, err := New(srv2.URL).Extract(context.Background(), extract.Query{URL: "https://frugal.sh", Formats: []string{"html", "markdown"}}); err != nil {
		t.Errorf("markdown+html should succeed: %v", err)
	}
}

func TestExtract_BadURLIsFatal(t *testing.T) {
	for _, u := range []string{"", "   ", "not-a-url", "/relative/path"} {
		_, err := New("http://127.0.0.1:1").Extract(context.Background(), extract.Query{URL: u})
		var re *routing.Error
		if !errors.As(err, &re) || re.Kind != routing.KindFatal {
			t.Errorf("%q: want Fatal, got %v", u, err)
		}
	}
}

func TestExtract_HTTP429_IsTransientWithStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	_, err := New(srv.URL).Extract(context.Background(), extract.Query{URL: "https://example.com"})
	var re *routing.Error
	if !errors.As(err, &re) || re.Status != http.StatusTooManyRequests || re.Kind != routing.KindTransient {
		t.Fatalf("want 429 transient, got %v", err)
	}
}

func TestNameAndCost(t *testing.T) {
	c := New("")
	if c.Name() != "parallel-free" {
		t.Errorf("Name = %q", c.Name())
	}
	if c.CostPerCall() != 0 {
		t.Errorf("CostPerCall = %v", c.CostPerCall())
	}
	if c.endpoint != DefaultBaseURL {
		t.Errorf("endpoint = %q, want %q", c.endpoint, DefaultBaseURL)
	}
}

func TestClip(t *testing.T) {
	if got := clip("short", 10); got != "short" {
		t.Errorf("clip short = %q", got)
	}
	got := clip("alpha beta gamma delta epsilon", 17)
	if got != "alpha beta gamma..." {
		t.Errorf("clip on word boundary = %q", got)
	}
	// Multi-byte safety: never split a rune.
	s := strings.Repeat("é", 50)
	got = clip(s, 21)
	if !strings.HasSuffix(got, "...") || strings.ContainsRune(got, '\uFFFD') {
		t.Errorf("clip must not split runes; got %q", got)
	}
}

func TestSameURL(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"https://frugal.sh", "https://frugal.sh/", true},
		{"http://Frugal.sh/", "https://frugal.sh", true},
		{"https://frugal.sh/Docs", "https://frugal.sh/docs", false},
		{"https://a.com", "https://b.com", false},
	}
	for _, c := range cases {
		if got := sameURL(c.a, c.b); got != c.want {
			t.Errorf("sameURL(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
