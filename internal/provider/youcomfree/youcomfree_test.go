package youcomfree

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

	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// resultDoc is the you-search output the hosted server returns, as
// captured from a live keyless call (structuredContent and the text
// block carry the same document).
const resultDoc = `{"results":{"web":[
  {"url":"https://www.speakeasy.com/blog/ai-agent-framework-comparison","title":"Choosing an agent framework","description":"No framework perfectly meets all five criteria.","snippets":["Python teams building stateful agents should use LangGraph."],"page_age":"2026-05-02"},
  {"url":"https://example.com/no-description","title":"Snippets only","snippets":["first relevant passage","second"]},
  {"title":"no url, must be dropped"}
]}}`

// sseBody wraps the result document the way the live endpoint streams
// it: a log notification first, then the response, each as one event
// whose data is a single line (SSE has no continuation lines without a
// field name, so the document is compacted first).
func sseBody(result string) string {
	var compact strings.Builder
	var buf json.RawMessage
	if err := json.Unmarshal([]byte(result), &buf); err == nil {
		b, _ := json.Marshal(buf)
		compact.Write(b)
		result = compact.String()
	}
	return "event: message\n" +
		`data: {"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info","data":"Search successful"}}` + "\n\n" +
		"event: message\n" +
		`data: {"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":` + strconv(result) + `}],"structuredContent":` + result + `}}` + "\n\n"
}

// strconv JSON-encodes s as a string literal.
func strconv(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestSearch_HappyPath_SSE(t *testing.T) {
	var gotBody []byte
	var gotHeaders http.Header
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: got %s want POST", r.Method)
		}
		gotQuery = r.URL.RawQuery
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseBody(resultDoc))
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.Search(context.Background(), search.Query{Text: "AI agent framework comparison", MaxResults: 3, Freshness: "month"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 items (the url-less hit dropped), got %d: %+v", len(res.Items), res.Items)
	}
	if res.Items[0].Title != "Choosing an agent framework" || res.Items[0].Snippet != "No framework perfectly meets all five criteria." {
		t.Errorf("item[0] = %+v", res.Items[0])
	}
	if res.Items[0].PublishedAt != "2026-05-02" {
		t.Errorf("item[0] published: got %q", res.Items[0].PublishedAt)
	}
	// snippets[0] fallback when description is empty, same rule as the
	// keyed youcom driver.
	if res.Items[1].Snippet != "first relevant passage" {
		t.Errorf("item[1] snippet should fall back to snippets[0]; got %q", res.Items[1].Snippet)
	}
	if res.CostUSD != 0 {
		t.Errorf("CostUSD must be 0; got %v", res.CostUSD)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("freshness is honored natively; no warning expected, got %v", res.Warnings)
	}

	// Wire shape: the free profile is selected in the query string, the
	// body is a JSON-RPC tools/call for you-search with query, count,
	// and freshness passed through.
	if gotQuery != "profile=free" {
		t.Errorf("query string: got %q want profile=free", gotQuery)
	}
	var rpc struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(gotBody, &rpc); err != nil {
		t.Fatalf("request body is not JSON: %v\n%s", err, gotBody)
	}
	if rpc.JSONRPC != "2.0" || rpc.Method != "tools/call" || rpc.Params.Name != "you-search" {
		t.Errorf("rpc envelope = %+v", rpc)
	}
	if rpc.Params.Arguments["query"] != "AI agent framework comparison" {
		t.Errorf("query arg = %v", rpc.Params.Arguments["query"])
	}
	if rpc.Params.Arguments["count"] != float64(3) {
		t.Errorf("count arg = %v want 3", rpc.Params.Arguments["count"])
	}
	if rpc.Params.Arguments["freshness"] != "month" {
		t.Errorf("freshness arg = %v want month", rpc.Params.Arguments["freshness"])
	}
	if got := gotHeaders.Get("Accept"); !strings.Contains(got, "text/event-stream") || !strings.Contains(got, "application/json") {
		t.Errorf("Accept must allow both JSON and SSE; got %q", got)
	}
	if !strings.Contains(gotHeaders.Get("User-Agent"), "frugal") {
		t.Errorf("User-Agent should identify as frugal; got %q", gotHeaders.Get("User-Agent"))
	}
	if gotHeaders.Get("Mcp-Method") != "tools/call" || gotHeaders.Get("Mcp-Name") != "you-search" {
		t.Errorf("header routing hints: Mcp-Method=%q Mcp-Name=%q", gotHeaders.Get("Mcp-Method"), gotHeaders.Get("Mcp-Name"))
	}
}

func TestSearch_HappyPath_JSON_TextFallback(t *testing.T) {
	// A plain JSON response with no structuredContent: the text block
	// carries the document and must be decoded instead.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":`+strconv(resultDoc)+`}]}}`)
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Items) != 2 || res.Items[0].URL != "https://www.speakeasy.com/blog/ai-agent-framework-comparison" {
		t.Errorf("unexpected items: %+v", res.Items)
	}
}

func TestSearch_DefaultsAndClamp(t *testing.T) {
	var counts []float64
	var freshnessSeen bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc struct {
			Params struct {
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&rpc)
		counts = append(counts, rpc.Params.Arguments["count"].(float64))
		if _, ok := rpc.Params.Arguments["freshness"]; ok {
			freshnessSeen = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"structuredContent":{"results":{"web":[]}}}}`)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.Search(context.Background(), search.Query{Text: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Search(context.Background(), search.Query{Text: "a", MaxResults: 50}); err != nil {
		t.Fatal(err)
	}
	if len(counts) != 2 || counts[0] != 5 || counts[1] != 20 {
		t.Errorf("count defaults/clamp: got %v want [5 20]", counts)
	}
	if freshnessSeen {
		t.Errorf("freshness must be omitted from the arguments when the query has none")
	}
}

func TestSearch_ZeroHitsIsEmptySuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"structuredContent":{"results":{"web":[]}}}}`)
	}))
	defer srv.Close()
	res, err := New(srv.URL).Search(context.Background(), search.Query{Text: "nothing"})
	if err != nil {
		t.Fatalf("zero hits must not error: %v", err)
	}
	if len(res.Items) != 0 {
		t.Errorf("expected no items, got %+v", res.Items)
	}
}

func TestSearch_429IsTransientWithStatus(t *testing.T) {
	// The daily quota refusal must surface with Status 429 so the
	// routing guard opens its cooldown instead of the chain re-probing.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x"})
	if err == nil {
		t.Fatalf("expected error on 429")
	}
	var re *routing.Error
	if !errors.As(err, &re) {
		t.Fatalf("expected *routing.Error, got %T %v", err, err)
	}
	if re.Status != http.StatusTooManyRequests || re.Kind != routing.KindTransient {
		t.Errorf("429 must be transient with Status 429; got %+v", re)
	}
	if !strings.Contains(err.Error(), "YDC_API_KEY") {
		t.Errorf("429 message should point at the keyed upgrade path; got %q", err)
	}
	// Transient, so the in-driver retry loop ran its full schedule.
	if calls.Load() != int32(1+len(routing.DefaultBackoff)) {
		t.Errorf("expected %d attempts, got %d", 1+len(routing.DefaultBackoff), calls.Load())
	}
}

func TestSearch_ToolErrorRateLimitMapsTo429(t *testing.T) {
	// Quota refusal delivered as an MCP tool error (isError: true) on an
	// HTTP 200: classified like a 429 so the cooldown applies.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[{"type":"text","text":"Rate limit exceeded for the free profile. Try again tomorrow."}]}}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x"})
	var re *routing.Error
	if !errors.As(err, &re) {
		t.Fatalf("expected *routing.Error, got %T %v", err, err)
	}
	if re.Status != http.StatusTooManyRequests || !routing.IsTransient(err) {
		t.Errorf("tool-level rate limit must map to a transient 429; got %+v", re)
	}
}

func TestSearch_OtherToolErrorIsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[{"type":"text","text":"query too long"}]}}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x"})
	if err == nil || !routing.IsPermanent(err) {
		t.Errorf("non-quota tool error must be permanent; got %v", err)
	}
	if !strings.Contains(err.Error(), "query too long") {
		t.Errorf("tool error text should be preserved; got %q", err)
	}
}

func TestSearch_RPCErrorIsPermanent(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"Unknown tool: you-search"}}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x"})
	if err == nil || !routing.IsPermanent(err) {
		t.Errorf("JSON-RPC error must be permanent; got %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("permanent errors must not retry; got %d attempts", calls.Load())
	}
	if !strings.Contains(err.Error(), "Unknown tool") {
		t.Errorf("rpc error message should be preserved; got %q", err)
	}
}

func TestSearch_5xxRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseBody(resultDoc))
	}))
	defer srv.Close()
	res, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x"})
	if err != nil {
		t.Fatalf("expected retry to recover, got %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 attempts, got %d", calls.Load())
	}
	if len(res.Items) != 2 {
		t.Errorf("unexpected result: %+v", res.Items)
	}
}

func TestSearch_4xxIsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `blocked`)
	}))
	defer srv.Close()
	_, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x"})
	if err == nil || !routing.IsPermanent(err) {
		t.Errorf("403 must classify as permanent; got %v", err)
	}
}

func TestSearch_MalformedBodyIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{not json`)
	}))
	defer srv.Close()
	_, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x"})
	if err == nil || !routing.IsTransient(err) {
		t.Errorf("malformed 200 body must be transient; got %v", err)
	}
}

func TestSearch_SSEWithoutResponseIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{}}\n\n: keepalive\n\n")
	}))
	defer srv.Close()
	_, err := New(srv.URL).Search(context.Background(), search.Query{Text: "x"})
	if err == nil || !routing.IsTransient(err) {
		t.Errorf("a stream with only notifications must be transient; got %v", err)
	}
}

func TestSearch_NetworkErrorIsTransient(t *testing.T) {
	c := New("http://127.0.0.1:1")
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err == nil || !routing.IsTransient(err) {
		t.Errorf("network failure must classify as transient; got %v", err)
	}
}

func TestSearch_EmptyQueryIsPermanent(t *testing.T) {
	_, err := New("http://example.invalid").Search(context.Background(), search.Query{})
	if err == nil || !routing.IsPermanent(err) {
		t.Errorf("empty query must be permanent; got %v", err)
	}
}

func TestSearch_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(srv.URL).Search(ctx, search.Query{Text: "x"})
	if err == nil {
		t.Fatalf("expected error from cancelled context")
	}
}

func TestNew_EndpointShape(t *testing.T) {
	cases := map[string]string{
		"":                              DefaultBaseURL + "?profile=free",
		"https://api.you.com/mcp/":      "https://api.you.com/mcp?profile=free",
		"https://proxy.example/mcp?x=1": "https://proxy.example/mcp?x=1&profile=free",
	}
	for in, want := range cases {
		if got := New(in).endpoint; got != want {
			t.Errorf("New(%q).endpoint = %q, want %q", in, got, want)
		}
	}
	if New("").Name() != "youcom-free" {
		t.Errorf("Name() = %q", New("").Name())
	}
	if New("").CostPerCall() != 0 {
		t.Errorf("CostPerCall() must be 0")
	}
}
