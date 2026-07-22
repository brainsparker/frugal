package tools

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/search"
)

// decodeSearchOutput re-marshals the SDK's any-typed StructuredContent
// (which the SDK populates as a map[string]any from the Out generic) into
// our typed SearchOutput. Roundtripping through JSON is the simplest path
// that doesn't depend on the SDK's internal representation.
func decodeSearchOutput(raw any) (SearchOutput, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return SearchOutput{}, err
	}
	var out SearchOutput
	if err := json.Unmarshal(b, &out); err != nil {
		return SearchOutput{}, err
	}
	return out, nil
}

type fakeSearcher struct {
	name      string
	cost      float64
	results   []search.Item
	err       error
	lastQuery search.Query
}

func (f *fakeSearcher) Name() string         { return f.name }
func (f *fakeSearcher) CostPerCall() float64 { return f.cost }
func (f *fakeSearcher) Search(_ context.Context, q search.Query) (search.Results, error) {
	f.lastQuery = q
	if f.err != nil {
		return search.Results{}, f.err
	}
	return search.Results{Items: f.results, CostUSD: f.cost}, nil
}

func newServer() *sdkmcp.Server {
	return sdkmcp.NewServer(&sdkmcp.Implementation{Name: "frugal-test", Version: "test"}, &sdkmcp.ServerOptions{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func dialClient(t *testing.T, srv *sdkmcp.Server) (*sdkmcp.ClientSession, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	clientT, serverT := sdkmcp.NewInMemoryTransports()
	serverSess, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		cancel()
		t.Fatalf("server connect: %v", err)
	}

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "1"}, nil)
	clientSess, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		serverSess.Close()
		cancel()
		t.Fatalf("client connect: %v", err)
	}
	return clientSess, func() {
		clientSess.Close()
		serverSess.Close()
		cancel()
	}
}

func TestRegisterSearch_ToolAppearsInToolsList(t *testing.T) {
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{
		&fakeSearcher{name: "fake", cost: 0.001},
	}, nil)

	client, cleanup := dialClient(t, srv)
	defer cleanup()

	res, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(res.Tools))
	}
	if res.Tools[0].Name != "frugal__search" {
		t.Errorf("tool name: got %q want frugal__search", res.Tools[0].Name)
	}
}

func TestRegisterSearch_NoSearchersSkipsRegistration(t *testing.T) {
	srv := newServer()
	RegisterSearch(srv, nil, nil)

	client, cleanup := dialClient(t, srv)
	defer cleanup()

	res, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 0 {
		t.Errorf("expected 0 tools when no searchers configured, got %d", len(res.Tools))
	}
}

func TestCallTool_RoutesToCheapest(t *testing.T) {
	expensive := &fakeSearcher{
		name: "expensive", cost: 0.01,
		results: []search.Item{{Title: "EXPENSIVE"}},
	}
	cheap := &fakeSearcher{
		name: "cheap", cost: 0.001,
		results: []search.Item{{Title: "CHEAP", URL: "https://cheap.example/x", Snippet: "hit"}},
	}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{expensive, cheap}, nil)

	client, cleanup := dialClient(t, srv)
	defer cleanup()

	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name:      "frugal__search",
		Arguments: map[string]any{"query": "anything"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned isError: %+v", res.Content)
	}
	out, err := decodeSearchOutput(res.StructuredContent)
	if err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
	if out.ProviderUsed != "cheap" {
		t.Errorf("provider_used: got %q want cheap (cheapest)", out.ProviderUsed)
	}
	if len(out.Results) != 1 || out.Results[0].Title != "CHEAP" {
		t.Errorf("unexpected results: %+v", out.Results)
	}
	if cheap.lastQuery.Text != "anything" {
		t.Errorf("cheap.lastQuery.Text = %q", cheap.lastQuery.Text)
	}
	if expensive.lastQuery.Text != "" {
		t.Errorf("expensive should not have been called; got %q", expensive.lastQuery.Text)
	}
}

func TestCallTool_ExplicitProviderOverridesAuto(t *testing.T) {
	expensive := &fakeSearcher{
		name: "expensive", cost: 0.01,
		results: []search.Item{{Title: "EXPENSIVE"}},
	}
	cheap := &fakeSearcher{
		name: "cheap", cost: 0.001,
		results: []search.Item{{Title: "CHEAP"}},
	}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{expensive, cheap}, nil)

	client, cleanup := dialClient(t, srv)
	defer cleanup()

	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "frugal__search",
		Arguments: map[string]any{
			"query":    "anything",
			"provider": "expensive",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	out, err := decodeSearchOutput(res.StructuredContent)
	if err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
	if out.ProviderUsed != "expensive" {
		t.Errorf("explicit provider override failed: got %q want expensive", out.ProviderUsed)
	}
}

func TestCallTool_NormalizesProviderAndAutoValue(t *testing.T) {
	expensive := &fakeSearcher{name: "expensive", cost: 0.01, results: []search.Item{{Title: "EXPENSIVE"}}}
	cheap := &fakeSearcher{name: "cheap", cost: 0.001, results: []search.Item{{Title: "CHEAP"}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{expensive, cheap}, nil)

	client, cleanup := dialClient(t, srv)
	defer cleanup()

	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "frugal__search",
		Arguments: map[string]any{
			"query":    "x",
			"provider": "  ExPenSive  ",
		},
	})
	if err != nil {
		t.Fatalf("CallTool transport: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res)
	}
	out, err := decodeSearchOutput(res.StructuredContent)
	if err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
	if out.ProviderUsed != "expensive" {
		t.Fatalf("provider normalization failed: got %q want expensive", out.ProviderUsed)
	}

	res, err = client.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "frugal__search",
		Arguments: map[string]any{
			"query":    "x",
			"provider": "  AuTo  ",
		},
	})
	if err != nil {
		t.Fatalf("CallTool transport: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res)
	}
	out, err = decodeSearchOutput(res.StructuredContent)
	if err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
	if out.ProviderUsed != "cheap" {
		t.Fatalf("auto normalization failed: got %q want cheap", out.ProviderUsed)
	}
}

func TestCallTool_NormalizesFreshnessValue(t *testing.T) {
	cheap := &fakeSearcher{name: "cheap", cost: 0.001, results: []search.Item{{Title: "ok"}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{cheap}, nil)

	client, cleanup := dialClient(t, srv)
	defer cleanup()

	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "frugal__search",
		Arguments: map[string]any{
			"query":     "x",
			"freshness": "  WEEK  ",
		},
	})
	if err != nil {
		t.Fatalf("CallTool transport: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res)
	}
	if cheap.lastQuery.Freshness != "week" {
		t.Fatalf("freshness normalization failed: got %q want week", cheap.lastQuery.Freshness)
	}
}

func TestCallTool_InvalidFreshnessErrors(t *testing.T) {
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{&fakeSearcher{name: "only", cost: 0.001}}, nil)

	client, cleanup := dialClient(t, srv)
	defer cleanup()

	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "frugal__search",
		Arguments: map[string]any{
			"query":     "x",
			"freshness": "year",
		},
	})
	if err != nil {
		t.Fatalf("CallTool transport: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected isError=true for invalid freshness")
	}
	joined := ""
	for _, c := range res.Content {
		if t, ok := c.(*sdkmcp.TextContent); ok {
			joined += t.Text
		}
	}
	if !strings.Contains(joined, "freshness must be one of") {
		t.Fatalf("missing freshness validation message: %q", joined)
	}
}

func TestCallTool_UnknownProviderErrors(t *testing.T) {
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{&fakeSearcher{name: "only", cost: 0.001}}, nil)

	client, cleanup := dialClient(t, srv)
	defer cleanup()

	res, err := client.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name: "frugal__search",
		Arguments: map[string]any{
			"query":    "x",
			"provider": "does-not-exist",
		},
	})
	if err != nil {
		// SDK may surface tool errors via the result rather than as a Go error.
		t.Fatalf("CallTool transport: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected isError=true for unknown provider, got result=%+v", res)
	}
}
