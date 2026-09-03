package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/browse"
	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/search"
)

// Integration tests for the max_chars result cap and the size footer,
// driving real in-memory MCP client sessions like the sibling tool tests.

func decodeBrowseOutputT(t *testing.T, raw any) BrowseOutput {
	t.Helper()
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out BrowseOutput
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal BrowseOutput: %v", err)
	}
	return out
}

func callExtractWith(t *testing.T, srv *sdkmcp.Server, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	client, cleanup := dialExtractClient(t, srv)
	defer cleanup()
	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "frugal__extract", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	return res
}

func TestExtract_NoCapReturnsWholeContentWithFooter(t *testing.T) {
	body := strings.Repeat("word ", 2000) // 10,000 chars
	srv := newExtractServer()
	RegisterExtract(srv, []extract.Extractor{
		&fakeExtractor{name: "free", res: extract.Result{Markdown: body, HTML: "<p>x</p>"}},
	}, nil)
	res := callExtractWith(t, srv, map[string]any{"url": "https://example.com"})
	if res.IsError {
		t.Fatalf("isError: %+v", res.Content)
	}
	out, err := decodeExtractOutput(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if out.Truncated {
		t.Fatal("no cap configured: must not truncate")
	}
	if out.Markdown != body || out.HTML != "<p>x</p>" {
		t.Error("content must be byte-for-byte unchanged without a cap")
	}
	if out.CharsTotal != 10_008 || out.CharsReturned != 10_008 {
		t.Errorf("footer = %d/%d, want 10008/10008", out.CharsReturned, out.CharsTotal)
	}
	if out.EstTokens != 2502 {
		t.Errorf("est_tokens = %d, want 2502", out.EstTokens)
	}
}

func TestExtract_PerCallMaxCharsTruncatesAndReports(t *testing.T) {
	body := strings.Repeat("lorem ipsum ", 1000) // 12,000 chars
	srv := newExtractServer()
	RegisterExtract(srv, []extract.Extractor{
		&fakeExtractor{name: "free", res: extract.Result{Markdown: body, Title: "T"}},
	}, nil)
	res := callExtractWith(t, srv, map[string]any{"url": "https://example.com", "max_chars": 1000})
	if res.IsError {
		t.Fatalf("isError: %+v", res.Content)
	}
	out, err := decodeExtractOutput(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated {
		t.Fatal("expected truncated: true")
	}
	if out.CharsTotal != 12_000 {
		t.Errorf("chars_total = %d, want 12000", out.CharsTotal)
	}
	if out.CharsReturned > 1000 || out.CharsReturned < 900 {
		t.Errorf("chars_returned = %d, want <= 1000 and near it", out.CharsReturned)
	}
	if !strings.Contains(out.Markdown, "[frugal: output truncated to") {
		t.Error("marker missing from truncated markdown")
	}
	if strings.HasSuffix(strings.SplitN(out.Markdown, "\n\n[frugal:", 2)[0], " ") {
		t.Error("kept content should not end in whitespace")
	}
	if out.Title != "T" {
		t.Error("metadata fields must survive the cap")
	}
	if out.EstTokens != (out.CharsReturned+3)/4 {
		t.Errorf("est_tokens = %d for %d chars", out.EstTokens, out.CharsReturned)
	}
}

func TestExtract_ConfiguredDefaultAppliesAndPerCallOverridesIt(t *testing.T) {
	body := strings.Repeat("a", 5000)
	srv := newExtractServer()
	RegisterExtract(srv, []extract.Extractor{
		&fakeExtractor{name: "free", res: extract.Result{Markdown: body}},
	}, nil, WithMaxChars(500))

	// Default from config.
	out, err := decodeExtractOutput(callExtractWith(t, srv, map[string]any{"url": "https://example.com"}).StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated || out.CharsReturned != 500 {
		t.Errorf("configured default not applied: %+v", out.CharsReturned)
	}

	// Per-call raises it above the default.
	out, err = decodeExtractOutput(callExtractWith(t, srv, map[string]any{"url": "https://example.com", "max_chars": 4000}).StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated || out.CharsReturned != 4000 {
		t.Errorf("per-call raise not honored: got %d", out.CharsReturned)
	}

	// Per-call larger than the content: whole thing, not truncated.
	out, err = decodeExtractOutput(callExtractWith(t, srv, map[string]any{"url": "https://example.com", "max_chars": 10_000}).StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if out.Truncated || out.CharsReturned != 5000 {
		t.Errorf("generous per-call cap should return everything: %+v", out.CharsReturned)
	}
}

func TestExtract_BudgetPrefersMarkdownOverHTML(t *testing.T) {
	srv := newExtractServer()
	RegisterExtract(srv, []extract.Extractor{
		&fakeExtractor{name: "free", res: extract.Result{
			Markdown: strings.Repeat("m", 300),
			Text:     strings.Repeat("t", 300),
			HTML:     strings.Repeat("h", 300),
		}},
	}, nil)
	out, err := decodeExtractOutput(callExtractWith(t, srv, map[string]any{"url": "https://example.com", "max_chars": 450}).StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if out.Markdown != strings.Repeat("m", 300) {
		t.Error("markdown must be kept whole when it fits")
	}
	if !strings.HasPrefix(out.Text, strings.Repeat("t", 150)) || !strings.Contains(out.Text, "[frugal:") {
		t.Errorf("text should carry the remaining 150 chars plus the marker, got %d chars", len(out.Text))
	}
	if out.HTML != "" {
		t.Error("html should be dropped once the budget is spent")
	}
	if out.CharsReturned != 450 || out.CharsTotal != 900 || !out.Truncated {
		t.Errorf("footer = %+v", out)
	}
}

func TestExtract_NegativeMaxCharsErrors(t *testing.T) {
	srv := newExtractServer()
	RegisterExtract(srv, []extract.Extractor{&fakeExtractor{name: "free", res: extract.Result{Markdown: "x"}}}, nil)
	res := callExtractWith(t, srv, map[string]any{"url": "https://example.com", "max_chars": -1})
	if !res.IsError || !strings.Contains(errorText(res), "max_chars") {
		t.Errorf("expected a max_chars validation error, got %+v", res.Content)
	}
}

func TestBrowse_MaxCharsCapsTextBeforeHTML(t *testing.T) {
	srv := newBrowseServer()
	RegisterBrowse(srv, []browse.Browser{
		&fakeBrowser{name: "render", cost: 0.002, res: browse.Result{
			Text: strings.Repeat("t", 200), HTML: strings.Repeat("<b>", 200), CostUSD: 0.002,
		}},
	}, nil)
	client, cleanup := dialBrowseClient(t, srv)
	defer cleanup()
	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name:      "frugal__browse",
		Arguments: map[string]any{"url": "https://example.com", "format": "text", "max_chars": 300},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("isError: %+v", res.Content)
	}
	out := decodeBrowseOutputT(t, res.StructuredContent)
	if out.Text != strings.Repeat("t", 200) {
		t.Error("text must survive whole when it fits the budget")
	}
	if !strings.HasPrefix(out.HTML, strings.Repeat("<b>", 33)) || !strings.Contains(out.HTML, "[frugal:") {
		t.Errorf("html should be cut to the remaining budget with a marker, got %q", out.HTML)
	}
	if !out.Truncated || out.CharsTotal != 800 || out.CharsReturned != 300 {
		t.Errorf("footer = returned %d total %d truncated %v", out.CharsReturned, out.CharsTotal, out.Truncated)
	}
	if out.CostUSD != 0.002 || out.ProviderUsed != "render" {
		t.Error("routing receipt must be unaffected by the cap")
	}
}

func TestExecute_ExtractIntentHonorsMaxChars(t *testing.T) {
	srv := newServer()
	RegisterExecute(srv,
		[]search.Searcher{&fakeSearcher{name: "free"}},
		[]extract.Extractor{&fakeExtractor{name: "reader", res: extract.Result{Markdown: strings.Repeat("page ", 1000)}}},
		nil, nil)
	res := callExecute(t, srv, map[string]any{"intent": "read https://example.com/post", "max_chars": 500})
	if res.IsError {
		t.Fatalf("isError: %+v", res.Content)
	}
	out := decodeExecuteOutput(t, res.StructuredContent)
	if out.Capability != "extract" {
		t.Fatalf("capability = %q", out.Capability)
	}
	if !out.Truncated || out.CharsTotal != 5000 || out.CharsReturned > 500 {
		t.Errorf("footer = returned %d total %d truncated %v", out.CharsReturned, out.CharsTotal, out.Truncated)
	}
	if !strings.Contains(out.Markdown, "[frugal: output truncated to") {
		t.Error("marker missing")
	}
}

func TestExecute_FallForwardRenderIsCappedToo(t *testing.T) {
	srv := newServer()
	RegisterExecute(srv,
		[]search.Searcher{&fakeSearcher{name: "free"}},
		[]extract.Extractor{&fakeExtractor{name: "reader", res: extract.Result{}}}, // empty: JS page
		[]browse.Browser{&fakeBrowser{name: "render", cost: 0.002, res: browse.Result{Text: strings.Repeat("dom ", 1000), CostUSD: 0.002}}},
		nil, WithMaxChars(400))
	res := callExecute(t, srv, map[string]any{"intent": "https://example.com/app"})
	if res.IsError {
		t.Fatalf("isError: %+v", res.Content)
	}
	out := decodeExecuteOutput(t, res.StructuredContent)
	if out.Capability != "browse" {
		t.Fatalf("expected fall-forward to browse, got %q", out.Capability)
	}
	if !out.Truncated || out.CharsTotal != 4000 || out.CharsReturned > 400 {
		t.Errorf("configured default not applied on fall-forward: returned %d total %d", out.CharsReturned, out.CharsTotal)
	}
}

func TestExecute_SearchIntentMeasuredNeverTruncated(t *testing.T) {
	items := []search.Item{
		{Title: strings.Repeat("t", 50), URL: "https://a.example", Snippet: strings.Repeat("s", 400)},
		{Title: strings.Repeat("t", 50), URL: "https://b.example", Snippet: strings.Repeat("s", 400)},
	}
	srv := newServer()
	RegisterExecute(srv,
		[]search.Searcher{&fakeSearcher{name: "free", results: items}},
		nil, nil, nil, WithMaxChars(100))
	res := callExecute(t, srv, map[string]any{"intent": "search for something"})
	if res.IsError {
		t.Fatalf("isError: %+v", res.Content)
	}
	out := decodeExecuteOutput(t, res.StructuredContent)
	if out.Truncated {
		t.Error("search results must never be truncated")
	}
	if len(out.Results) != 2 || out.Results[1].Snippet != items[1].Snippet {
		t.Error("search results must be returned whole")
	}
	want := 2 * (50 + len("https://a.example") + 400)
	if out.CharsTotal != want || out.CharsReturned != want {
		t.Errorf("chars = %d/%d, want %d", out.CharsReturned, out.CharsTotal, want)
	}
	if out.EstTokens != (want+3)/4 {
		t.Errorf("est_tokens = %d", out.EstTokens)
	}
}

func TestSearch_ReportsEstTokens(t *testing.T) {
	items := []search.Item{{Title: "abcd", URL: "https://x.io", Snippet: strings.Repeat("z", 84)}} // 4+12+84 = 100 chars
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{&fakeSearcher{name: "free", results: items}}, nil)
	client, cleanup := dialClient(t, srv)
	defer cleanup()
	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name:      "frugal__search",
		Arguments: map[string]any{"query": "q"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	b, _ := json.Marshal(res.StructuredContent)
	var out SearchOutput
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.EstTokens != 25 {
		t.Errorf("est_tokens = %d, want 25 for 100 chars", out.EstTokens)
	}
	if len(out.Results) != 1 || out.Results[0].Snippet != items[0].Snippet {
		t.Error("search results must be returned whole")
	}
}
