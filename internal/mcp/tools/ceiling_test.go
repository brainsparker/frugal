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

func callTool(t *testing.T, srv *sdkmcp.Server, name string, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	client, cleanup := dialClient(t, srv)
	defer cleanup()
	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

func TestCeiling_AdvertisedOnEveryToolSchema(t *testing.T) {
	// Agents discover arguments from tools/list; the ceiling must be in
	// every routed tool's input schema or nobody will ever set it.
	srv := newServer()
	searchers := []search.Searcher{&fakeSearcher{name: "free"}}
	extractors := []extract.Extractor{&fakeExtractor{name: "goreadability"}}
	browsers := []browse.Browser{&fakeBrowser{name: "browserless", cost: 0.002}}
	RegisterSearch(srv, searchers, nil)
	RegisterExtract(srv, extractors, nil)
	RegisterBrowse(srv, browsers, nil)
	RegisterExecute(srv, searchers, extractors, browsers, nil)

	client, cleanup := dialClient(t, srv)
	defer cleanup()
	res, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(res.Tools))
	}
	for _, tool := range res.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal schema for %s: %v", tool.Name, err)
		}
		if !strings.Contains(string(raw), `"max_cost_usd"`) {
			t.Errorf("%s input schema lacks max_cost_usd: %s", tool.Name, raw)
		}
	}
}

func TestCeiling_SearchSkipsProvidersAboveCeiling(t *testing.T) {
	// Under a premium policy youcom would win; the caller's ceiling
	// removes it from the chain before any call, so serper serves.
	pricey := &fakeSearcher{name: "youcom", cost: 0.005, results: []search.Item{{Title: "PRICEY"}}}
	cheap := &fakeSearcher{name: "serper", cost: 0.001, results: []search.Item{{Title: "CHEAP"}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{cheap, pricey}, nil,
		WithPolicy(routing.Policy{Strategy: routing.StrategyPremium}))

	res := callTool(t, srv, "frugal__search", map[string]any{"query": "anything", "max_cost_usd": 0.001})
	if res.IsError {
		t.Fatalf("isError: %s", errorText(res))
	}
	out, err := decodeSearchOutput(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if out.ProviderUsed != "serper" {
		t.Errorf("provider_used = %q, want serper (youcom above ceiling)", out.ProviderUsed)
	}
	if pricey.lastQuery.Text != "" {
		t.Error("a provider above the ceiling must never be called")
	}
}

func TestCeiling_ZeroMeansFreeOnly(t *testing.T) {
	free := &fakeSearcher{name: "marginalia", cost: 0, results: []search.Item{{Title: "FREE"}}}
	paid := &fakeSearcher{name: "serper", cost: 0.001, results: []search.Item{{Title: "PAID"}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{paid, free}, nil)

	res := callTool(t, srv, "frugal__search", map[string]any{"query": "anything", "max_cost_usd": 0})
	if res.IsError {
		t.Fatalf("isError: %s", errorText(res))
	}
	out, _ := decodeSearchOutput(res.StructuredContent)
	if out.ProviderUsed != "marginalia" || out.CostUSD != 0 {
		t.Errorf("got provider %q cost %v, want the free rung at $0", out.ProviderUsed, out.CostUSD)
	}
	if paid.lastQuery.Text != "" {
		t.Error("paid provider must not be called under a zero ceiling")
	}
}

func TestCeiling_ZeroStillFallsThroughFreeRungs(t *testing.T) {
	// Free-only must keep the normal zero-hit fall-through between free
	// providers; the ceiling only trims the chain, it does not change the
	// fallback walk.
	whiff := &fakeSearcher{name: "marginalia", cost: 0, results: nil}
	hit := &fakeSearcher{name: "wikipedia", cost: 0, results: []search.Item{{Title: "HIT"}}}
	paid := &fakeSearcher{name: "youcom", cost: 0.005, results: []search.Item{{Title: "PAID"}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{whiff, hit, paid}, nil)

	res := callTool(t, srv, "frugal__search", map[string]any{"query": "anything", "max_cost_usd": 0})
	if res.IsError {
		t.Fatalf("isError: %s", errorText(res))
	}
	out, _ := decodeSearchOutput(res.StructuredContent)
	if out.ProviderUsed != "wikipedia" {
		t.Errorf("provider_used = %q, want wikipedia after marginalia whiffed", out.ProviderUsed)
	}
	if paid.lastQuery.Text != "" {
		t.Error("paid provider must not be reached under a zero ceiling")
	}
}

func TestCeiling_AllAboveCeilingErrorsNamingCheapest(t *testing.T) {
	a := &fakeSearcher{name: "youcom", cost: 0.005, results: []search.Item{{Title: "x"}}}
	b := &fakeSearcher{name: "serper", cost: 0.001, results: []search.Item{{Title: "x"}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{a, b}, nil)

	res := callTool(t, srv, "frugal__search", map[string]any{"query": "anything", "max_cost_usd": 0.0005})
	if !res.IsError {
		t.Fatal("expected an error when every provider is above the ceiling")
	}
	msg := errorText(res)
	for _, want := range []string{"max_cost_usd", "$0.0005", "serper", "$0.0010"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
	if a.lastQuery.Text != "" || b.lastQuery.Text != "" {
		t.Error("no provider may be called when all are above the ceiling")
	}
}

func TestCeiling_PinnedProviderAboveCeilingErrors(t *testing.T) {
	pinned := &fakeSearcher{name: "youcom", cost: 0.005, results: []search.Item{{Title: "x"}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{pinned}, nil)

	res := callTool(t, srv, "frugal__search", map[string]any{"query": "anything", "provider": "youcom", "max_cost_usd": 0.001})
	if !res.IsError {
		t.Fatal("expected an error for a pin above the ceiling")
	}
	if msg := errorText(res); !strings.Contains(msg, "youcom") || !strings.Contains(msg, "max_cost_usd") {
		t.Errorf("error = %q", msg)
	}
	if pinned.lastQuery.Text != "" {
		t.Error("pinned provider above the ceiling must never be called")
	}
}

func TestCeiling_PinnedProviderAtOrUnderCeilingRuns(t *testing.T) {
	pinned := &fakeSearcher{name: "serper", cost: 0.001, results: []search.Item{{Title: "x"}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{pinned}, nil)

	res := callTool(t, srv, "frugal__search", map[string]any{"query": "anything", "provider": "serper", "max_cost_usd": 0.001})
	if res.IsError {
		t.Fatalf("a pin priced exactly at the ceiling must run, got %s", errorText(res))
	}
}

func TestCeiling_NegativeIsRejected(t *testing.T) {
	s := &fakeSearcher{name: "free", results: []search.Item{{Title: "x"}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{s}, nil)

	res := callTool(t, srv, "frugal__search", map[string]any{"query": "anything", "max_cost_usd": -1})
	if !res.IsError {
		t.Fatal("expected an error for a negative ceiling")
	}
	if !strings.Contains(errorText(res), "zero or positive") {
		t.Errorf("error = %q", errorText(res))
	}
	if s.lastQuery.Text != "" {
		t.Error("invalid input must fail before any provider call")
	}
}

func TestCeiling_UnsetLeavesRoutingUnchanged(t *testing.T) {
	// No max_cost_usd: premium policy still picks the pricey provider,
	// exactly as before this feature existed.
	pricey := &fakeSearcher{name: "youcom", cost: 0.005, results: []search.Item{{Title: "PRICEY"}}}
	cheap := &fakeSearcher{name: "serper", cost: 0.001, results: []search.Item{{Title: "CHEAP"}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{cheap, pricey}, nil,
		WithPolicy(routing.Policy{Strategy: routing.StrategyPremium}))

	res := callTool(t, srv, "frugal__search", map[string]any{"query": "anything"})
	out, _ := decodeSearchOutput(res.StructuredContent)
	if out.ProviderUsed != "youcom" {
		t.Errorf("provider_used = %q, want youcom when no ceiling is declared", out.ProviderUsed)
	}
}

func TestCeiling_ExtractSkipsPaidRung(t *testing.T) {
	free := &fakeExtractor{name: "goreadability", cost: 0, res: extract.Result{Markdown: "free text", CostUSD: 0}}
	paid := &fakeExtractor{name: "firecrawl", cost: 0.001, res: extract.Result{Markdown: "paid text", CostUSD: 0.001}}
	srv := newServer()
	RegisterExtract(srv, []extract.Extractor{paid, free}, nil,
		WithPolicy(routing.Policy{Strategy: routing.StrategyPremium}))

	res := callTool(t, srv, "frugal__extract", map[string]any{"url": "https://example.com/a", "max_cost_usd": 0})
	if res.IsError {
		t.Fatalf("isError: %s", errorText(res))
	}
	out, err := decodeExtractOutput(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if out.ProviderUsed != "goreadability" {
		t.Errorf("provider_used = %q, want goreadability", out.ProviderUsed)
	}
	if paid.lastQuery.URL != "" {
		t.Error("paid extractor must not be called under a zero ceiling")
	}
}

func TestCeiling_BrowseAllAboveCeilingErrors(t *testing.T) {
	br := &fakeBrowser{name: "browserless", cost: 0.002, res: browse.Result{HTML: "<p>x</p>", CostUSD: 0.002}}
	srv := newServer()
	RegisterBrowse(srv, []browse.Browser{br}, nil)

	res := callTool(t, srv, "frugal__browse", map[string]any{"url": "https://example.com/a", "max_cost_usd": 0.001})
	if !res.IsError {
		t.Fatal("expected an error when the only browser is above the ceiling")
	}
	if msg := errorText(res); !strings.Contains(msg, "browserless") || !strings.Contains(msg, "$0.0020") {
		t.Errorf("error = %q", msg)
	}
	if br.lastQuery.URL != "" {
		t.Error("browser above the ceiling must never be called")
	}
}

func TestCeiling_ExecuteSearchTraceShowsSkip(t *testing.T) {
	pricey := &fakeSearcher{name: "youcom", cost: 0.005, results: []search.Item{{Title: "PRICEY"}}}
	cheap := &fakeSearcher{name: "serper", cost: 0.001, results: []search.Item{{Title: "CHEAP"}}}
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{cheap, pricey}, nil, nil, nil)

	res := callExecute(t, srv, map[string]any{"intent": "search for anything", "priority": "premium", "max_cost_usd": 0.001})
	if res.IsError {
		t.Fatalf("isError: %s", errorText(res))
	}
	out := decodeExecuteOutput(t, res.StructuredContent)
	if out.ProviderUsed != "serper" {
		t.Errorf("provider_used = %q, want serper", out.ProviderUsed)
	}
	if !strings.Contains(out.Reason, "ceiling: skipped youcom") {
		t.Errorf("reason = %q, want the ceiling skip in the trace", out.Reason)
	}
	if pricey.lastQuery.Text != "" {
		t.Error("provider above the ceiling must never be called")
	}
}

func TestCeiling_ExecuteFallForwardRespectsCeiling(t *testing.T) {
	// The extract comes back empty (JS page). Without a ceiling the call
	// would fall forward into a $0.002 render; with max_cost_usd 0 it
	// must stay on the free rung and say why the content is empty.
	ex := &fakeExtractor{name: "goreadability", cost: 0, res: extract.Result{CostUSD: 0}}
	br := &fakeBrowser{name: "browserless", cost: 0.002, res: browse.Result{Text: "rendered", CostUSD: 0.002}}
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{&fakeSearcher{name: "free"}}, []extract.Extractor{ex}, []browse.Browser{br}, nil)

	res := callExecute(t, srv, map[string]any{"intent": "read https://example.com/spa", "max_cost_usd": 0})
	if res.IsError {
		t.Fatalf("isError: %s", errorText(res))
	}
	out := decodeExecuteOutput(t, res.StructuredContent)
	if out.Capability != "extract" || out.ProviderUsed != "goreadability" {
		t.Errorf("got capability %q provider %q, want the empty extract returned", out.Capability, out.ProviderUsed)
	}
	if out.CostUSD != 0 {
		t.Errorf("cost = %v, want 0 (no paid render under a zero ceiling)", out.CostUSD)
	}
	if br.lastQuery.URL != "" {
		t.Error("browser above the ceiling must never be called on fall-forward")
	}
	joined := strings.Join(out.Warnings, " | ")
	if !strings.Contains(joined, "fall-forward skipped") || !strings.Contains(joined, "max_cost_usd") {
		t.Errorf("warnings = %q, want a fall-forward skip note naming the ceiling", joined)
	}
}

func TestCeiling_ExecuteFallForwardRunsWhenAffordable(t *testing.T) {
	ex := &fakeExtractor{name: "goreadability", cost: 0, res: extract.Result{CostUSD: 0}}
	br := &fakeBrowser{name: "browserless", cost: 0.002, res: browse.Result{Text: "rendered", CostUSD: 0.002}}
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{&fakeSearcher{name: "free"}}, []extract.Extractor{ex}, []browse.Browser{br}, nil)

	res := callExecute(t, srv, map[string]any{"intent": "read https://example.com/spa", "max_cost_usd": 0.002})
	if res.IsError {
		t.Fatalf("isError: %s", errorText(res))
	}
	out := decodeExecuteOutput(t, res.StructuredContent)
	if out.Capability != "browse" || out.Text != "rendered" {
		t.Errorf("got %+v, want the render under a ceiling that affords it", out)
	}
}

func TestCeiling_ExecutePinnedAboveCeilingErrors(t *testing.T) {
	pricey := &fakeSearcher{name: "youcom", cost: 0.005, results: []search.Item{{Title: "x"}}}
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{pricey}, nil, nil, nil)

	res := callExecute(t, srv, map[string]any{"intent": "search for anything", "provider": "youcom", "max_cost_usd": 0.001})
	if !res.IsError {
		t.Fatal("expected an error for a pin above the ceiling")
	}
	if pricey.lastQuery.Text != "" {
		t.Error("pinned provider above the ceiling must never be called")
	}
}
