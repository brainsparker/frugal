package tools

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/cache"
	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/search"
)

// countingSearcher counts real provider calls so tests can prove a
// cache hit skipped the provider.
type countingSearcher struct {
	name    string
	cost    float64
	results []search.Item
	calls   atomic.Int64
}

func (c *countingSearcher) Name() string         { return c.name }
func (c *countingSearcher) CostPerCall() float64 { return c.cost }
func (c *countingSearcher) Search(_ context.Context, _ search.Query) (search.Results, error) {
	c.calls.Add(1)
	return search.Results{Items: c.results, CostUSD: c.cost, Warnings: []string{"note"}}, nil
}

type countingExtractor struct {
	name  string
	cost  float64
	res   extract.Result
	calls atomic.Int64
}

func (c *countingExtractor) Name() string         { return c.name }
func (c *countingExtractor) CostPerCall() float64 { return c.cost }
func (c *countingExtractor) Extract(_ context.Context, _ extract.Query) (extract.Result, error) {
	c.calls.Add(1)
	r := c.res
	r.CostUSD = c.cost
	return r, nil
}

func decodeInto[T any](t *testing.T, raw any) T {
	t.Helper()
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	return out
}

func callToolOK(t *testing.T, client *sdkmcp.ClientSession, name string, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: name, Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s returned isError: %+v", name, res.Content)
	}
	return res
}

func TestSearchCache_SecondCallIsHit(t *testing.T) {
	s := &countingSearcher{
		name: "serper", cost: 0.001,
		results: []search.Item{{Title: "T", URL: "https://x.example/1", Snippet: "s"}},
	}
	rc := cache.New(8)
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{s}, nil, WithResultCache(rc, 5*time.Minute, 15*time.Minute))
	client, cleanup := dialClient(t, srv)
	defer cleanup()

	args := map[string]any{"query": "python docs"}
	first := decodeInto[SearchOutput](t, callToolOK(t, client, "frugal__search", args).StructuredContent)
	if first.Cached {
		t.Fatal("first call must not be cached")
	}
	if first.CostUSD != 0.001 {
		t.Fatalf("first cost = %v, want 0.001", first.CostUSD)
	}

	second := decodeInto[SearchOutput](t, callToolOK(t, client, "frugal__search", args).StructuredContent)
	if !second.Cached {
		t.Fatal("second identical call must be a cache hit")
	}
	if second.CostUSD != 0 {
		t.Fatalf("cached cost = %v, want 0", second.CostUSD)
	}
	if second.ProviderUsed != "serper" {
		t.Fatalf("cached provider_used = %q, want serper", second.ProviderUsed)
	}
	if len(second.Results) != 1 || second.Results[0].Title != "T" {
		t.Fatalf("cached results = %+v", second.Results)
	}
	if len(second.Warnings) != 1 {
		t.Fatalf("cached warnings = %+v, want original warnings", second.Warnings)
	}
	if got := s.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}
	if st := rc.Snapshot(); st.Hits != 1 {
		t.Fatalf("cache hits = %d, want 1", st.Hits)
	}
}

func TestSearchCache_DifferentArgsMiss(t *testing.T) {
	s := &countingSearcher{name: "serper", cost: 0.001, results: []search.Item{{Title: "T"}}}
	rc := cache.New(8)
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{s}, nil, WithResultCache(rc, 5*time.Minute, 15*time.Minute))
	client, cleanup := dialClient(t, srv)
	defer cleanup()

	callToolOK(t, client, "frugal__search", map[string]any{"query": "python docs"})
	callToolOK(t, client, "frugal__search", map[string]any{"query": "python docs", "freshness": "day"})
	callToolOK(t, client, "frugal__search", map[string]any{"query": "python docs", "max_results": 10})
	if got := s.calls.Load(); got != 3 {
		t.Fatalf("provider calls = %d, want 3 (distinct keys)", got)
	}
}

func TestSearchCache_DisabledKeepsCallingProvider(t *testing.T) {
	s := &countingSearcher{name: "serper", cost: 0.001, results: []search.Item{{Title: "T"}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{s}, nil) // no cache option
	client, cleanup := dialClient(t, srv)
	defer cleanup()

	args := map[string]any{"query": "python docs"}
	callToolOK(t, client, "frugal__search", args)
	out := decodeInto[SearchOutput](t, callToolOK(t, client, "frugal__search", args).StructuredContent)
	if out.Cached {
		t.Fatal("cache disabled: nothing may report cached")
	}
	if got := s.calls.Load(); got != 2 {
		t.Fatalf("provider calls = %d, want 2", got)
	}
}

func TestExtractCache_SecondCallIsHitWithFullFields(t *testing.T) {
	e := &countingExtractor{
		name: "goreadability", cost: 0.002,
		res: extract.Result{
			Markdown: "# Hi", Title: "Hi", Byline: "Ann Author",
			Links: []string{"https://x.example/next"},
		},
	}
	rc := cache.New(8)
	srv := newServer()
	RegisterExtract(srv, []extract.Extractor{e}, nil, WithResultCache(rc, 5*time.Minute, 15*time.Minute))
	client, cleanup := dialClient(t, srv)
	defer cleanup()

	args := map[string]any{"url": "https://x.example/a"}
	callToolOK(t, client, "frugal__extract", args)
	out := decodeInto[ExtractOutput](t, callToolOK(t, client, "frugal__extract", args).StructuredContent)
	if !out.Cached {
		t.Fatal("second identical extract must be a cache hit")
	}
	if out.CostUSD != 0 {
		t.Fatalf("cached cost = %v, want 0", out.CostUSD)
	}
	if out.Byline != "Ann Author" || len(out.Links) != 1 {
		t.Fatalf("cached extract lost fields: byline=%q links=%v", out.Byline, out.Links)
	}
	if got := e.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}
}

func TestExecuteSharesCacheWithSearch(t *testing.T) {
	s := &countingSearcher{name: "serper", cost: 0.001, results: []search.Item{{Title: "T"}}}
	rc := cache.New(8)
	srv := newServer()
	opt := WithResultCache(rc, 5*time.Minute, 15*time.Minute)
	RegisterSearch(srv, []search.Searcher{s}, nil, opt)
	RegisterExecute(srv, []search.Searcher{s}, nil, nil, nil, opt)
	client, cleanup := dialClient(t, srv)
	defer cleanup()

	// Warm through the direct tool, hit through execute.
	callToolOK(t, client, "frugal__search", map[string]any{"query": "python docs"})
	out := decodeInto[ExecuteOutput](t, callToolOK(t, client, "frugal__execute",
		map[string]any{"intent": "search python docs"}).StructuredContent)
	if out.Capability != "search" {
		t.Fatalf("capability = %q, want search", out.Capability)
	}
	if !out.Cached {
		t.Fatalf("execute must hit the entry frugal__search stored (reason: %s)", out.Reason)
	}
	if out.CostUSD != 0 {
		t.Fatalf("cached cost = %v, want 0", out.CostUSD)
	}
	if got := s.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}

	// And the reverse: an execute miss stores an entry the direct tool hits.
	out2 := decodeInto[ExecuteOutput](t, callToolOK(t, client, "frugal__execute",
		map[string]any{"intent": "search go generics"}).StructuredContent)
	if out2.Cached {
		t.Fatal("new query through execute must miss")
	}
	hit := decodeInto[SearchOutput](t, callToolOK(t, client, "frugal__search",
		map[string]any{"query": "go generics"}).StructuredContent)
	if !hit.Cached {
		t.Fatal("frugal__search must hit the entry execute stored")
	}
	if got := s.calls.Load(); got != 2 {
		t.Fatalf("provider calls = %d, want 2", got)
	}
}

func TestExecutePriorityBypassesCache(t *testing.T) {
	s := &countingSearcher{name: "serper", cost: 0.001, results: []search.Item{{Title: "T"}}}
	rc := cache.New(8)
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{s}, nil, nil, nil, WithResultCache(rc, 5*time.Minute, 15*time.Minute))
	client, cleanup := dialClient(t, srv)
	defer cleanup()

	callToolOK(t, client, "frugal__execute", map[string]any{"intent": "search python docs"})
	out := decodeInto[ExecuteOutput](t, callToolOK(t, client, "frugal__execute",
		map[string]any{"intent": "search python docs", "priority": "premium"}).StructuredContent)
	if out.Cached {
		t.Fatal("explicit premium priority must bypass the cache")
	}
	if got := s.calls.Load(); got != 2 {
		t.Fatalf("provider calls = %d, want 2", got)
	}
}
