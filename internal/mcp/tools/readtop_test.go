package tools

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/cache"
	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// urlExtractor returns a different result per URL, so a read of several
// pages can assert each page got its own content, and records every URL
// it was asked for.
type urlExtractor struct {
	name  string
	cost  float64
	pages map[string]extract.Result
	errs  map[string]error
	delay time.Duration
	calls atomic.Int64
	seen  []string
	mu    chan struct{}
}

func newURLExtractor(name string, cost float64) *urlExtractor {
	return &urlExtractor{name: name, cost: cost, pages: map[string]extract.Result{}, errs: map[string]error{}, mu: make(chan struct{}, 1)}
}

func (u *urlExtractor) Name() string         { return u.name }
func (u *urlExtractor) CostPerCall() float64 { return u.cost }
func (u *urlExtractor) Extract(ctx context.Context, q extract.Query) (extract.Result, error) {
	u.calls.Add(1)
	u.mu <- struct{}{}
	u.seen = append(u.seen, q.URL)
	<-u.mu
	if u.delay > 0 {
		select {
		case <-time.After(u.delay):
		case <-ctx.Done():
			return extract.Result{}, routing.Transient(u.name, 0, ctx.Err())
		}
	}
	if err, ok := u.errs[q.URL]; ok {
		return extract.Result{}, err
	}
	res, ok := u.pages[q.URL]
	if !ok {
		return extract.Result{}, routing.Permanent(u.name, 0, errors.New("no content"))
	}
	res.CostUSD = u.cost
	return res, nil
}

func threeHits() []search.Item {
	return []search.Item{
		{Title: "One", URL: "https://a.example/1", Snippet: "first", PublishedAt: "2026-10-20"},
		{Title: "Two", URL: "https://b.example/2", Snippet: "second"},
		{Title: "Three", URL: "https://c.example/3", Snippet: "third", PublishedAt: "not a date"},
	}
}

func readSearch(t *testing.T, client *sdkmcp.ClientSession, args map[string]any) (SearchOutput, error) {
	t.Helper()
	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "frugal__search", Arguments: args})
	if err != nil {
		return SearchOutput{}, err
	}
	if res.IsError {
		msg := ""
		for _, c := range res.Content {
			if tc, ok := c.(*sdkmcp.TextContent); ok {
				msg += tc.Text
			}
		}
		return SearchOutput{}, errors.New(msg)
	}
	out, err := decodeSearchOutput(res.StructuredContent)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out, nil
}

func TestReadTop_ReadsTopPagesWithReceipts(t *testing.T) {
	s := &fakeSearcher{name: "free", cost: 0, results: threeHits()}
	ex := newURLExtractor("goreadability", 0)
	ex.pages["https://a.example/1"] = extract.Result{Markdown: "# One\n\nbody one", Title: "One (page)"}
	ex.pages["https://b.example/2"] = extract.Result{Markdown: "body two"}
	ex.pages["https://c.example/3"] = extract.Result{Markdown: "body three"}

	srv := newServer()
	RegisterSearch(srv, []search.Searcher{s}, nil, WithPageReaders([]extract.Extractor{ex}, routing.Policy{}, nil))
	client, cleanup := dialClient(t, srv)
	defer cleanup()

	out, err := readSearch(t, client, map[string]any{"query": "q", "read_top": 2})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(out.Results) != 3 {
		t.Fatalf("results: got %d want 3 (read_top must not shorten the hit list)", len(out.Results))
	}
	if len(out.Pages) != 2 {
		t.Fatalf("pages: got %d want 2", len(out.Pages))
	}
	if out.PagesRead != 2 || out.PagesFailed != 0 {
		t.Errorf("pages_read/failed: got %d/%d want 2/0", out.PagesRead, out.PagesFailed)
	}
	if ex.calls.Load() != 2 {
		t.Errorf("extractor calls: got %d want 2 (only the top 2 are read)", ex.calls.Load())
	}
	p0, p1 := out.Pages[0], out.Pages[1]
	if p0.URL != "https://a.example/1" || p1.URL != "https://b.example/2" {
		t.Errorf("page order should follow result order: %q, %q", p0.URL, p1.URL)
	}
	if p0.FetchStatus != "ok" || p0.Content != "# One\n\nbody one" || p0.ContentFormat != "markdown" || p0.ProviderUsed != "goreadability" {
		t.Errorf("page 0: %+v", p0)
	}
	if p0.Title != "One" {
		t.Errorf("page title should keep the search hit's title when present, got %q", p0.Title)
	}
	if p0.CharsReturned != len("# One\n\nbody one") || p0.CharsTotal != p0.CharsReturned || p0.Truncated {
		t.Errorf("page 0 size footer: %+v", p0)
	}
	// Dates: page 0 has a parseable published_at, page 1 has none.
	if p0.AgeDays == nil || p0.Undated {
		t.Errorf("page 0 should carry age_days from published_at, got %+v", p0)
	}
	if p1.AgeDays != nil || !p1.Undated {
		t.Errorf("page 1 should be undated, got %+v", p1)
	}
	// est_tokens includes the page content, not just the snippets.
	snippetsOnly, err := readSearch(t, client, map[string]any{"query": "q"})
	if err != nil {
		t.Fatalf("snippets-only call: %v", err)
	}
	if out.EstTokens <= snippetsOnly.EstTokens {
		t.Errorf("est_tokens with pages (%d) should exceed snippets only (%d)", out.EstTokens, snippetsOnly.EstTokens)
	}
	if len(snippetsOnly.Pages) != 0 || snippetsOnly.PagesRead != 0 {
		t.Errorf("read_top unset must return no pages, got %+v", snippetsOnly.Pages)
	}
	// attempts[] names the capability on every entry when pages are read.
	if len(out.Attempts) != 3 {
		t.Fatalf("attempts: got %d want 3 (1 search + 2 extract): %+v", len(out.Attempts), out.Attempts)
	}
	var searchAttempts, extractAttempts int
	for _, a := range out.Attempts {
		switch a.Capability {
		case "search":
			searchAttempts++
		case "extract":
			extractAttempts++
		default:
			t.Errorf("attempt without capability when read_top is set: %+v", a)
		}
	}
	if searchAttempts != 1 || extractAttempts != 2 {
		t.Errorf("attempt capabilities: search=%d extract=%d", searchAttempts, extractAttempts)
	}
	// Snippets-only calls keep the historical unlabeled attempt.
	if len(snippetsOnly.Attempts) != 1 || snippetsOnly.Attempts[0].Capability != "" {
		t.Errorf("snippets-only attempts should stay unlabeled: %+v", snippetsOnly.Attempts)
	}
}

func TestReadTop_DeadLinkIsReportedNotDropped(t *testing.T) {
	s := &fakeSearcher{name: "free", cost: 0, results: threeHits()}
	ex := newURLExtractor("goreadability", 0)
	ex.pages["https://a.example/1"] = extract.Result{Markdown: "alive"}
	ex.errs["https://b.example/2"] = routing.Fatal("goreadability", 404, errors.New("target returned 404"))
	paid := newURLExtractor("firecrawl", 0.001)
	paid.pages["https://b.example/2"] = extract.Result{Markdown: "should not be reached: fatal stops the chain"}

	srv := newServer()
	RegisterSearch(srv, []search.Searcher{s}, nil, WithPageReaders([]extract.Extractor{ex, paid}, routing.Policy{}, nil))
	client, cleanup := dialClient(t, srv)
	defer cleanup()

	out, err := readSearch(t, client, map[string]any{"query": "q", "read_top": 2})
	if err != nil {
		t.Fatalf("CallTool: %v (a dead page must not fail the call)", err)
	}
	if len(out.Pages) != 2 {
		t.Fatalf("pages: got %d want 2", len(out.Pages))
	}
	dead := out.Pages[1]
	if dead.FetchStatus != "error" || dead.ErrorKind != "fatal" || !strings.Contains(dead.Error, "404") {
		t.Errorf("dead page receipt: %+v", dead)
	}
	if dead.Content != "" || dead.ProviderUsed != "" {
		t.Errorf("dead page must carry no content or provider: %+v", dead)
	}
	if out.PagesRead != 1 || out.PagesFailed != 1 {
		t.Errorf("pages_read/failed: got %d/%d want 1/1", out.PagesRead, out.PagesFailed)
	}
	if paid.calls.Load() != 0 {
		t.Errorf("a fatal error must stop the chain before the paid rung; firecrawl was called %d times", paid.calls.Load())
	}
}

func TestReadTop_PageMaxCharsCapsEachPage(t *testing.T) {
	s := &fakeSearcher{name: "free", cost: 0, results: threeHits()}
	ex := newURLExtractor("goreadability", 0)
	long := strings.Repeat("word ", 2000) // 10,000 chars
	ex.pages["https://a.example/1"] = extract.Result{Markdown: long}
	ex.pages["https://b.example/2"] = extract.Result{Markdown: "short"}

	srv := newServer()
	RegisterSearch(srv, []search.Searcher{s}, nil, WithPageReaders([]extract.Extractor{ex}, routing.Policy{}, nil))
	client, cleanup := dialClient(t, srv)
	defer cleanup()

	// Default cap applies when neither caller nor operator set one.
	out, err := readSearch(t, client, map[string]any{"query": "q", "read_top": 2})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	p := out.Pages[0]
	if !p.Truncated || p.CharsTotal != 10000 || p.CharsReturned > DefaultPageMaxChars || p.CharsReturned == 0 {
		t.Errorf("default cap: %+v", Page{Truncated: p.Truncated, CharsTotal: p.CharsTotal, CharsReturned: p.CharsReturned})
	}
	if out.Pages[1].Truncated || out.Pages[1].Content != "short" {
		t.Errorf("short page must be untouched: %+v", out.Pages[1])
	}

	// Caller's page_max_chars wins.
	out, err = readSearch(t, client, map[string]any{"query": "q", "read_top": 1, "page_max_chars": 100})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if p := out.Pages[0]; !p.Truncated || p.CharsReturned > 100 {
		t.Errorf("page_max_chars=100: returned %d truncated=%v", p.CharsReturned, p.Truncated)
	}

	// Operator limits.max_chars is the fallback before the default.
	srv2 := newServer()
	RegisterSearch(srv2, []search.Searcher{s}, nil, WithMaxChars(250), WithPageReaders([]extract.Extractor{ex}, routing.Policy{}, nil))
	client2, cleanup2 := dialClient(t, srv2)
	defer cleanup2()
	out, err = readSearch(t, client2, map[string]any{"query": "q", "read_top": 1})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if p := out.Pages[0]; !p.Truncated || p.CharsReturned > 250 || p.CharsReturned <= 100 {
		t.Errorf("operator max_chars=250: returned %d truncated=%v", p.CharsReturned, p.Truncated)
	}
}

func TestReadTop_CeilingCoversSearchAndPages(t *testing.T) {
	// Paid search ($0.001) then pages. With max_cost_usd 0.0015 and two
	// pages, each page has $0.00025 left: the free rung fits, the paid
	// extract rung ($0.001) does not, even when the free rung fails.
	s := &fakeSearcher{name: "serper", cost: 0.001, results: threeHits()}
	free := newURLExtractor("goreadability", 0)
	free.pages["https://a.example/1"] = extract.Result{Markdown: "free read"}
	// b fails on the free rung (permanent, so the chain would fall through).
	free.errs["https://b.example/2"] = routing.Permanent("goreadability", 0, errors.New("empty"))
	paid := newURLExtractor("firecrawl", 0.001)
	paid.pages["https://b.example/2"] = extract.Result{Markdown: "paid read"}

	srv := newServer()
	RegisterSearch(srv, []search.Searcher{s}, nil, WithPageReaders([]extract.Extractor{free, paid}, routing.Policy{}, nil))
	client, cleanup := dialClient(t, srv)
	defer cleanup()

	out, err := readSearch(t, client, map[string]any{"query": "q", "read_top": 2, "max_cost_usd": 0.0015})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if out.CostUSD != 0.001 {
		t.Errorf("cost_usd should stay the search price, got %g", out.CostUSD)
	}
	if paid.calls.Load() != 0 {
		t.Errorf("paid extract rung must be fenced off by the remaining ceiling; called %d times", paid.calls.Load())
	}
	if out.Pages[0].FetchStatus != "ok" || out.Pages[0].Content != "free read" {
		t.Errorf("page 0: %+v", out.Pages[0])
	}
	if p := out.Pages[1]; p.FetchStatus != "error" || !strings.Contains(p.Error, "goreadability") {
		t.Errorf("page 1 should fail on the free rung without reaching paid: %+v", p)
	}
	if out.TotalCostUSD > 0.0015 {
		t.Errorf("total_cost_usd %g exceeds the caller's ceiling", out.TotalCostUSD)
	}

	// With room under the ceiling, the paid rung is reachable and its
	// cost shows on the page and in total_cost_usd.
	out, err = readSearch(t, client, map[string]any{"query": "q2", "read_top": 2, "max_cost_usd": 0.01})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if p := out.Pages[1]; p.FetchStatus != "ok" || p.ProviderUsed != "firecrawl" || p.CostUSD != 0.001 {
		t.Errorf("page 1 should fall through to firecrawl under a wide ceiling: %+v", p)
	}
	if out.TotalCostUSD != 0.002 {
		t.Errorf("total_cost_usd: got %g want 0.002 (search 0.001 + firecrawl 0.001)", out.TotalCostUSD)
	}
	if out.CostUSD != 0.001 {
		t.Errorf("cost_usd stays the search price: got %g", out.CostUSD)
	}
}

func TestReadTop_SharesExtractCache(t *testing.T) {
	s := &fakeSearcher{name: "free", cost: 0, results: threeHits()}
	ex := newURLExtractor("goreadability", 0)
	ex.pages["https://a.example/1"] = extract.Result{Markdown: "cached body", Title: "From page"}
	c := cache.New(64)

	srv := newServer()
	RegisterSearch(srv, []search.Searcher{s}, nil, WithResultCache(c, time.Minute, time.Minute), WithPageReaders([]extract.Extractor{ex}, routing.Policy{}, nil))
	RegisterExtract(srv, []extract.Extractor{ex}, nil, WithResultCache(c, time.Minute, time.Minute))
	client, cleanup := dialClient(t, srv)
	defer cleanup()

	// First read pays the extract.
	out, err := readSearch(t, client, map[string]any{"query": "q", "read_top": 1})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if out.Pages[0].Cached {
		t.Fatalf("first read must not be a cache hit")
	}
	// Second call: search is a cache hit, and the page is too.
	out, err = readSearch(t, client, map[string]any{"query": "q", "read_top": 1})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !out.Cached {
		t.Errorf("search should be served from cache")
	}
	if p := out.Pages[0]; !p.Cached || p.Content != "cached body" || p.FetchStatus != "ok" || p.CostUSD != 0 {
		t.Errorf("page should be served from the extract cache: %+v", p)
	}
	if ex.calls.Load() != 1 {
		t.Errorf("extractor should have been called once, got %d", ex.calls.Load())
	}
	// And frugal__extract on the same URL is a hit from the read_top entry.
	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "frugal__extract", Arguments: map[string]any{"url": "https://a.example/1"}})
	if err != nil || res.IsError {
		t.Fatalf("frugal__extract: %v %v", err, res)
	}
	eo, err := decodeExtractOutput(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if !eo.Cached || eo.Markdown != "cached body" {
		t.Errorf("frugal__extract should hit the entry read_top stored: %+v", eo)
	}
	if ex.calls.Load() != 1 {
		t.Errorf("extractor still called once after frugal__extract, got %d", ex.calls.Load())
	}
}

func TestReadTop_DeadlineReportsUnreadPages(t *testing.T) {
	// Five hits, a slow extractor, and a call deadline shorter than two
	// rounds of reads: the pages that never started still appear, as
	// errors naming the deadline, and the call itself succeeds.
	items := make([]search.Item, 0, 5)
	for _, u := range []string{"https://a.example/1", "https://b.example/2", "https://c.example/3", "https://d.example/4", "https://e.example/5"} {
		items = append(items, search.Item{Title: u, URL: u})
	}
	s := &fakeSearcher{name: "free", cost: 0, results: items}
	ex := newURLExtractor("slow", 0)
	ex.delay = 300 * time.Millisecond
	for _, it := range items {
		ex.pages[it.URL] = extract.Result{Markdown: "late"}
	}

	srv := newServer()
	RegisterSearch(srv, []search.Searcher{s}, nil, WithCallTimeout(150*time.Millisecond), WithPageReaders([]extract.Extractor{ex}, routing.Policy{}, nil))
	client, cleanup := dialClient(t, srv)
	defer cleanup()

	out, err := readSearch(t, client, map[string]any{"query": "q", "read_top": 5})
	if err != nil {
		t.Fatalf("CallTool: %v (the deadline must not fail a search that already has hits)", err)
	}
	if len(out.Pages) != 5 {
		t.Fatalf("pages: got %d want 5 (every attempted page is listed)", len(out.Pages))
	}
	if out.PagesRead != 0 || out.PagesFailed != 5 {
		t.Errorf("pages_read/failed: got %d/%d want 0/5", out.PagesRead, out.PagesFailed)
	}
	for _, p := range out.Pages {
		if p.FetchStatus != "error" || !strings.Contains(p.Error, "deadline") {
			t.Errorf("page should name the deadline: %+v", p)
		}
	}
	if len(out.Results) != 5 {
		t.Errorf("results must survive the deadline: %d", len(out.Results))
	}
}

func TestReadTop_Validation(t *testing.T) {
	s := &fakeSearcher{name: "free", cost: 0, results: threeHits()}
	ex := newURLExtractor("goreadability", 0)
	for _, it := range threeHits() {
		ex.pages[it.URL] = extract.Result{Markdown: "x"}
	}

	t.Run("no readers configured", func(t *testing.T) {
		srv := newServer()
		RegisterSearch(srv, []search.Searcher{s}, nil)
		client, cleanup := dialClient(t, srv)
		defer cleanup()
		if _, err := readSearch(t, client, map[string]any{"query": "q", "read_top": 1}); err == nil || !strings.Contains(err.Error(), "extract provider") {
			t.Errorf("read_top without extractors should error naming the gap, got %v", err)
		}
		// And the plain call still works, byte-for-byte the old shape.
		out, err := readSearch(t, client, map[string]any{"query": "q"})
		if err != nil || len(out.Results) != 3 || out.Pages != nil {
			t.Errorf("plain search: %v %+v", err, out)
		}
		if ex.calls.Load() != 0 {
			t.Errorf("no extractor call expected, got %d", ex.calls.Load())
		}
	})

	srv := newServer()
	RegisterSearch(srv, []search.Searcher{s}, nil, WithPageReaders([]extract.Extractor{ex}, routing.Policy{}, nil))
	client, cleanup := dialClient(t, srv)
	defer cleanup()

	t.Run("negative read_top", func(t *testing.T) {
		if _, err := readSearch(t, client, map[string]any{"query": "q", "read_top": -1}); err == nil || !strings.Contains(err.Error(), "read_top") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("negative page_max_chars", func(t *testing.T) {
		if _, err := readSearch(t, client, map[string]any{"query": "q", "read_top": 1, "page_max_chars": -5}); err == nil || !strings.Contains(err.Error(), "page_max_chars") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("read_top clamps to MaxReadTop and to the hit count", func(t *testing.T) {
		out, err := readSearch(t, client, map[string]any{"query": "q", "read_top": 50})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if len(out.Pages) != 3 {
			t.Errorf("3 hits, read_top 50: pages got %d want 3", len(out.Pages))
		}
	})
	t.Run("tool description advertises read_top", func(t *testing.T) {
		res, err := client.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.Tools[0].Description, "read_top") || !strings.Contains(res.Tools[0].Description, "goreadability") {
			t.Errorf("description should mention read_top and the extract chain: %q", res.Tools[0].Description)
		}
		schema, _ := res.Tools[0].InputSchema.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["read_top"]; !ok {
			t.Errorf("input schema missing read_top: %v", props)
		}
		if _, ok := props["page_max_chars"]; !ok {
			t.Errorf("input schema missing page_max_chars: %v", props)
		}
	})
}

func TestReadTop_DomainPolicySkipsRefusedPagesBeforeFetch(t *testing.T) {
	// The hit list is filtered first, so a denied host never appears
	// among the pages either; nothing is fetched for it.
	s := &fakeSearcher{name: "free", cost: 0, results: threeHits()}
	ex := newURLExtractor("goreadability", 0)
	for _, it := range threeHits() {
		ex.pages[it.URL] = extract.Result{Markdown: "x"}
	}
	pol, err := routing.NewDomainPolicy(nil, []string{"a.example"}, false)
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{s}, nil, WithDomainPolicy(pol), WithPageReaders([]extract.Extractor{ex}, routing.Policy{}, nil))
	client, cleanup := dialClient(t, srv)
	defer cleanup()

	out, err := readSearch(t, client, map[string]any{"query": "q", "read_top": 5})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(out.Results) != 2 || len(out.Pages) != 2 {
		t.Fatalf("results/pages: %d/%d want 2/2", len(out.Results), len(out.Pages))
	}
	for _, u := range ex.seen {
		if strings.Contains(u, "a.example") {
			t.Errorf("denied host was fetched: %s", u)
		}
	}
}

func TestSetPageAge(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in      string
		want    int
		undated bool
	}{
		{"2026-10-20", 4, false},
		{"2026-10-24T09:00:00Z", 0, false},
		{"2026-09-24T09:00:00.123Z", 30, false},
		{"2027-01-01", 0, false}, // future reads as today, never negative
		{"", 0, true},
		{"yesterday", 0, true},
	}
	for _, c := range cases {
		var p Page
		setPageAge(&p, c.in, now)
		if c.undated {
			if !p.Undated || p.AgeDays != nil {
				t.Errorf("%q: want undated, got %+v", c.in, p)
			}
			continue
		}
		if p.Undated || p.AgeDays == nil || *p.AgeDays != c.want {
			t.Errorf("%q: want age %d, got %+v", c.in, c.want, p)
		}
	}
}

func TestPageCeiling(t *testing.T) {
	if c := pageCeiling(routing.NoCeiling, 0.5, 3); c.Set {
		t.Errorf("unset ceiling must stay unset")
	}
	c := pageCeiling(routing.Ceiling{MaxUSD: 0.01, Set: true}, 0.004, 3)
	if !c.Set || c.MaxUSD < 0.0019 || c.MaxUSD > 0.0021 {
		t.Errorf("remaining 0.006 over 3 pages: got %+v", c)
	}
	c = pageCeiling(routing.Ceiling{MaxUSD: 0.001, Set: true}, 0.005, 2)
	if !c.Set || c.MaxUSD != 0 {
		t.Errorf("overspent search leaves free rungs only: got %+v", c)
	}
}

func TestPageContent_PrefersTextWhenMarkdownIsHTML(t *testing.T) {
	cases := []struct {
		name       string
		res        extract.Result
		want, form string
	}{
		{"real markdown wins", extract.Result{Markdown: "# Title\n\nbody", Text: "Title body"}, "# Title\n\nbody", "markdown"},
		{"html-as-markdown falls back to text", extract.Result{Markdown: "<div><p>body</p></div>", HTML: "<div><p>body</p></div>", Text: "body"}, "body", "text"},
		{"html-as-markdown with no text stays", extract.Result{Markdown: "<p>body</p>"}, "<p>body</p>", "markdown"},
		{"text only", extract.Result{Text: "plain"}, "plain", "text"},
		{"html only", extract.Result{HTML: "<p>x</p>"}, "<p>x</p>", "html"},
		{"empty", extract.Result{}, "", ""},
	}
	for _, c := range cases {
		got, form := pageContent(c.res)
		if got != c.want || form != c.form {
			t.Errorf("%s: got (%q, %q) want (%q, %q)", c.name, got, form, c.want, c.form)
		}
	}
}

func TestSqueezeWhitespace(t *testing.T) {
	in := "  Pinned      \t Loading\n\n\n\n  Spec   for MCP\n\nTypeScript\u00a0\u00a09.5k  "
	want := "Pinned Loading\n\nSpec for MCP\n\nTypeScript 9.5k"
	if got := squeezeWhitespace(in); got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if got := squeezeWhitespace("a\nb"); got != "a\nb" {
		t.Errorf("single newline must survive, got %q", got)
	}
}
