package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/browse"
	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// loopCountingSearcher is a fakeSearcher that also counts how many times a
// provider was actually reached, which is what the loop guard protects.
type loopCountingSearcher struct {
	fakeSearcher
	calls int
}

func (c *loopCountingSearcher) Search(ctx context.Context, q search.Query) (search.Results, error) {
	c.calls++
	return c.fakeSearcher.Search(ctx, q)
}

func loopGuard(limit int) *routing.Guard {
	g := routing.NewGuard(nil, time.Minute)
	g.SetLoopGuard(limit, time.Minute)
	return g
}

func callSearch(t *testing.T, client *sdkmcp.ClientSession, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name:      "frugal__search",
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	return res
}

func TestCallTool_LoopGuardRefusesRepeatedIdenticalSearch(t *testing.T) {
	// Two identical calls are fine at limit 2; the third is refused
	// before the provider is reached, with a reason the agent can act on.
	free := &loopCountingSearcher{fakeSearcher: fakeSearcher{name: "marginalia", cost: 0, results: []search.Item{{Title: "hit"}}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{free}, nil, WithGuard(loopGuard(2)))

	client, cleanup := dialClient(t, srv)
	defer cleanup()

	for i := 1; i <= 2; i++ {
		if res := callSearch(t, client, map[string]any{"query": "go routing"}); res.IsError {
			t.Fatalf("call %d within the limit errored: %s", i, errorText(res))
		}
	}
	res := callSearch(t, client, map[string]any{"query": "go routing"})
	if !res.IsError {
		t.Fatal("third identical call must be refused by the loop guard")
	}
	text := errorText(res)
	for _, want := range []string{"frugal__search", "loop guard", "3 times", "limit 2"} {
		if !strings.Contains(text, want) {
			t.Errorf("error text %q missing %q", text, want)
		}
	}
	if free.calls != 2 {
		t.Errorf("provider reached %d times, want 2 (the refused call must not spend)", free.calls)
	}
}

func TestCallTool_LoopGuardNormalizesArguments(t *testing.T) {
	// Whitespace around the query and provider spelling must not split
	// one loop into several fingerprints, otherwise a loop that jitters
	// its formatting would slip past the rail.
	free := &loopCountingSearcher{fakeSearcher: fakeSearcher{name: "marginalia", cost: 0, results: []search.Item{{Title: "hit"}}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{free}, nil, WithGuard(loopGuard(2)))

	client, cleanup := dialClient(t, srv)
	defer cleanup()

	callSearch(t, client, map[string]any{"query": "go routing", "provider": "marginalia"})
	callSearch(t, client, map[string]any{"query": " go routing ", "provider": "Marginalia"})
	res := callSearch(t, client, map[string]any{"query": "go routing  ", "provider": "MARGINALIA"})
	if !res.IsError {
		t.Fatal("formatting jitter must still count as the same call")
	}
	if free.calls != 2 {
		t.Errorf("provider reached %d times, want 2", free.calls)
	}
}

func TestCallTool_LoopGuardLetsDifferentArgumentsThrough(t *testing.T) {
	free := &loopCountingSearcher{fakeSearcher: fakeSearcher{name: "marginalia", cost: 0, results: []search.Item{{Title: "hit"}}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{free}, nil, WithGuard(loopGuard(1)))

	client, cleanup := dialClient(t, srv)
	defer cleanup()

	// Limit 1: a repeat is refused, but each new query is its own
	// fingerprint and sails through. max_results is part of the key too.
	for i, args := range []map[string]any{
		{"query": "alpha"},
		{"query": "beta"},
		{"query": "alpha", "max_results": 3},
		{"query": "alpha", "freshness": "week"},
	} {
		if res := callSearch(t, client, args); res.IsError {
			t.Fatalf("distinct call %d refused: %s", i, errorText(res))
		}
	}
	if res := callSearch(t, client, map[string]any{"query": "alpha"}); !res.IsError {
		t.Fatal("the repeat of the first call must be refused")
	}
	if free.calls != 4 {
		t.Errorf("provider reached %d times, want 4", free.calls)
	}
}

func TestCallTool_NoGuardMeansNoLoopRail(t *testing.T) {
	// Registering without WithGuard keeps the historical behavior: no
	// rail, every identical call reaches the provider.
	free := &loopCountingSearcher{fakeSearcher: fakeSearcher{name: "marginalia", cost: 0, results: []search.Item{{Title: "hit"}}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{free}, nil)

	client, cleanup := dialClient(t, srv)
	defer cleanup()

	for i := 0; i < 8; i++ {
		if res := callSearch(t, client, map[string]any{"query": "same"}); res.IsError {
			t.Fatalf("call %d errored without a guard: %s", i, errorText(res))
		}
	}
	if free.calls != 8 {
		t.Errorf("provider reached %d times, want 8", free.calls)
	}
}

func TestExecute_LoopGuardRefusesRepeatedIntent(t *testing.T) {
	free := &loopCountingSearcher{fakeSearcher: fakeSearcher{name: "free", cost: 0, results: []search.Item{{Title: "FREE"}}}}
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{free}, nil, nil, nil, WithGuard(loopGuard(1)))

	if res := callExecute(t, srv, map[string]any{"intent": "search for MCP registries"}); res.IsError {
		t.Fatalf("first execute errored: %s", errorText(res))
	}
	res := callExecute(t, srv, map[string]any{"intent": "search for MCP registries"})
	if !res.IsError {
		t.Fatal("repeated identical intent must be refused")
	}
	if text := errorText(res); !strings.Contains(text, "frugal__execute") || !strings.Contains(text, "loop guard") {
		t.Errorf("error text = %q", text)
	}
	// A different priority is a different decision, so it is admitted.
	if res := callExecute(t, srv, map[string]any{"intent": "search for MCP registries", "priority": "premium"}); res.IsError {
		t.Fatalf("different priority refused: %s", errorText(res))
	}
	if free.calls != 2 {
		t.Errorf("provider reached %d times, want 2", free.calls)
	}
}

func TestCallTool_LoopGuardOnExtractAndBrowse(t *testing.T) {
	ex := &fakeExtractor{name: "goreadability", cost: 0, res: extract.Result{Markdown: "# page"}}
	br := &fakeBrowser{name: "browserless", cost: 0.002, res: browse.Result{HTML: "<html/>"}}
	guard := loopGuard(1)
	srv := newServer()
	RegisterExtract(srv, []extract.Extractor{ex}, nil, WithGuard(guard))
	RegisterBrowse(srv, []browse.Browser{br}, nil, WithGuard(guard))

	client, cleanup := dialClient(t, srv)
	defer cleanup()

	call := func(tool string, args map[string]any) *sdkmcp.CallToolResult {
		res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("CallTool %s: %v", tool, err)
		}
		return res
	}
	if res := call("frugal__extract", map[string]any{"url": "https://example.com/a"}); res.IsError {
		t.Fatalf("first extract errored: %s", errorText(res))
	}
	if res := call("frugal__extract", map[string]any{"url": "https://example.com/a"}); !res.IsError {
		t.Fatal("repeated extract must be refused")
	}
	// The browse tool has its own fingerprint space even for the same URL.
	if res := call("frugal__browse", map[string]any{"url": "https://example.com/a"}); res.IsError {
		t.Fatalf("first browse errored: %s", errorText(res))
	}
	if res := call("frugal__browse", map[string]any{"url": "https://example.com/a"}); !res.IsError {
		t.Fatal("repeated browse must be refused")
	}
	if text := errorText(call("frugal__browse", map[string]any{"url": "https://example.com/a"})); !strings.Contains(text, "frugal__browse") {
		t.Errorf("browse refusal must name the tool, got %q", text)
	}
}
