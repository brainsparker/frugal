package proxy

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/frugalsh/frugal/internal/types"
)

type contextKey string

const (
	qualityKey  contextKey = "frugal_quality"
	fallbackKey contextKey = "frugal_fallback"
)

// QualityFromContext extracts the quality threshold from the request context.
func QualityFromContext(ctx context.Context) types.QualityThreshold {
	if v, ok := ctx.Value(qualityKey).(types.QualityThreshold); ok {
		return v
	}
	return types.QualityBalanced
}

// FallbacksFromContext extracts the fallback chain from the request context.
func FallbacksFromContext(ctx context.Context) []string {
	if v, ok := ctx.Value(fallbackKey).([]string); ok {
		return v
	}
	return nil
}

// HeaderExtractionMiddleware extracts X-Frugal-* headers into the request context.
func HeaderExtractionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if q := r.Header.Get("X-Frugal-Quality"); q != "" {
			ctx = context.WithValue(ctx, qualityKey, types.ParseQualityThreshold(q))
		} else {
			ctx = context.WithValue(ctx, qualityKey, types.QualityBalanced)
		}

		if fb := r.Header.Get("X-Frugal-Fallback"); fb != "" {
			parts := strings.Split(fb, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			ctx = context.WithValue(ctx, fallbackKey, parts)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// LoggingMiddleware logs request method, path, status, and duration.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

// Ensure statusWriter implements http.Flusher for SSE.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
