package proxy

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/frugalsh/frugal/internal/obs"
	"github.com/frugalsh/frugal/internal/types"
)

type contextKey string

const (
	qualityKey  contextKey = "frugal_quality"
	fallbackKey contextKey = "frugal_fallback"
	useCaseKey  contextKey = "frugal_use_case"
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

// UseCaseFromContext extracts the caller-declared use case (from
// X-Frugal-Use-Case header). Returns "" when the header was absent — the
// handler then falls through to non-use-case routing.
func UseCaseFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(useCaseKey).(string); ok {
		return v
	}
	return ""
}

// RequestIDMiddleware propagates or generates an X-Request-ID header, attaches
// it to the request context, and echoes it on the response. The value flows
// through obs.L so every downstream log line (including panics) can be tied
// back to a single request.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if id == "" || len(id) > 128 {
			id = obs.NewRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := obs.WithRequestID(r.Context(), id)
		ctx = obs.WithLogger(ctx, obs.L(ctx).With("request_id", id))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RateLimitMiddleware enforces a global token-bucket on the proxy's serve
// entrypoints. rps <= 0 disables the limiter entirely (local dev). Exceeded
// requests receive a 429 with a stable error body and no upstream call is
// issued, protecting the operator's provider keys from loops or abuse.
func RateLimitMiddleware(rps, burst int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if rps <= 0 {
			return next
		}
		if burst < rps {
			burst = rps
		}
		limiter := rate.NewLimiter(rate.Limit(rps), burst)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow() {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"message": "rate limit exceeded",
						"type":    "frugal_rate_limit_error",
						"code":    "rate_limited",
					},
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AuthMiddleware gates the proxy behind a shared bearer token. When the token
// is empty, the middleware is a no-op (local single-user deployments). When
// set, requests must carry `Authorization: Bearer <token>`; the comparison is
// constant-time. Missing or mismatched tokens return 401 with a stable error
// shape; the request body and headers are never logged.
func AuthMiddleware(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if token == "" {
			return next
		}
		want := []byte(token)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := bearerFromHeader(r.Header.Get("Authorization"))
			if got == "" || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("WWW-Authenticate", `Bearer realm="frugal"`)
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"message": "missing or invalid authorization",
						"type":    "frugal_auth_error",
						"code":    "unauthorized",
					},
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearerFromHeader(h string) string {
	const prefix = "Bearer "
	if len(h) <= len(prefix) {
		return ""
	}
	// Case-insensitive prefix match per RFC 6750.
	if !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// HeaderExtractionMiddleware extracts X-Frugal-* headers into the request
// context. Unknown X-Frugal-Quality values return 400 up front so typos
// surface to the caller rather than silently coercing to balanced.
func HeaderExtractionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if q := r.Header.Get("X-Frugal-Quality"); q != "" {
			qt, ok := types.ParseQualityThreshold(q)
			if !ok {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"X-Frugal-Quality must be one of: high, balanced, cost","type":"frugal_error","code":"invalid_quality"}}`))
				return
			}
			ctx = context.WithValue(ctx, qualityKey, qt)
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

		// Use case header is validated against the registry by the handler,
		// not here — middleware shouldn't need the registry reference.
		if uc := strings.TrimSpace(r.Header.Get("X-Frugal-Use-Case")); uc != "" {
			ctx = context.WithValue(ctx, useCaseKey, uc)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RecoverMiddleware catches panics from handlers and returns a structured 500.
func RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				obs.L(r.Context()).Error("panic recovered",
					"method", r.Method,
					"path", r.URL.Path,
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"message": "internal server error",
						"type":    "frugal_error",
					},
				})
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// LoggingMiddleware emits a single structured log line per request with
// method, path, status, duration, and any attrs added downstream.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		obs.L(r.Context()).Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
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
