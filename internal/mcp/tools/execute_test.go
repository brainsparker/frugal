package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/browse"
	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

func decodeExecuteOutput(t *testing.T, raw any) ExecuteOutput {
	t.Helper()
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out ExecuteOutput
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal ExecuteOutput: %v", err)
	}
	return out
}

func callExecute(t *testing.T, srv *sdkmcp.Server, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	client, cleanup := dialClient(t, srv)
	defer cleanup()
	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name:      "frugal__execute",
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	return res
}

func errorText(res *sdkmcp.CallToolResult) string {
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text
}

func TestExecute_SearchIntentRoutesCheapestByDefault(t *testing.T) {
	pricey := &fakeSearcher{name: "pricey", cost: 0.005, results: []search.Item{{Title: "PRICEY"}}}
	free := &fakeSearcher{name: "free", cost: 0, results: []search.Item{{Title: "FREE"}}}
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{pricey, free}, nil, nil, nil)

	res := callExecute(t, srv, map[string]any{"intent": "search for MCP registries"})
	if res.IsError {
		t.Fatalf("isError: %s", errorText(res))
	}
	out := decodeExecuteOutput(t, res.StructuredContent)
	if out.Capability != "search" || out.ProviderUsed != "free" {
		t.Errorf("got capability=%s provider=%s, want search/free", out.Capability, out.ProviderUsed)
	}
	if len(out.Results) != 1 || out.Results[0].Title != "FREE" {
		t.Errorf("results = %+v", out.Results)
	}
	if !strings.Contains(out.Reason, "policy=cheap") || !strings.Contains(out.Reason, "provider=free") {
		t.Errorf("reason = %q, want policy and provider in the trace", out.Reason)
	}
	// The verb prefix must be stripped before the provider sees it.
	if free.lastQuery.Text != "MCP registries" {
		t.Errorf("query = %q, want prefix-stripped", free.lastQuery.Text)
	}
}

func TestExecute_PremiumPriorityPrefersPriceyProvider(t *testing.T) {
	pricey := &fakeSearcher{name: "pricey", cost: 0.005, results: []search.Item{{Title: "PRICEY"}}}
	free := &fakeSearcher{name: "free", cost: 0, results: []search.Item{{Title: "FREE"}}}
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{free, pricey}, nil, nil, nil)

	res := callExecute(t, srv, map[string]any{"intent": "anything at all", "priority": "premium"})
	if res.IsError {
		t.Fatalf("isError: %s", errorText(res))
	}
	out := decodeExecuteOutput(t, res.StructuredContent)
	if out.ProviderUsed != "pricey" {
		t.Errorf("provider = %s, want pricey under priority=premium", out.ProviderUsed)
	}
	if free.lastQuery.Text != "" {
		t.Errorf("free should not have been called under premium priority")
	}
}

func TestExecute_BalancedUsesConfiguredPolicy(t *testing.T) {
	pricey := &fakeSearcher{name: "pricey", cost: 0.005, results: []search.Item{{Title: "PRICEY"}}}
	free := &fakeSearcher{name: "free", cost: 0, results: []search.Item{{Title: "FREE"}}}
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{free, pricey}, nil, nil, nil,
		WithPolicies(map[string]routing.Policy{"search": {Strategy: routing.StrategyPremium}}))

	// balanced (default) defers to the operator's configured strategy.
	res := callExecute(t, srv, map[string]any{"intent": "anything at all"})
	out := decodeExecuteOutput(t, res.StructuredContent)
	if out.ProviderUsed != "pricey" {
		t.Errorf("provider = %s, want pricey (config strategy premium)", out.ProviderUsed)
	}

	// But an explicit cheap priority overrides it.
	res = callExecute(t, srv, map[string]any{"intent": "anything at all", "priority": "cheap"})
	out = decodeExecuteOutput(t, res.StructuredContent)
	if out.ProviderUsed != "free" {
		t.Errorf("provider = %s, want free (priority=cheap overrides config)", out.ProviderUsed)
	}
}

func TestExecute_URLIntentExtracts(t *testing.T) {
	ex := &fakeExtractor{name: "goreadability", cost: 0,
		res: extract.Result{Markdown: "# Content", Title: "Post"}}
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{&fakeSearcher{name: "free"}}, []extract.Extractor{ex}, nil, nil)

	res := callExecute(t, srv, map[string]any{"intent": "read https://example.com/post"})
	if res.IsError {
		t.Fatalf("isError: %s", errorText(res))
	}
	out := decodeExecuteOutput(t, res.StructuredContent)
	if out.Capability != "extract" || out.Markdown != "# Content" || out.ProviderUsed != "goreadability" {
		t.Errorf("got %+v", out)
	}
	if ex.lastQuery.URL != "https://example.com/post" {
		t.Errorf("extract URL = %q", ex.lastQuery.URL)
	}
}

func TestExecute_EmptyExtractFallsForwardToBrowseAndSumsCost(t *testing.T) {
	ex := &fakeExtractor{name: "goreadability", cost: 0.001,
		res: extract.Result{CostUSD: 0.001}} // success, but no content: JS page
	br := &fakeBrowser{name: "browserless", cost: 0.002,
		res: browse.Result{Text: "rendered text", CostUSD: 0.002}}
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{&fakeSearcher{name: "free"}}, []extract.Extractor{ex}, []browse.Browser{br}, nil)

	res := callExecute(t, srv, map[string]any{"intent": "read https://example.com/spa"})
	if res.IsError {
		t.Fatalf("isError: %s", errorText(res))
	}
	out := decodeExecuteOutput(t, res.StructuredContent)
	if out.Capability != "browse" || out.ProviderUsed != "browserless" || out.Text != "rendered text" {
		t.Errorf("got %+v", out)
	}
	if out.CostUSD != 0.003 {
		t.Errorf("cost = %v, want 0.003 (extract + browse summed)", out.CostUSD)
	}
	if !strings.Contains(out.Reason, "fell forward") {
		t.Errorf("reason = %q, want fall-forward explanation", out.Reason)
	}
}

func TestExecute_FatalExtractDoesNotFallForward(t *testing.T) {
	ex := &fakeExtractor{name: "goreadability", cost: 0,
		err: routing.Fatal("goreadability", 404, nil)}
	br := &fakeBrowser{name: "browserless", cost: 0.002,
		res: browse.Result{Text: "should never render"}}
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{&fakeSearcher{name: "free"}}, []extract.Extractor{ex}, []browse.Browser{br}, nil)

	res := callExecute(t, srv, map[string]any{"intent": "read https://example.com/gone"})
	if !res.IsError {
		t.Fatal("expected error for a fatally-dead URL")
	}
	if br.lastQuery.URL != "" {
		t.Error("browse must not run after a fatal extract error — the page is gone for everyone")
	}
}

func TestExecute_PinnedWrongCapabilityErrors(t *testing.T) {
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{&fakeSearcher{name: "free", results: []search.Item{{Title: "x"}}}}, nil, nil, nil)

	// goreadability is an extract provider; the intent classifies to search.
	res := callExecute(t, srv, map[string]any{"intent": "search for anything", "provider": "goreadability"})
	if !res.IsError {
		t.Fatal("expected error pinning a provider the capability doesn't have")
	}
	if !strings.Contains(errorText(res), "not configured") {
		t.Errorf("error = %q, want not-configured mention", errorText(res))
	}
}

func TestExecute_DeniedPinErrors(t *testing.T) {
	pricey := &fakeSearcher{name: "youcom", cost: 0.005, results: []search.Item{{Title: "x"}}}
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{pricey}, nil, nil, nil,
		WithPolicies(map[string]routing.Policy{"search": {Deny: map[string]bool{"youcom": true}}}))

	res := callExecute(t, srv, map[string]any{"intent": "search for anything", "provider": "youcom"})
	if !res.IsError {
		t.Fatal("expected error for a denied pinned provider")
	}
	if !strings.Contains(errorText(res), "denied by the routing policy") {
		t.Errorf("error = %q", errorText(res))
	}
	if pricey.lastQuery.Text != "" {
		t.Error("denied provider must never be called")
	}
}

func TestExecute_MissingCapabilityProvidersErrors(t *testing.T) {
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{&fakeSearcher{name: "free"}}, nil, nil, nil)

	res := callExecute(t, srv, map[string]any{"intent": "read https://example.com/post"})
	if !res.IsError {
		t.Fatal("expected error when the classified capability has no providers")
	}
	if !strings.Contains(errorText(res), "no extract providers") {
		t.Errorf("error = %q, want missing-capability mention", errorText(res))
	}
}

func TestExecute_InvalidPriorityErrors(t *testing.T) {
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{&fakeSearcher{name: "free"}}, nil, nil, nil)

	res := callExecute(t, srv, map[string]any{"intent": "anything", "priority": "fastest"})
	if !res.IsError {
		t.Fatal("expected error for unknown priority")
	}
}

func TestExecute_NotRegisteredWithoutSearchers(t *testing.T) {
	srv := newServer()
	RegisterExecute(srv, nil, []extract.Extractor{&fakeExtractor{name: "goreadability"}}, nil, nil)

	client, cleanup := dialClient(t, srv)
	defer cleanup()
	res, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 0 {
		t.Errorf("expected no tools without searchers, got %d", len(res.Tools))
	}
}
