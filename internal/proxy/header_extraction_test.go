package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeaderExtractionMiddleware_UseCaseAccepted(t *testing.T) {
	h := HeaderExtractionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := UseCaseFromContext(r.Context()); got != "research-synthesis" {
			t.Fatalf("unexpected use case: %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("X-Frugal-Use-Case", "research-synthesis")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestHeaderExtractionMiddleware_UseCaseRejected(t *testing.T) {
	tests := []string{
		"Research-Synthesis",
		"../../etc/passwd",
		"a_b",
		strings.Repeat("a", 65),
	}

	for _, tc := range tests {
		t.Run(tc, func(t *testing.T) {
			h := HeaderExtractionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
			req.Header.Set("X-Frugal-Use-Case", tc)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", rr.Code)
			}
		})
	}
}

func TestHeaderExtractionMiddleware_FallbackCanonicalization(t *testing.T) {
	h := HeaderExtractionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := FallbacksFromContext(r.Context())
		if len(got) != 2 || got[0] != "gpt-4.1" || got[1] != "claude-sonnet" {
			t.Fatalf("unexpected fallbacks: %#v", got)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("X-Frugal-Fallback", " gpt-4.1 ,, claude-sonnet, gpt-4.1 ")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestHeaderExtractionMiddleware_FallbackTooMany(t *testing.T) {
	h := HeaderExtractionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("X-Frugal-Fallback", "a,b,c,d,e,f,g,h,i,j,k")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHeaderExtractionMiddleware_FallbackModelTooLong(t *testing.T) {
	h := HeaderExtractionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("X-Frugal-Fallback", strings.Repeat("a", 129))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}
