package tools

import (
	"context"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/browse"
	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

func testDomainPolicy(t *testing.T, allow, deny []string, denyPrivate bool) *routing.DomainPolicy {
	t.Helper()
	p, err := routing.NewDomainPolicy(allow, deny, denyPrivate)
	if err != nil {
		t.Fatalf("NewDomainPolicy: %v", err)
	}
	return p
}

func domainCallTool(t *testing.T, srv *sdkmcp.Server, name string, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	client, cleanup := dialClient(t, srv)
	defer cleanup()
	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

func TestExtract_DomainPolicyRefusesBeforeAnyProviderCall(t *testing.T) {
	ex := &fakeExtractor{name: "goreadability", cost: 0, res: extract.Result{Markdown: "should not be read"}}
	srv := newExtractServer()
	RegisterExtract(srv, []extract.Extractor{ex}, nil,
		WithDomainPolicy(testDomainPolicy(t, nil, []string{"blocked.example"}, false)))

	res := domainCallTool(t, srv, "frugal__extract", map[string]any{"url": "https://www.blocked.example/page"})
	if !res.IsError {
		t.Fatalf("expected refusal, got %+v", res.StructuredContent)
	}
	msg := errorText(res)
	for _, want := range []string{"frugal__extract", "domain policy", "blocked.example", "routing.domains.deny"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q should mention %q", msg, want)
		}
	}
	if ex.lastQuery.URL != "" {
		t.Errorf("provider must not be called for a refused host; got %q", ex.lastQuery.URL)
	}

	// A pinned provider is refused the same way: the policy is about the
	// target, not the route.
	res = domainCallTool(t, srv, "frugal__extract", map[string]any{"url": "https://blocked.example/", "provider": "goreadability"})
	if !res.IsError || ex.lastQuery.URL != "" {
		t.Fatalf("pinned call to a denied host must be refused without a provider call")
	}

	// An allowed host still flows through unchanged.
	res = domainCallTool(t, srv, "frugal__extract", map[string]any{"url": "https://fine.example/"})
	if res.IsError {
		t.Fatalf("allowed host errored: %s", errorText(res))
	}
	if ex.lastQuery.URL != "https://fine.example/" {
		t.Errorf("allowed host should reach the provider, got %q", ex.lastQuery.URL)
	}
}

func TestBrowse_DomainPolicyDenyPrivateRefusesLocalTargets(t *testing.T) {
	br := &fakeBrowser{name: "browserless", cost: 0.002, res: browse.Result{HTML: "<html>"}}
	srv := newServer()
	RegisterBrowse(srv, []browse.Browser{br}, nil,
		WithDomainPolicy(testDomainPolicy(t, nil, nil, true)))

	res := domainCallTool(t, srv, "frugal__browse", map[string]any{"url": "http://169.254.169.254/latest/meta-data/"})
	if !res.IsError {
		t.Fatalf("metadata endpoint must be refused under deny_private")
	}
	if !strings.Contains(errorText(res), "deny_private") {
		t.Errorf("error should name deny_private: %s", errorText(res))
	}
	if br.lastQuery.URL != "" {
		t.Errorf("provider must not be called; got %q", br.lastQuery.URL)
	}

	res = domainCallTool(t, srv, "frugal__browse", map[string]any{"url": "https://public.example/"})
	if res.IsError {
		t.Fatalf("public host errored: %s", errorText(res))
	}
}

func TestSearch_DomainPolicyFiltersResultsAndWarns(t *testing.T) {
	s := &fakeSearcher{name: "free", cost: 0, results: []search.Item{
		{Title: "keep", URL: "https://docs.example/a"},
		{Title: "drop", URL: "https://reddit.com/r/x"},
		{Title: "drop-sub", URL: "https://old.reddit.com/r/y"},
		{Title: "no-url"},
	}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{s}, nil,
		WithDomainPolicy(testDomainPolicy(t, nil, []string{"reddit.com"}, false)))

	res := domainCallTool(t, srv, "frugal__search", map[string]any{"query": "anything"})
	if res.IsError {
		t.Fatalf("isError: %s", errorText(res))
	}
	out, err := decodeSearchOutput(res.StructuredContent)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Results) != 2 || out.Results[0].Title != "keep" || out.Results[1].Title != "no-url" {
		t.Errorf("results after filtering = %+v", out.Results)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "dropped 2 results") {
		t.Errorf("warnings = %v", out.Warnings)
	}
	// The provider's own results slice is never mutated.
	if len(s.results) != 4 {
		t.Errorf("provider slice mutated: %d", len(s.results))
	}
}

func TestSearch_DomainPolicyAllowListKeepsOnlyFetchableHosts(t *testing.T) {
	s := &fakeSearcher{name: "free", cost: 0, results: []search.Item{
		{Title: "in", URL: "https://docs.python.org/3/"},
		{Title: "out", URL: "https://pypi.org/"},
	}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{s}, nil,
		WithDomainPolicy(testDomainPolicy(t, []string{"docs.python.org"}, nil, false)))

	res := domainCallTool(t, srv, "frugal__search", map[string]any{"query": "anything"})
	out, err := decodeSearchOutput(res.StructuredContent)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Results) != 1 || out.Results[0].Title != "in" {
		t.Errorf("results = %+v", out.Results)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "dropped 1 result ") {
		t.Errorf("warnings = %v", out.Warnings)
	}
}

func TestSearch_NoDomainPolicyLeavesResultsUntouched(t *testing.T) {
	s := &fakeSearcher{name: "free", cost: 0, results: []search.Item{
		{Title: "a", URL: "http://localhost/"},
		{Title: "b", URL: "https://reddit.com/"},
	}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{s}, nil)
	res := domainCallTool(t, srv, "frugal__search", map[string]any{"query": "anything"})
	out, err := decodeSearchOutput(res.StructuredContent)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Results) != 2 || len(out.Warnings) != 0 {
		t.Errorf("zero config must not filter: %+v %v", out.Results, out.Warnings)
	}
}

func TestExecute_URLIntentRefusedByDomainPolicySkipsExtractAndBrowse(t *testing.T) {
	ex := &fakeExtractor{name: "goreadability", cost: 0, res: extract.Result{}}
	br := &fakeBrowser{name: "browserless", cost: 0.002, res: browse.Result{Text: "rendered"}}
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{&fakeSearcher{name: "free"}}, []extract.Extractor{ex}, []browse.Browser{br}, nil,
		WithDomainPolicy(testDomainPolicy(t, []string{"docs.example"}, nil, false)))

	// Bare-domain intents are read as https and judged by host.
	res := callExecute(t, srv, map[string]any{"intent": "read intranet.example/wiki/secrets"})
	if !res.IsError {
		t.Fatalf("expected refusal, got %+v", res.StructuredContent)
	}
	msg := errorText(res)
	if !strings.Contains(msg, "frugal__execute") || !strings.Contains(msg, "routing.domains.allow") {
		t.Errorf("error = %q", msg)
	}
	if ex.lastQuery.URL != "" || br.lastQuery.URL != "" {
		t.Errorf("neither extract (%q) nor the browse fall-forward (%q) may run for a refused host", ex.lastQuery.URL, br.lastQuery.URL)
	}

	res = callExecute(t, srv, map[string]any{"intent": "render https://docs.example/app after js"})
	if res.IsError {
		t.Fatalf("allowed host errored: %s", errorText(res))
	}
	if br.lastQuery.URL != "https://docs.example/app" {
		t.Errorf("allowed browse intent should reach the renderer, got %q", br.lastQuery.URL)
	}
}

func TestExecute_SearchIntentResultsFilteredByDomainPolicy(t *testing.T) {
	s := &fakeSearcher{name: "free", cost: 0, results: []search.Item{
		{Title: "keep", URL: "https://a.example/"},
		{Title: "drop", URL: "http://10.0.0.7/status"},
	}}
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{s}, nil, nil, nil,
		WithDomainPolicy(testDomainPolicy(t, nil, nil, true)))

	res := callExecute(t, srv, map[string]any{"intent": "search for status pages"})
	if res.IsError {
		t.Fatalf("isError: %s", errorText(res))
	}
	out := decodeExecuteOutput(t, res.StructuredContent)
	if out.Capability != "search" || len(out.Results) != 1 || out.Results[0].Title != "keep" {
		t.Errorf("got %+v", out)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "domain policy dropped 1 result") {
		t.Errorf("warnings = %v", out.Warnings)
	}
}
