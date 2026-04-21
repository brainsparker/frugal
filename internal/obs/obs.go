// Package obs wires structured logging, request IDs, and a small set of
// cross-cutting observability primitives used by the rest of the codebase.
// The public surface is intentionally tiny so we can swap implementations
// (slog handler, ID generator) without touching callers.
package obs

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"log/slog"
	"os"
	"strings"
)

type ctxKey int

const (
	requestIDKey ctxKey = iota
	loggerKey
)

// InitLogger configures the process-wide default slog logger from
// FRUGAL_LOG_LEVEL (debug|info|warn|error) and FRUGAL_LOG_FORMAT (text|json).
// Text output preserves the human-readable local-dev experience; json is
// what deployers pipe into collectors.
func InitLogger() *slog.Logger {
	level := parseLevel(os.Getenv("FRUGAL_LOG_LEVEL"))
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	switch strings.ToLower(os.Getenv("FRUGAL_LOG_FORMAT")) {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	default:
		handler = slog.NewTextHandler(os.Stderr, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// NewRequestID returns a random 16-byte ID in unpadded base32. Cryptographic
// randomness is overkill for a trace ID but free and avoids collisions
// entirely without a central sequencer.
func NewRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Fall back to deterministic-but-unique ID only in the impossible
		// event rand.Read fails. A bad ID is better than a dropped request.
		return "req-00000000000000000000000000"
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf[:])
}

// WithRequestID attaches a request ID to a context. Retrieved via RequestID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the request ID stored in ctx, or "" when absent.
func RequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// WithLogger attaches a pre-scoped slog.Logger to ctx. Handlers pull this
// via L(ctx) so request-scoped attrs (request_id, model, provider) are
// never lost between helpers.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// L returns the request-scoped logger if one was attached via WithLogger,
// otherwise the process default. Safe to call with a nil or background ctx.
func L(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok && l != nil {
			return l
		}
	}
	return slog.Default()
}
