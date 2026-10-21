package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeUpstreams stands in for every provider API the smoke config wires:
// a SearXNG that always 5xxs (free, so it is retried, then fails over),
// Serper (paid search), Firecrawl (paid extract), Browserless (browse).
func fakeUpstreams(t *testing.T) (searx, serper, firecrawl, browserless *httptest.Server) {
	t.Helper()
	searx = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	serper = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"organic":[{"title":"Hit","link":"https://example.com/a","snippet":"s"}]}`)
	}))
	firecrawl = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"success":true,"data":{"markdown":"# Page\n\nbody text","metadata":{"title":"Page"}}}`)
	}))
	browserless = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<html><body><p>rendered</p></body></html>`)
	}))
	for _, s := range []*httptest.Server{searx, serper, firecrawl, browserless} {
		t.Cleanup(s.Close)
	}
	return
}

func writeSmokeConfig(t *testing.T, searx, serper, firecrawl, browserless string) string {
	t.Helper()
	cfg := fmt.Sprintf(`search_providers:
  searxng: {base_url: %q, cost_per_call: 0}
  serper: {api_key_env: FRUGAL_TEST_SERPER_KEY, base_url: %q, cost_per_call: 0.001}
  youcom: {enabled: false}
  brave: {enabled: false}
  youcom-free: {enabled: false}
  marginalia: {enabled: false}
  wikipedia: {enabled: false}
extract_providers:
  firecrawl: {api_key_env: FRUGAL_TEST_FIRECRAWL_KEY, base_url: %q, cost_per_call: 0.001}
  goreadability: {enabled: false}
  jina: {enabled: false}
browse_providers:
  browserless: {api_key_env: FRUGAL_TEST_BROWSERLESS_KEY, base_url: %q, cost_per_call: 0.002}
limits:
  retries: 1
  call_timeout: 20s
`, searx, serper, firecrawl, browserless)
	p := filepath.Join(t.TempDir(), "models.yaml")
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestE2E_StdioSmoke runs the real `frugal mcp serve` entry point over
// stdio (os.Stdin/os.Stdout swapped for pipes) against fake upstreams and
// drives every tool through an MCP client, checking the receipts.
func TestE2E_StdioSmoke(t *testing.T) {
	searx, serper, firecrawl, browserless := fakeUpstreams(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("FRUGAL_CONFIG", writeSmokeConfig(t, searx.URL, serper.URL, firecrawl.URL, browserless.URL))
	t.Setenv("FRUGAL_TEST_SERPER_KEY", "k")
	t.Setenv("FRUGAL_TEST_FIRECRAWL_KEY", "k")
	t.Setenv("FRUGAL_TEST_BROWSERLESS_KEY", "k")
	t.Setenv("SEARXNG_URL", "")
	for _, k := range []string{"SERPER_API_KEY", "BRAVE_API_KEY", "YDC_API_KEY", "FIRECRAWL_API_KEY", "BROWSERLESS_TOKEN"} {
		t.Setenv(k, "")
	}

	clientToServerR, clientToServerW, _ := os.Pipe()
	serverToClientR, serverToClientW, _ := os.Pipe()
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = clientToServerR, serverToClientW
	t.Cleanup(func() { os.Stdin, os.Stdout = oldIn, oldOut })

	done := make(chan int, 1)
	go func() { done <- runMCPServe([]string{"--call-timeout", "15s"}) }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "smoke", Version: "1"}, nil)
	sess, err := client.Connect(ctx, &sdkmcp.IOTransport{Reader: serverToClientR, Writer: clientToServerW}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	tl, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	have := map[string]bool{}
	for _, tool := range tl.Tools {
		have[tool.Name] = true
	}
	for _, n := range []string{"frugal__search", "frugal__extract", "frugal__browse", "frugal__execute"} {
		if !have[n] {
			t.Fatalf("tool %s not advertised; got %v", n, have)
		}
	}

	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := sess.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			b, _ := json.Marshal(res.Content)
			t.Fatalf("%s returned error: %s", name, b)
		}
		b, _ := json.Marshal(res.StructuredContent)
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		return out
	}
	attempts := func(out map[string]any) []map[string]any {
		raw, _ := out["attempts"].([]any)
		var as []map[string]any
		for _, a := range raw {
			m, _ := a.(map[string]any)
			as = append(as, m)
		}
		return as
	}

	// search: free SearXNG 5xx (retried, then fails over) → paid Serper wins.
	out := call("frugal__search", map[string]any{"query": "hello"})
	if out["provider_used"] != "serper" || out["cost_usd"] != 0.001 || out["total_cost_usd"] != 0.001 {
		t.Fatalf("search receipt = %v", out)
	}
	as := attempts(out)
	if len(as) != 2 || as[0]["provider"] != "searxng" || as[0]["outcome"] != "error" || as[1]["provider"] != "serper" || as[1]["outcome"] != "won" {
		t.Fatalf("search attempts = %v", as)
	}

	out = call("frugal__extract", map[string]any{"url": "https://example.com/page"})
	if out["provider_used"] != "firecrawl" || len(attempts(out)) != 1 {
		t.Fatalf("extract receipt = %v", out)
	}

	out = call("frugal__browse", map[string]any{"url": "https://example.com/page"})
	if out["provider_used"] != "browserless" || out["cost_usd"] != 0.002 || len(attempts(out)) != 1 {
		t.Fatalf("browse receipt = %v", out)
	}

	out = call("frugal__execute", map[string]any{"intent": "search the web for frugal routing"})
	if out["capability"] != "search" || out["provider_used"] != "serper" || out["reason"] == "" || len(attempts(out)) == 0 {
		t.Fatalf("execute receipt = %v", out)
	}

	_ = sess.Close()
	clientToServerW.Close()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("mcp serve exited %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("mcp serve did not exit after stdin closed")
	}
}
