package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoverMiddleware_RecoversPanic(t *testing.T) {
	h := RecoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}

	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("expected json content-type, got %q", ct)
	}

	var body struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if body.Error.Message != "internal server error" {
		t.Fatalf("unexpected error message: %q", body.Error.Message)
	}
	if body.Error.Type != "frugal_error" {
		t.Fatalf("unexpected error type: %q", body.Error.Type)
	}
}

func TestRecoverMiddleware_PassesThrough(t *testing.T) {
	h := RecoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTeapot {
		t.Fatalf("expected 418, got %d", rr.Code)
	}
	if rr.Body.String() != "ok" {
		t.Fatalf("expected body ok, got %q", rr.Body.String())
	}
}

func TestHeaderExtractionMiddleware_RejectsInvalidUseCaseHeader(t *testing.T) {
	h := HeaderExtractionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("X-Frugal-Use-Case", "Legal Research")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error.Code != "invalid_use_case_header" {
		t.Fatalf("expected invalid_use_case_header, got %q", body.Error.Code)
	}
}

func TestHeaderExtractionMiddleware_AllowsValidUseCaseHeader(t *testing.T) {
	h := HeaderExtractionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := UseCaseFromContext(r.Context()); got != "research-synthesis" {
			t.Fatalf("expected research-synthesis in context, got %q", got)
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

func TestHeaderExtractionMiddleware_NormalizesUseCaseHeaderToLowercase(t *testing.T) {
	h := HeaderExtractionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := UseCaseFromContext(r.Context()); got != "research-synthesis" {
			t.Fatalf("expected normalized research-synthesis in context, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("X-Frugal-Use-Case", "Research-Synthesis")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}
