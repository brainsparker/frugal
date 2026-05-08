package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/frugalsh/frugal/internal/classifier"
	"github.com/frugalsh/frugal/internal/config"
	"github.com/frugalsh/frugal/internal/metrics"
	"github.com/frugalsh/frugal/internal/obs"
	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/provider/anthropic"
	"github.com/frugalsh/frugal/internal/provider/google"
	"github.com/frugalsh/frugal/internal/provider/openai"
	"github.com/frugalsh/frugal/internal/proxy"
	"github.com/frugalsh/frugal/internal/router"
	"github.com/frugalsh/frugal/internal/usecase"
)

func main() {
	obs.InitLogger()
	metrics.Register()

	configPath := "config/models.yaml"
	if p := os.Getenv("FRUGAL_CONFIG"); p != "" {
		configPath = p
	}

	// Handle subcommands
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help", "help":
			printHelp()
			return
		case "-v", "--version", "version":
			fmt.Println(version())
			return
		case "sync":
			if err := runSync(configPath); err != nil {
				log.Fatalf("sync failed: %v", err)
			}
			return
		case "bench":
			os.Exit(runBench(configPath, os.Args[2:]))
		case "serve":
			// fall through to server startup
		default:
			// Anything else is treated as a command to wrap
			// frugal python app.py → start proxy, run "python app.py" with OPENAI_BASE_URL set
			os.Exit(runWrap(configPath, os.Args[1:]))
		}
	}

	// Sync pricing from models.dev on startup (non-fatal if it fails)
	if err := runSync(configPath); err != nil {
		slog.Warn("pricing sync failed; using cached config", "err", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	registry := provider.NewRegistry()

	// Register providers based on available API keys
	if pc, ok := cfg.Providers["openai"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			models := modelNames(pc)
			registry.Register(provider.WithRetry(openai.New(key, pc.BaseURL, models)))
			slog.Info("registered provider", "provider", "openai", "models", len(models))
		}
	}

	if pc, ok := cfg.Providers["anthropic"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			models := modelNames(pc)
			registry.Register(provider.WithRetry(anthropic.New(key, pc.BaseURL, models)))
			slog.Info("registered provider", "provider", "anthropic", "models", len(models))
		}
	}

	if pc, ok := cfg.Providers["google"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			models := modelNames(pc)
			registry.Register(provider.WithRetry(google.New(key, pc.BaseURL, models)))
			slog.Info("registered provider", "provider", "google", "models", len(models))
		}
	}

	// Build classifier and router
	cls := classifier.NewRuleBased()
	modelEntries, thresholds := router.BuildTaxonomy(cfg)
	modelEntries = filterRegisteredModels(modelEntries, registry)
	if len(modelEntries) == 0 {
		log.Fatal("no routable models available for registered providers")
	}
	rtr := router.New(modelEntries, thresholds)

	// Load use-case registry. Dir resolves relative to the binary's working
	// directory; override via FRUGAL_USE_CASES_DIR. An absent or empty dir
	// disables use-case routing without failing startup.
	useCaseDir := os.Getenv("FRUGAL_USE_CASES_DIR")
	if useCaseDir == "" {
		useCaseDir = "config/use_cases"
	}
	useCases, err := usecase.Load(useCaseDir)
	if err != nil {
		log.Fatalf("failed to load use cases from %s: %v", useCaseDir, err)
	}
	slog.Info("use cases loaded", "dir", useCaseDir, "count", useCases.Len())

	// Build HTTP handler
	h := proxy.NewHandlerWithUseCases(cls, rtr, registry, useCases)

	addr := "127.0.0.1:8080"
	if a := os.Getenv("FRUGAL_ADDR"); a != "" {
		addr = a
	}

	authToken := os.Getenv("FRUGAL_AUTH_TOKEN")
	if err := guardUnauthenticatedBind(addr, authToken); err != nil {
		log.Fatalf("startup rejected: %v", err)
	}

	rps := envNonNegativeIntOrDefault("FRUGAL_RPS", 30)
	burst := envIntOrDefault("FRUGAL_BURST", 60)

	// Wire routes. Middleware ordering matters: RequestID first so
	// Recover/Logging carry the ID; Auth before any handler touches
	// registry; HeaderExtraction last so per-request controls land on the
	// authenticated ctx.
	r := chi.NewRouter()
	r.Use(proxy.RequestIDMiddleware)
	r.Use(proxy.RecoverMiddleware)
	r.Use(proxy.LoggingMiddleware)
	r.Use(proxy.RateLimitMiddleware(rps, burst))
	r.Use(proxy.AuthMiddleware(authToken))
	r.Use(proxy.HeaderExtractionMiddleware)

	r.Post("/v1/chat/completions", h.ChatCompletions)
	r.Get("/v1/models", h.ListModels)
	r.Get("/v1/routing/explain", h.RoutingExplain)
	r.Get("/v1/bundles", h.ListBundles)
	r.Get("/v1/bundles/{useCase}", h.GetBundle)

	// Health check — always unauthenticated so deployment probes keep working.
	// Reports provider list + model count so operators can distinguish "server
	// up" from "server up with valid routing". Returns 503 when no models are
	// routable so load balancers take the instance out of rotation.
	r.Get("/health", healthHandler(registry))

	// Prometheus metrics. Sits behind the same auth middleware as /v1, so
	// anyone with scrape creds also has chat creds — which is the usual ops
	// posture for a small internal proxy. If operators want separate scrape
	// access they can run a second listener with its own token later.
	r.Handle("/metrics", metrics.Handler())

	server := newHTTPServer(addr, r)

	slog.Info("frugal listening", "addr", addr, "auth", authToken != "")
	if err := runServer(server); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// guardUnauthenticatedBind refuses to start an unauthenticated proxy on a
// non-loopback interface unless the operator has explicitly opted in via
// FRUGAL_ALLOW_UNAUTH=1. The check keeps "no API keys in config? just run it"
// working on localhost while preventing the Fly/Docker footgun where :8080
// binds to 0.0.0.0 and any network traffic can drain the operator's keys.
func guardUnauthenticatedBind(addr, token string) error {
	if token != "" {
		return nil
	}
	if os.Getenv("FRUGAL_ALLOW_UNAUTH") == "1" {
		log.Printf("warning: FRUGAL_ALLOW_UNAUTH=1 set — running without auth on %s", addr)
		return nil
	}
	if isLoopbackBind(addr) {
		return nil
	}
	return &startupError{msg: "refusing to bind " + addr + " without FRUGAL_AUTH_TOKEN; set a token or FRUGAL_ALLOW_UNAUTH=1 to override"}
}

// isLoopbackBind reports whether addr binds only to the loopback interface.
// Accepts forms like "127.0.0.1:8080", "[::1]:8080", "localhost:8080".
func isLoopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

type startupError struct{ msg string }

func (e *startupError) Error() string { return e.msg }

// printHelp prints a one-page summary of commands and flags.
func printHelp() {
	fmt.Println(`frugal — open-source LLM cost-optimizing proxy

Usage:
  frugal <command> [args...]     Wrap any command with the routing proxy
  frugal serve                   Run the proxy as a persistent server
  frugal sync                    Refresh model pricing from models.dev
  frugal bench [flags]           Run cost + quality benchmark against providers
  frugal -v | --version          Print the build version
  frugal -h | --help             Show this help

Common environment:
  FRUGAL_ADDR                    Listen address (serve; default 127.0.0.1:8080)
  FRUGAL_AUTH_TOKEN              Shared bearer token required on non-loopback
  FRUGAL_LOG_LEVEL               debug | info | warn | error
  FRUGAL_LOG_FORMAT              text | json
  FRUGAL_MAX_COST_PER_REQUEST_USD Per-request spend cap (default 1.00)
  OPENAI_API_KEY, ANTHROPIC_API_KEY, GOOGLE_API_KEY  Provider credentials

See README.md for the full list.`)
}

// buildVersion is injected at release time via
//
//	go build -ldflags "-X main.buildVersion=$VERSION"
//
// (see Makefile). It takes precedence over debug.ReadBuildInfo so release
// binaries built with `go build` — not `go install` — still report a real
// tag. Left empty for local `go run` / `go build` without ldflags.
var buildVersion string

// version reports a human-readable build identifier.
func version() string {
	if buildVersion != "" {
		return buildVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}
	return "dev"
}

// healthHandler reports liveness + a shallow inventory of routable models.
// Operators (and Fly/K8s) distinguish "HTTP is up" from "routing is actually
// healthy" — the latter requires at least one registered model.
func healthHandler(registry *provider.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		models := registry.AllModels()
		providers := map[string]bool{}
		for _, m := range models {
			if p, err := registry.Resolve(m); err == nil {
				providers[p.Name()] = true
			}
		}
		names := make([]string, 0, len(providers))
		for n := range providers {
			names = append(names, n)
		}

		status := "ok"
		code := http.StatusOK
		if len(models) == 0 {
			status = "degraded"
			code = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    status,
			"providers": names,
			"models":    len(models),
		})
	}
}

// runServer starts the HTTP server and waits for SIGINT/SIGTERM to trigger a
// graceful shutdown. In-flight requests finish (bounded to shutdownTimeout)
// and the listener is closed. Returns nil on clean shutdown.
func runServer(server *http.Server) error {
	const shutdownTimeout = 30 * time.Second

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutdown signal received; draining in-flight requests")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Warn("server shutdown returned error", "err", err)
		}
		return nil
	}
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: envDurationOrDefault("FRUGAL_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:       envDurationOrDefault("FRUGAL_READ_TIMEOUT", 15*time.Second),
		WriteTimeout:      envDurationOrDefault("FRUGAL_WRITE_TIMEOUT", 120*time.Second),
		IdleTimeout:       envDurationOrDefault("FRUGAL_IDLE_TIMEOUT", 60*time.Second),
		MaxHeaderBytes:    envIntOrDefault("FRUGAL_MAX_HEADER_BYTES", http.DefaultMaxHeaderBytes),
	}
}

func envDurationOrDefault(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		slog.Warn("invalid env duration; using default", "key", key, "value", value, "default", fallback.String())
		return fallback
	}

	return parsed
}

func envIntOrDefault(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		slog.Warn("invalid env int; using default", "key", key, "value", value, "default", fallback)
		return fallback
	}

	return parsed
}

func envNonNegativeIntOrDefault(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		slog.Warn("invalid env int; using default", "key", key, "value", value, "default", fallback)
		return fallback
	}

	return parsed
}

func modelNames(pc config.ProviderConfig) []string {
	names := make([]string, 0, len(pc.Models))
	for name := range pc.Models {
		names = append(names, name)
	}
	return names
}

func filterRegisteredModels(entries []router.ModelEntry, registry *provider.Registry) []router.ModelEntry {
	filtered := make([]router.ModelEntry, 0, len(entries))
	for _, entry := range entries {
		if _, err := registry.Resolve(entry.Name); err == nil {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
