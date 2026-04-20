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
