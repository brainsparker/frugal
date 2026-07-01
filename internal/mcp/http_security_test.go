package mcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
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

func TestWithBearerAuth_AcceptsCaseInsensitiveBearerScheme(t *testing.T) {
	h := withBearerAuth(echoHandler(), "secret")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "bearer secret")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestWithBearerAuth_AcceptsExtraHeaderWhitespace(t *testing.T) {
	h := withBearerAuth(echoHandler(), "secret")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "  Bearer   secret  ")
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

func TestWithRateLimit_ResetsAtBoundaryInstant(t *testing.T) {
	r := &rateLimited{next: echoHandler(), rpm: 1}
	ip := "192.0.2.77"
	now := time.Now()
	r.buckets.Store(ip, &rlBucket{remaining: 0, resetAt: now})
	atomic.StoreInt64(&r.bucketCount, 1)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = ip + ":1234"
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestServeHTTP_RefusesAnonymousWithoutFlag(t *testing.T) {
	srv := New("frugal", "v", slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := srv.ServeHTTP(context.Background(), ":0", HTTPOptions{}) // no token, no allow-anon
	if err == nil || !strings.Contains(err.Error(), "FRUGAL_AUTH_TOKEN") {
		t.Errorf("expected refusal mentioning FRUGAL_AUTH_TOKEN; got %v", err)
	}
}

func TestServeHTTP_RefusesAnonymousNonLocalBind(t *testing.T) {
	srv := New("frugal", "v", slog.New(slog.NewTextHandler(io.Discard, nil)))
	// A bare port binds all interfaces, so it must be refused too.
	for _, addr := range []string{"0.0.0.0:0", ":0"} {
		err := srv.ServeHTTP(context.Background(), addr, HTTPOptions{AllowAnon: true})
		if err == nil || !strings.Contains(err.Error(), "localhost bind address") {
			t.Errorf("addr %q: expected localhost-bind refusal; got %v", addr, err)
		}
	}
}

func TestIsLocalhostBind(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		// Empty host binds ALL interfaces in Go — must not count as local.
		{addr: ":8765", want: false},
		{addr: "localhost:8765", want: true},
		{addr: "127.0.0.1:8765", want: true},
		{addr: "[::1]:8765", want: true},
		{addr: "0.0.0.0:8765", want: false},
		{addr: "192.168.1.10:8080", want: false},
	}
	for _, tc := range tests {
		if got := isLocalhostBind(tc.addr); got != tc.want {
			t.Errorf("isLocalhostBind(%q)=%v, want %v", tc.addr, got, tc.want)
		}
	}
}

func TestWithRateLimit_CapsBucketCardinality(t *testing.T) {
	r := &rateLimited{next: echoHandler(), rpm: 1}
	for i := 0; i < maxRateLimitBuckets; i++ {
		a := (i / (255 * 255)) % 255
		b := (i / 255) % 255
		c := i % 255
		ip := "10." + strconv.Itoa(a) + "." + strconv.Itoa(b) + "." + strconv.Itoa(c) + ":1234"
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("pre-cap request %d failed with status %d", i, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.200:9999"
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestRateLimiterCleanup_RemovesStaleBuckets(t *testing.T) {
	r := &rateLimited{next: echoHandler(), rpm: 1}
	old := &rlBucket{remaining: 1, resetAt: time.Now().Add(-bucketTTL - time.Minute)}
	r.buckets.Store("192.0.2.1", old)
	atomic.StoreInt64(&r.bucketCount, 1)
	atomic.StoreInt64(&r.lastCleanupNanos, time.Now().Add(-2*time.Minute).UnixNano())

	r.maybeCleanup(time.Now())
	if _, ok := r.buckets.Load("192.0.2.1"); ok {
		t.Fatal("expected stale bucket to be removed")
	}
	if got := atomic.LoadInt64(&r.bucketCount); got != 0 {
		t.Fatalf("bucketCount = %d, want 0", got)
	}
}

func TestNewHTTPServer_SetsSafeTimeoutDefaults(t *testing.T) {
	srv := newHTTPServer(":0", echoHandler(), 0)
	if srv.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s, want %s", srv.ReadHeaderTimeout, 5*time.Second)
	}
	if srv.ReadTimeout != 30*time.Second {
		t.Fatalf("ReadTimeout = %s, want %s", srv.ReadTimeout, 30*time.Second)
	}
	if srv.WriteTimeout != 60*time.Second {
		t.Fatalf("WriteTimeout = %s, want %s", srv.WriteTimeout, 60*time.Second)
	}
	if srv.IdleTimeout != 60*time.Second {
		t.Fatalf("IdleTimeout = %s, want %s", srv.IdleTimeout, 60*time.Second)
	}
}

func TestServeHTTP_MetricsEndpointBypassesAuth(t *testing.T) {
	m := obs.NewMetrics()
	m.RecordCall("youcom", 100*time.Millisecond, 0.005, true, nil)

	// Exercise the real handler chain ServeHTTP serves, via httptest so we
	// don't own a listener.
	srv := New("frugal", "v", slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := srv.httpHandler(HTTPOptions{AuthToken: "secret", Metrics: m})
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

	// Non-GET/HEAD on /metrics should be rejected.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/metrics", strings.NewReader("x"))
	resp3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post /metrics: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics status = %d, want 405", resp3.StatusCode)
	}
	if allow := resp3.Header.Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("POST /metrics Allow = %q, want %q", allow, "GET, HEAD")
	}
}

func TestServeHTTP_MetricsPathRequiresAuthWhenNotMounted(t *testing.T) {
	// With no Metrics configured, /metrics is just another path routed to
	// the MCP handler — it must not get an auth bypass.
	srv := New("frugal", "v", slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := srv.httpHandler(HTTPOptions{AuthToken: "secret"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("/metrics without mounted endpoint: status = %d, want 401", rec.Code)
	}
}

func TestResolveMaxHeaderBytes_DefaultAndOverride(t *testing.T) {
	if got := resolveMaxHeaderBytes(0); got != 1<<20 {
		t.Fatalf("default max header bytes = %d, want %d", got, 1<<20)
	}
	if got := resolveMaxHeaderBytes(-5); got != 1<<20 {
		t.Fatalf("negative max header bytes = %d, want %d", got, 1<<20)
	}
	if got := resolveMaxHeaderBytes(8192); got != 8192 {
		t.Fatalf("override max header bytes = %d, want %d", got, 8192)
	}
}

func TestWithSecurityHeaders_SetsNoSniffAndNoStore(t *testing.T) {
	h := withSecurityHeaders(echoHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestWithMaxContentLength_RejectsOversizedRequest(t *testing.T) {
	h := withMaxContentLength(echoHandler(), 10)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("01234567890"))
	req.ContentLength = 11
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestWithMaxContentLength_AllowsSmallRequest(t *testing.T) {
	h := withMaxContentLength(echoHandler(), 10)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("0123456789"))
	req.ContentLength = 10
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestWithMaxContentLength_CapsChunkedBody(t *testing.T) {
	// Chunked requests declare no Content-Length (-1), so the fast-path
	// check can't catch them; the body itself must be capped at read time.
	var readErr error
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	})
	h := withMaxContentLength(inner, 10)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("0123456789X"))
	req.ContentLength = -1
	h.ServeHTTP(rec, req)
	var mbe *http.MaxBytesError
	if !errors.As(readErr, &mbe) {
		t.Fatalf("body read err = %v, want *http.MaxBytesError", readErr)
	}
}
