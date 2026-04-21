package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestOKHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`ok`))
	})
}

func TestAuthMiddleware_NoTokenIsNoOp(t *testing.T) {
	h := AuthMiddleware("")(newTestOKHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with empty token, got %d", rec.Code)
	}
}

func TestAuthMiddleware_RejectsMissingHeader(t *testing.T) {
	h := AuthMiddleware("secret-token")(newTestOKHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without header, got %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got == "" {
		t.Fatalf("expected WWW-Authenticate challenge, got empty")
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("expected JSON error body, got %q", rec.Body.String())
	}
}

func TestAuthMiddleware_RejectsWrongToken(t *testing.T) {
	h := AuthMiddleware("secret-token")(newTestOKHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer nope")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", rec.Code)
	}
}

func TestAuthMiddleware_AcceptsCorrectToken(t *testing.T) {
	h := AuthMiddleware("secret-token")(newTestOKHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with correct token, got %d", rec.Code)
	}
}

func TestAuthMiddleware_CaseInsensitiveBearerPrefix(t *testing.T) {
	h := AuthMiddleware("secret")(newTestOKHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "bearer secret")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with lowercase bearer, got %d", rec.Code)
	}
}

func TestRateLimitMiddleware_TrivialRpsDisables(t *testing.T) {
	h := RateLimitMiddleware(0, 0)(newTestOKHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with disabled limiter, got %d", rec.Code)
	}
}

func TestRateLimitMiddleware_RejectsOverBurst(t *testing.T) {
	h := RateLimitMiddleware(1, 1)(newTestOKHandler())

	// First request consumes the single burst token.
	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("first request: expected 200, got %d", rec.Code)
		}
	}

	// Second request in rapid succession is rejected.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: expected 429, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "rate_limited") {
		t.Fatalf("expected rate_limited code in body, got %q", rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatalf("expected Retry-After header")
	}
}
