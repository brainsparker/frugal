package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/frugalsh/frugal/internal/obs"
)

func TestRequestIDMiddleware_GeneratesWhenMissing(t *testing.T) {
	var captured string
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = obs.RequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	if captured == "" {
		t.Fatalf("expected a generated request ID in context")
	}
	if got := rec.Header().Get("X-Request-ID"); got == "" {
		t.Fatalf("expected X-Request-ID response header")
	}
	if rec.Header().Get("X-Request-ID") != captured {
		t.Fatalf("header %q != context %q", rec.Header().Get("X-Request-ID"), captured)
	}
}

func TestRequestIDMiddleware_PropagatesInbound(t *testing.T) {
	var captured string
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = obs.RequestID(r.Context())
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "upstream-trace-abc")
	handler.ServeHTTP(rec, req)

	if captured != "upstream-trace-abc" {
		t.Fatalf("expected propagated ID, got %q", captured)
	}
	if rec.Header().Get("X-Request-ID") != "upstream-trace-abc" {
		t.Fatalf("expected echoed header, got %q", rec.Header().Get("X-Request-ID"))
	}
}

func TestRequestIDMiddleware_RejectsOverlongInbound(t *testing.T) {
	oversized := make([]byte, 200)
	for i := range oversized {
		oversized[i] = 'x'
	}

	var captured string
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = obs.RequestID(r.Context())
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", string(oversized))
	handler.ServeHTTP(rec, req)

	if captured == string(oversized) {
		t.Fatalf("expected oversized inbound ID to be replaced")
	}
	if captured == "" {
		t.Fatalf("expected a fresh generated ID when inbound is rejected")
	}
}

func TestRequestIDMiddleware_RejectsUnsafeCharacters(t *testing.T) {
	var captured string
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = obs.RequestID(r.Context())
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "trace id with spaces")
	handler.ServeHTTP(rec, req)

	if captured == "trace id with spaces" {
		t.Fatalf("expected unsafe inbound ID to be replaced")
	}
	if captured == "" {
		t.Fatalf("expected generated request ID")
	}
}

func TestIsSafeRequestID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{name: "valid simple", id: "abc-123", want: true},
		{name: "valid punctuation", id: "trace_id:abc.def-123", want: true},
		{name: "empty", id: "", want: false},
		{name: "contains space", id: "abc 123", want: false},
		{name: "contains slash", id: "abc/123", want: false},
		{name: "contains unicode", id: "abc-µ", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSafeRequestID(tc.id); got != tc.want {
				t.Fatalf("isSafeRequestID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}
