package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// These tests cover the MCP 2026-07-28 transport changes (SEP-2567 sessions
// removed, SEP-2575 initialize removed, SEP-2549 cache hints) against the
// real middleware chain httpHandler builds, with real SDK clients at both
// protocol generations. The SDK only serves >= 2026-07-28 clients from a
// stateless handler, so the version routing in protocolRouter is what keeps
// new and old clients working on one endpoint.

const legacyProtocolVersion = "2025-11-25"

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newHTTPTestServer stands up httpHandler with the given options behind an
// httptest server. AllowAnon is not consulted by httpHandler (ServeHTTP
// enforces it before building the chain), so no token is needed here.
func newHTTPTestServer(t *testing.T, opts HTTPOptions) (*Server, *httptest.Server) {
	t.Helper()
	srv := New("frugal", "test-version", quietLogger())
	ts := httptest.NewServer(srv.httpHandler(opts))
	t.Cleanup(ts.Close)
	return srv, ts
}

// connectClient runs a real SDK client against url at the requested
// protocol version and returns the live session.
func connectClient(t *testing.T, ctx context.Context, url, protocolVersion string) *sdkmcp.ClientSession {
	t.Helper()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "test"}, nil)
	transport := &sdkmcp.StreamableClientTransport{Endpoint: url, DisableStandaloneSSE: true}
	session, err := client.Connect(ctx, transport, &sdkmcp.ClientSessionOptions{ProtocolVersion: protocolVersion})
	if err != nil {
		t.Fatalf("client connect (protocol %s): %v", protocolVersion, err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// postJSONRPC sends a hand-crafted JSON-RPC request the way a Streamable
// HTTP client would. From 2026-07-28 every POST must also name its method in
// the Mcp-Method header (SEP-2575) so gateways can route without parsing the
// body; legacy requests carry neither header.
func postJSONRPC(t *testing.T, url, protocolVersion, method, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if protocolVersion != "" {
		req.Header.Set(protocolVersionHeader, protocolVersion)
	}
	if protocolVersion >= statelessProtocolVersion && protocolVersion != "" {
		req.Header.Set("Mcp-Method", method)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// readJSONRPCResult decodes the JSON-RPC result from a response whose body
// is either plain JSON or a single SSE event carrying JSON.
func readJSONRPCResult(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	payload := string(raw)
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		var data []string
		for _, line := range strings.Split(payload, "\n") {
			if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		payload = strings.Join(data, "")
	}
	var msg struct {
		Result map[string]any  `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		t.Fatalf("decode JSON-RPC response %q: %v", payload, err)
	}
	if len(msg.Error) > 0 {
		t.Fatalf("JSON-RPC error: %s", msg.Error)
	}
	return msg.Result
}

const (
	// A 2026-07-28 tools/list: version and capabilities ride in _meta on
	// every request, there is no initialize beforehand.
	statelessListToolsBody = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
		`"io.modelcontextprotocol/clientCapabilities":{},` +
		`"io.modelcontextprotocol/clientInfo":{"name":"test-client","version":"test"}}}}`
	statelessDiscoverBody = `{"jsonrpc":"2.0","id":2,"method":"server/discover","params":{"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
		`"io.modelcontextprotocol/clientCapabilities":{},` +
		`"io.modelcontextprotocol/clientInfo":{"name":"test-client","version":"test"}}}}`
	legacyInitializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25",` +
		`"capabilities":{},"clientInfo":{"name":"test-client","version":"test"}}}`
)

// TestHTTP_SpecCurrentClientConnects is the headline: a real client speaking
// 2026-07-28 gets a session and a tool list through the default (dual-mode)
// handler. Before this change the SDK answered it with
// UnsupportedProtocolVersion because the handler kept sessions.
func TestHTTP_SpecCurrentClientConnects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, ts := newHTTPTestServer(t, HTTPOptions{})

	session := connectClient(t, ctx, ts.URL, statelessProtocolVersion)
	if got := session.InitializeResult().ProtocolVersion; got != statelessProtocolVersion {
		t.Fatalf("negotiated protocol = %q, want %q", got, statelessProtocolVersion)
	}
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if result.TTLMs != int(catalogTTL/time.Millisecond) {
		t.Errorf("tools/list ttlMs = %d, want %d", result.TTLMs, int(catalogTTL/time.Millisecond))
	}
	if result.CacheScope != "public" {
		t.Errorf("tools/list cacheScope = %q, want public", result.CacheScope)
	}
}

// TestHTTP_LegacyClientStillConnects: the same endpoint keeps serving a
// session-based client, and negotiates its version rather than forcing the
// new one.
func TestHTTP_LegacyClientStillConnects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, ts := newHTTPTestServer(t, HTTPOptions{})

	session := connectClient(t, ctx, ts.URL, legacyProtocolVersion)
	if got := session.InitializeResult().ProtocolVersion; got != legacyProtocolVersion {
		t.Fatalf("negotiated protocol = %q, want %q", got, legacyProtocolVersion)
	}
	if _, err := session.ListTools(ctx, nil); err != nil {
		t.Fatalf("ListTools over legacy session: %v", err)
	}
}

// TestHTTP_DualMode_RoutesOnProtocolHeader checks the wire-level contract:
// a 2026-07-28 request is answered without a session header, a legacy
// initialize on the same handler still mints one.
func TestHTTP_DualMode_RoutesOnProtocolHeader(t *testing.T) {
	_, ts := newHTTPTestServer(t, HTTPOptions{})

	resp := postJSONRPC(t, ts.URL, statelessProtocolVersion, "tools/list", statelessListToolsBody)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("stateless tools/list: status %d: %s", resp.StatusCode, body)
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.Errorf("2026-07-28 request got Mcp-Session-Id %q; stateless responses must not carry one", sid)
	}
	result := readJSONRPCResult(t, resp)
	if _, ok := result["tools"]; !ok {
		t.Errorf("tools/list result missing tools: %v", result)
	}

	resp = postJSONRPC(t, ts.URL, "", "initialize", legacyInitializeBody)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("legacy initialize: status %d: %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Mcp-Session-Id") == "" {
		t.Error("legacy initialize on the dual-mode handler should still open a session")
	}
}

// TestHTTP_ServerDiscover: the SEP-2575 replacement for initialize answers
// with the supported versions and carries the catalog cache hint.
func TestHTTP_ServerDiscover(t *testing.T) {
	_, ts := newHTTPTestServer(t, HTTPOptions{})

	resp := postJSONRPC(t, ts.URL, statelessProtocolVersion, "server/discover", statelessDiscoverBody)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("server/discover: status %d: %s", resp.StatusCode, body)
	}
	result := readJSONRPCResult(t, resp)
	versions, _ := result["supportedVersions"].([]any)
	found := false
	for _, v := range versions {
		if v == statelessProtocolVersion {
			found = true
		}
	}
	if !found {
		t.Errorf("server/discover supportedVersions = %v, want to include %s", versions, statelessProtocolVersion)
	}
	if ttl, _ := result["ttlMs"].(float64); int(ttl) != int(catalogTTL/time.Millisecond) {
		t.Errorf("server/discover ttlMs = %v, want %d", result["ttlMs"], int(catalogTTL/time.Millisecond))
	}
	if result["cacheScope"] != "public" {
		t.Errorf("server/discover cacheScope = %v, want public", result["cacheScope"])
	}
}

// TestHTTP_StatelessOption_AppliesToEveryClient: with --stateless, even a
// legacy initialize gets no session and the GET event stream is declined
// with 405, which is the load-balancer-safe behavior.
func TestHTTP_StatelessOption_AppliesToEveryClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, ts := newHTTPTestServer(t, HTTPOptions{Stateless: true})

	resp := postJSONRPC(t, ts.URL, "", "initialize", legacyInitializeBody)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("legacy initialize in stateless mode: status %d: %s", resp.StatusCode, body)
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.Errorf("stateless mode minted Mcp-Session-Id %q", sid)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	getResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET event stream in stateless mode: status %d, want 405", getResp.StatusCode)
	}

	// Both client generations still complete a full round trip.
	for _, v := range []string{statelessProtocolVersion, legacyProtocolVersion} {
		session := connectClient(t, ctx, ts.URL, v)
		if _, err := session.ListTools(ctx, nil); err != nil {
			t.Errorf("ListTools at %s in stateless mode: %v", v, err)
		}
	}
}

// TestHTTP_StatelessRouting_SurvivesMiddleware: the version routing sits
// under auth and rate limiting, so a bearer-protected deployment serves
// 2026-07-28 clients too.
func TestHTTP_StatelessRouting_SurvivesMiddleware(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const token = "s3cret"
	srv := New("frugal", "test-version", quietLogger())
	ts := httptest.NewServer(srv.httpHandler(HTTPOptions{AuthToken: token, RateLimitPerMinute: 600}))
	defer ts.Close()

	authed := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		r.Header.Set("Authorization", "Bearer "+token)
		return http.DefaultTransport.RoundTrip(r)
	})}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "test"}, nil)
	transport := &sdkmcp.StreamableClientTransport{Endpoint: ts.URL, HTTPClient: authed, DisableStandaloneSSE: true}
	session, err := client.Connect(ctx, transport, &sdkmcp.ClientSessionOptions{ProtocolVersion: statelessProtocolVersion})
	if err != nil {
		t.Fatalf("authed 2026-07-28 connect: %v", err)
	}
	defer session.Close()
	if _, err := session.ListTools(ctx, nil); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	// Without the token the chain still refuses, regardless of version.
	resp := postJSONRPC(t, ts.URL, statelessProtocolVersion, "tools/list", statelessListToolsBody)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated 2026-07-28 request: status %d, want 401", resp.StatusCode)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRequestIsStateless(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{"", false},
		{"2025-03-26", false},
		{"2025-06-18", false},
		{"2025-11-25", false},
		{"2026-07-28", true},
		{" 2026-07-28 ", true},
		{"2027-01-01", true},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		if tc.header != "" {
			r.Header.Set(protocolVersionHeader, tc.header)
		}
		if got := requestIsStateless(r); got != tc.want {
			t.Errorf("requestIsStateless(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

// TestStdio_ListToolsCarriesCacheHints: the cache hint is set at the server
// level, so it also reaches in-memory and stdio sessions, not only HTTP.
func TestStdio_ListToolsCarriesCacheHints(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv := New("frugal", "test-version", quietLogger())
	clientT, serverT := sdkmcp.NewInMemoryTransports()
	serverSession, err := srv.Inner.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer serverSession.Close()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer clientSession.Close()
	result, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if result.TTLMs != int(catalogTTL/time.Millisecond) || result.CacheScope != "public" {
		t.Errorf("ListTools cache hint = (%d, %q), want (%d, public)", result.TTLMs, result.CacheScope, int(catalogTTL/time.Millisecond))
	}
}
