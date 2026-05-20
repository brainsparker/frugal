// Package mcp wires Frugal as an MCP server — the second of Frugal's two
// primary surfaces alongside the CLI. Agent clients (Claude Code, Claude
// Desktop, Cursor, custom MCP hosts) connect to an instance of this server
// and call routed tools (frugal__search, frugal__chat, …); the routing
// decision happens server-side inside each tools/call.
//
// PR 3 ships the scaffold: server construction, stdio + Streamable HTTP
// transports, and a tool registry that's intentionally empty (tools/list
// returns no entries). PR 4 registers frugal__search. The scaffold is
// validated against a real client via NewInMemoryTransports in tests.
//
// See STRATEGY.md for the product positioning and component-status matrix.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/obs"
)

// Server wraps an *mcp.Server with the bits the CLI driver needs (logger,
// graceful HTTP shutdown). The embedded *mcp.Server is exported so future
// PRs can register tools via mcp.AddTool[In, Out](s.Inner, …).
type Server struct {
	Inner  *mcp.Server
	Logger *slog.Logger
}

// New constructs a Frugal MCP server with no tools registered. Callers add
// tools via mcp.AddTool against s.Inner before calling ServeStdio /
// ServeHTTP.
//
// name + version are advertised in the MCP `initialize` response so clients
// can show "frugal vX.Y" in their UIs. Both are required — name is the
// programmatic identifier, version is what users see.
func New(name, version string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    name,
		Title:   "frugal",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: "frugal routes each MCP tool call to the cheapest reliable provider " +
			"per use case. See https://frugal.sh for the recipe model and routing decisions.",
		Logger: logger,
	})
	return &Server{Inner: srv, Logger: logger}
}

// ServeStdio runs the MCP server over stdio — the transport Claude Desktop,
// Claude Code, and Cursor use for locally-installed servers. Blocks until
// the client closes the connection or ctx is cancelled.
//
// stdio is single-session by design: one client process owns the
// stdin/stdout pair for this binary's lifetime. For multi-client serving
// (e.g. a shared deployment), use ServeHTTP instead.
func (s *Server) ServeStdio(ctx context.Context) error {
	s.Logger.Info("mcp serve", "transport", "stdio")
	err := s.Inner.Run(ctx, &mcp.StdioTransport{})
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return nil
	}
	// The SDK wraps the underlying stream's EOF in a "server is closing"
	// message — that's the normal end-of-session signal when the client
	// disconnects, not an error condition for us to surface.
	if strings.Contains(err.Error(), "server is closing") {
		return nil
	}
	return fmt.Errorf("stdio serve: %w", err)
}

// HTTPOptions tunes ServeHTTP. Zero value is "no auth, no rate limit, no
// metrics endpoint, default timeouts" — same behavior as the pre-options
// signature. Use it to bolt on auth and rate-limiting when exposing the
// server beyond localhost.
type HTTPOptions struct {
	// AuthToken, when non-empty, enables bearer-token auth: every request
	// must carry `Authorization: Bearer <AuthToken>` or get 401. Compared
	// in constant time to avoid timing oracles.
	AuthToken string
	// AllowAnon disables the refuse-to-start guard. Without it, ServeHTTP
	// returns an error when AuthToken is empty — exposing an unauth'd MCP
	// server beyond localhost is a foot-gun we won't make easy. Set
	// AllowAnon=true to opt back into anonymous mode (e.g. localhost-only
	// or behind a trusted reverse proxy).
	AllowAnon bool
	// RateLimitPerMinute is the per-IP request budget. ≤0 disables.
	RateLimitPerMinute int
	// Metrics, when non-nil, enables a /metrics endpoint that renders
	// Prometheus text-format counters. Bypasses auth so scrapers don't
	// need the bearer token.
	Metrics *obs.Metrics
	// RequestTimeout caps a single HTTP request. Zero = unlimited (the SDK
	// already handles long-poll'ish streaming; cap at the proxy layer for
	// remote deployments).
	RequestTimeout time.Duration
}

// ServeHTTP runs the MCP server over Streamable HTTP on addr. Returns when
// the http.Server stops — either because ctx was cancelled (graceful
// shutdown, bounded by a 30s drain) or because ListenAndServe errored.
//
// Streamable HTTP is the spec-current alternative to stdio (legacy SSE was
// deprecated in the 2025-03-26 revision). It supports multiple concurrent
// sessions per server instance — Claude Code's `--transport http` mode and
// remote deployments both use it.
//
// The handler returns the same Server for every request — Frugal is
// single-tenant per binary, so per-request server lookup isn't useful here.
// Auth + rate-limit live in HTTPOptions; a zero-value HTTPOptions still
// requires explicit AllowAnon to start (see HTTPOptions.AllowAnon).
func (s *Server) ServeHTTP(ctx context.Context, addr string, opts HTTPOptions) error {
	if opts.AuthToken == "" && !opts.AllowAnon {
		return errors.New("mcp serve: --http requires FRUGAL_AUTH_TOKEN or --allow-anon (refusing to expose an unauthenticated MCP server)")
	}

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return s.Inner
	}, nil)

	mux := http.NewServeMux()
	if opts.Metrics != nil {
		// /metrics bypasses auth so Prometheus scrapers don't need the
		// bearer token. Keep it on a known path the operator controls.
		mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			if err := opts.Metrics.WritePrometheus(w); err != nil {
				s.Logger.Warn("metrics endpoint write", "err", err)
			}
		})
	}
	mux.Handle("/", mcpHandler)

	var handler http.Handler = mux
	handler = withRequestTimeout(handler, opts.RequestTimeout)
	if opts.RateLimitPerMinute > 0 {
		handler = withRateLimit(handler, opts.RateLimitPerMinute)
	}
	if opts.AuthToken != "" {
		// /metrics path stays unauth'd; the auth middleware skips it.
		handler = withBearerAuth(handler, opts.AuthToken, "/metrics")
	}
	handler = withSecurityHeaders(handler)

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.Logger.Info("mcp serve",
			"transport", "streamable-http",
			"addr", addr,
			"auth", opts.AuthToken != "",
			"rate_limit_rpm", opts.RateLimitPerMinute,
			"metrics_endpoint", opts.Metrics != nil)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.Logger.Info("mcp serve: shutdown signal received; draining in-flight sessions")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			s.Logger.Warn("mcp serve: shutdown error", "err", err)
		}
		return nil
	}
}

// withSecurityHeaders applies conservative HTTP security defaults suitable
// for MCP responses that may contain prompts, tool inputs, or model output.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// withBearerAuth rejects requests whose Authorization header doesn't carry
// `Bearer <token>`. Paths in skipPaths bypass — used for /metrics so
// Prometheus scrapers don't need the bearer.
func withBearerAuth(next http.Handler, token string, skipPaths ...string) http.Handler {
	skip := make(map[string]struct{}, len(skipPaths))
	for _, p := range skipPaths {
		skip[p] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := skip[r.URL.Path]; ok {
			next.ServeHTTP(w, r)
			return
		}
		got := bearerTokenFromHeader(r.Header.Get("Authorization"))
		// Constant-time compare to avoid leaking the token via timing.
		if !constantTimeStringEqual(got, token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="frugal"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerTokenFromHeader extracts the bearer token while treating the
// auth-scheme as case-insensitive and tolerating surrounding whitespace.
func bearerTokenFromHeader(v string) string {
	v = strings.TrimSpace(v)
	if len(v) < 7 || !strings.EqualFold(v[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(v[7:])
}

// constantTimeStringEqual compares two strings in constant time. Pulled
// out so the auth middleware stays readable.
func constantTimeStringEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// withRequestTimeout caps the handler's per-request runtime via
// http.TimeoutHandler when d > 0. d=0 is a no-op.
func withRequestTimeout(next http.Handler, d time.Duration) http.Handler {
	if d <= 0 {
		return next
	}
	return http.TimeoutHandler(next, d, "request timed out")
}

// withRateLimit applies a per-IP token bucket: rpm requests per minute,
// refilled steadily. Buckets are kept in a sync.Map keyed by the client
// IP (best-effort — behind a proxy, set proxy headers or terminate TLS
// before this layer).
func withRateLimit(next http.Handler, rpm int) http.Handler {
	if rpm <= 0 {
		return next
	}
	return &rateLimited{next: next, rpm: rpm, buckets: sync.Map{}}
}

const (
	maxRateLimitBuckets = 10000
	bucketTTL           = 10 * time.Minute
)

type rateLimited struct {
	next             http.Handler
	rpm              int
	buckets          sync.Map // key=ip, value=*rlBucket
	bucketCount      int64
	lastCleanupNanos int64
}

type rlBucket struct {
	mu        sync.Mutex
	remaining int
	resetAt   time.Time
}

func (r *rateLimited) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	now := time.Now()
	r.maybeCleanup(now)
	ip := clientIP(req)

	bAny, loaded := r.buckets.LoadOrStore(ip, &rlBucket{remaining: r.rpm, resetAt: now.Add(time.Minute)})
	if !loaded {
		if atomic.AddInt64(&r.bucketCount, 1) > maxRateLimitBuckets {
			r.buckets.Delete(ip)
			atomic.AddInt64(&r.bucketCount, -1)
			http.Error(w, "rate limiter capacity reached", http.StatusServiceUnavailable)
			return
		}
	}
	b := bAny.(*rlBucket)
	b.mu.Lock()
	if !now.Before(b.resetAt) {
		b.remaining = r.rpm
		b.resetAt = now.Add(time.Minute)
	}
	if b.remaining <= 0 {
		retry := int(time.Until(b.resetAt).Seconds()) + 1
		if retry < 1 {
			retry = 1
		}
		b.mu.Unlock()
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retry))
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	b.remaining--
	b.mu.Unlock()
	r.next.ServeHTTP(w, req)
}

func (r *rateLimited) maybeCleanup(now time.Time) {
	last := time.Unix(0, atomic.LoadInt64(&r.lastCleanupNanos))
	if now.Sub(last) < time.Minute {
		return
	}
	if !atomic.CompareAndSwapInt64(&r.lastCleanupNanos, last.UnixNano(), now.UnixNano()) {
		return
	}
	staleBefore := now.Add(-bucketTTL)
	r.buckets.Range(func(key, value any) bool {
		b, ok := value.(*rlBucket)
		if !ok {
			return true
		}
		b.mu.Lock()
		resetAt := b.resetAt
		b.mu.Unlock()
		if resetAt.Before(staleBefore) {
			r.buckets.Delete(key)
			atomic.AddInt64(&r.bucketCount, -1)
		}
		return true
	})
}

// clientIP best-effort extracts the client's IP. Strips port from
// RemoteAddr; doesn't honor X-Forwarded-For (terminate TLS / strip proxy
// headers before this layer if you trust them).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
