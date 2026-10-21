package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

func TestSearchReceipt_ListsEveryAttempt(t *testing.T) {
	broken := &fakeSearcher{name: "a-broken", cost: 0, err: routing.Transient("a-broken", 503, errors.New("down"))}
	empty := &fakeSearcher{name: "b-empty", cost: 0}
	paid := &fakeSearcher{name: "c-paid", cost: 0.002, results: []search.Item{{Title: "hit", URL: "https://x.example"}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{broken, empty, paid}, nil)
	client, cleanup := dialClient(t, srv)
	defer cleanup()

	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "frugal__search", Arguments: map[string]any{"query": "q"}})
	if err != nil || res.IsError {
		t.Fatalf("CallTool: %v %+v", err, res)
	}
	out, err := decodeSearchOutput(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if out.ProviderUsed != "c-paid" || out.CostUSD != 0.002 || out.TotalCostUSD != 0.002 {
		t.Fatalf("receipt: provider=%s cost=%v total=%v", out.ProviderUsed, out.CostUSD, out.TotalCostUSD)
	}
	want := []struct{ p, o string }{{"a-broken", "error"}, {"b-empty", "empty"}, {"c-paid", "won"}}
	if len(out.Attempts) != len(want) {
		t.Fatalf("attempts = %+v", out.Attempts)
	}
	for i, w := range want {
		a := out.Attempts[i]
		if a.Provider != w.p || a.Outcome != w.o {
			t.Errorf("attempt %d = %+v, want %s/%s", i, a, w.p, w.o)
		}
	}
	if out.Attempts[0].ErrorKind != "transient" || out.Attempts[0].CostUSD != 0 || out.Attempts[0].Error == "" {
		t.Errorf("failed attempt = %+v, want transient, $0, error text", out.Attempts[0])
	}
}

func TestExtractReceipt_FailedPaidAttemptNotBilled(t *testing.T) {
	paidFail := &fakeExtractor{name: "a-paid", cost: 0.001, err: routing.Permanent("a-paid", 402, errors.New("quota"))}
	ok := &fakeExtractor{name: "b-ok", cost: 0.003, res: extract.Result{Markdown: "body", CostUSD: 0.003}}
	srv := newExtractServer()
	RegisterExtract(srv, []extract.Extractor{paidFail, ok}, nil, WithPolicy(routing.Policy{Order: []string{"a-paid", "b-ok"}}))
	client, cleanup := dialClient(t, srv)
	defer cleanup()
	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "frugal__extract", Arguments: map[string]any{"url": "https://x.example/p"}})
	if err != nil || res.IsError {
		t.Fatalf("CallTool: %v %+v", err, res)
	}
	var out ExtractOutput
	if err := remarshal(res.StructuredContent, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Attempts) != 2 || out.Attempts[0].Outcome != "error" || out.Attempts[0].CostUSD != 0 || out.Attempts[1].Outcome != "won" {
		t.Fatalf("attempts = %+v", out.Attempts)
	}
	if out.TotalCostUSD != 0.003 || out.CostUSD != 0.003 {
		t.Fatalf("cost=%v total=%v, want 0.003", out.CostUSD, out.TotalCostUSD)
	}
}

func remarshal(raw any, v any) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
