package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/frugalsh/frugal/internal/classifier"
	"github.com/frugalsh/frugal/internal/config"
	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/provider/anthropic"
	"github.com/frugalsh/frugal/internal/provider/google"
	"github.com/frugalsh/frugal/internal/provider/openai"
	"github.com/frugalsh/frugal/internal/proxy"
	"github.com/frugalsh/frugal/internal/router"
)

func main() {
	configPath := "config/models.yaml"
	if p := os.Getenv("FRUGAL_CONFIG"); p != "" {
		configPath = p
	}

	// Handle subcommands
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "sync":
			if err := runSync(configPath); err != nil {
				log.Fatalf("sync failed: %v", err)
			}
			return
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
		log.Printf("warning: pricing sync failed (using cached config): %v", err)
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
			registry.Register(openai.New(key, pc.BaseURL, models))
			log.Printf("registered openai provider with %d models", len(models))
		}
	}

	if pc, ok := cfg.Providers["anthropic"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			models := modelNames(pc)
			registry.Register(anthropic.New(key, pc.BaseURL, models))
			log.Printf("registered anthropic provider with %d models", len(models))
		}
	}

	if pc, ok := cfg.Providers["google"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			models := modelNames(pc)
			registry.Register(google.New(key, pc.BaseURL, models))
			log.Printf("registered google provider with %d models", len(models))
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

	// Build HTTP handler
	h := proxy.NewHandler(cls, rtr, registry)

	addr := "127.0.0.1:8080"
	if a := os.Getenv("FRUGAL_ADDR"); a != "" {
		addr = a
	}

	authToken := os.Getenv("FRUGAL_AUTH_TOKEN")
	if err := guardUnauthenticatedBind(addr, authToken); err != nil {
		log.Fatalf("startup rejected: %v", err)
	}

	rps := envIntOrDefault("FRUGAL_RPS", 30)
	burst := envIntOrDefault("FRUGAL_BURST", 60)

	// Wire routes
	r := chi.NewRouter()
	r.Use(proxy.RecoverMiddleware)
	r.Use(proxy.LoggingMiddleware)
	r.Use(proxy.RateLimitMiddleware(rps, burst))
	r.Use(proxy.AuthMiddleware(authToken))
	r.Use(proxy.HeaderExtractionMiddleware)

	r.Post("/v1/chat/completions", h.ChatCompletions)
	r.Get("/v1/models", h.ListModels)
	r.Get("/v1/routing/explain", h.RoutingExplain)

	// Health check — always unauthenticated so deployment probes keep working.
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	server := newHTTPServer(addr, r)

	log.Printf("frugal listening on %s (auth=%v)", addr, authToken != "")
	if err := server.ListenAndServe(); err != nil {
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
		log.Printf("warning: invalid %s=%q, using default %s", key, value, fallback)
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
		log.Printf("warning: invalid %s=%q, using default %d", key, value, fallback)
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
