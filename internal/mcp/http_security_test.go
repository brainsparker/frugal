package mcp

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/obs"
)

// echoHandler is a minimal handler we wrap with the middlewares to test
// them in isolation — we don't need a real MCP server for auth / rate-
// limit / metrics tests.
func echoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
}

func TestWithBearerAuth_RejectsMissingHeader(t *testing.T) {
	h := withBearerAuth(echoHandler(), "secret")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("WWW-Authenticate"), `Bearer`) {
		t.Errorf("missing WWW-Authenticate header: %q", rec.Header().Get("WWW-Authenticate"))
	}
}

func TestWithBearerAuth_RejectsWrongToken(t *testing.T) {
	h := withBearerAuth(echoHandler(), "secret")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer not-the-token")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestWithBearerAuth_AcceptsCorrectToken(t *testing.T) {
	h := withBearerAuth(echoHandler(), "secret")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestWithBearerAuth_SkipsConfiguredPaths(t *testing.T) {
	h := withBearerAuth(echoHandler(), "secret", "/metrics")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("/metrics should bypass auth; got status %d", rec.Code)
	}
}

func TestWithRateLimit_BlocksAfterBudget(t *testing.T) {
	h := withRateLimit(echoHandler(), 3) // 3/min budget
	const ip = "192.0.2.10:5555"
	doReq := func() int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	// First 3 succeed.
	for i := 0; i < 3; i++ {
		if got := doReq(); got != http.StatusOK {
			t.Errorf("attempt %d: status = %d, want 200", i+1, got)
		}
	}
	// 4th gets throttled.
	if got := doReq(); got != http.StatusTooManyRequests {
		t.Errorf("4th attempt: status = %d, want 429", got)
	}
}

func TestWithRateLimit_PerIPIsolation(t *testing.T) {
	h := withRateLimit(echoHandler(), 1) // 1/min budget per IP
	for _, ip := range []string{"192.0.2.10:1234", "192.0.2.11:1234"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("ip %s should get its own bucket; got %d", ip, rec.Code)
		}
	}
}

func TestServeHTTP_RefusesAnonymousWithoutFlag(t *testing.T) {
	srv := New("frugal", "v", slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := srv.ServeHTTP(context.Background(), ":0", HTTPOptions{}) // no token, no allow-anon
	if err == nil || !strings.Contains(err.Error(), "FRUGAL_AUTH_TOKEN") {
		t.Errorf("expected refusal mentioning FRUGAL_AUTH_TOKEN; got %v", err)
	}
}

func TestServeHTTP_WhitespaceOnlyTokenStillRefusesAnonymousWithoutFlag(t *testing.T) {
	srv := New("frugal", "v", slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := srv.ServeHTTP(context.Background(), ":0", HTTPOptions{AuthToken: " \t\n "})
	if err == nil || !strings.Contains(err.Error(), "FRUGAL_AUTH_TOKEN") {
		t.Errorf("expected refusal for whitespace-only token; got %v", err)
	}
}

func TestWithBearerAuth_AcceptsTrimmedTokenValueFromOptions(t *testing.T) {
	h := withBearerAuth(echoHandler(), strings.TrimSpace("  secret\t"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestServeHTTP_MetricsEndpointBypassesAuth(t *testing.T) {
	m := obs.NewMetrics()
	m.RecordCall("youcom", 100*time.Millisecond, 0.005, nil)

	// Build the same handler chain ServeHTTP wires up so we can hit it
	// through httptest without owning a real listener.
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_ = m.WritePrometheus(w)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("mcp")) })
	h := withBearerAuth(mux, "secret", "/metrics")
	ts := httptest.NewServer(h)
	defer ts.Close()

	// Unauthenticated /metrics → 200 with text body.
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("get metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/metrics status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "frugal_calls_total") {
		t.Errorf("/metrics body missing expected counter; got: %s", body)
	}

	// Unauthenticated / → 401.
	resp2, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("get /: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("/ status without auth = %d, want 401", resp2.StatusCode)
	}
}
