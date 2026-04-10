package proxy

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/frugalsh/frugal/internal/types"
)

func TestHeaderExtractionMiddleware_NormalizesQualityHeader(t *testing.T) {
	var got types.QualityThreshold

	h := HeaderExtractionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = QualityFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Frugal-Quality", " COST ")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if got != types.QualityCost {
		t.Fatalf("expected quality %q, got %q", types.QualityCost, got)
	}
}

func TestHeaderExtractionMiddleware_SanitizesFallbackHeader(t *testing.T) {
	var got []string

	h := HeaderExtractionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = FallbacksFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Frugal-Fallback", " gpt-4o-mini, ,claude-sonnet-4-20250514, gpt-4o-mini ,,gemini-2.5-flash ")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	want := []string{"gpt-4o-mini", "claude-sonnet-4-20250514", "gemini-2.5-flash"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected fallback chain %v, got %v", want, got)
	}
}
