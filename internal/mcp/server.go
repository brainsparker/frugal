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
// See frugal-strategy-v6.md §7 for the product-surface framing and §8 for
// the component-status matrix that says which tools have shipped.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
// Auth + rate-limit middleware can wrap the http.Handler at the caller's
// discretion (Phase 1 PR 6 ports the proxy-era token check).
func (s *Server) ServeHTTP(ctx context.Context, addr string) error {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return s.Inner
	}, nil)

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.Logger.Info("mcp serve", "transport", "streamable-http", "addr", addr)
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
