package main

import (
	"log"
	"net/http"
	"os"

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
	rtr := router.New(modelEntries, thresholds)

	// Build HTTP handler
	h := proxy.NewHandler(cls, rtr, registry)

	// Wire routes
	r := chi.NewRouter()
	r.Use(proxy.LoggingMiddleware)
	r.Use(proxy.HeaderExtractionMiddleware)

	r.Post("/v1/chat/completions", h.ChatCompletions)
	r.Get("/v1/models", h.ListModels)
	r.Get("/v1/routing/explain", h.RoutingExplain)

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	addr := ":8080"
	if a := os.Getenv("FRUGAL_ADDR"); a != "" {
		addr = a
	}

	log.Printf("frugal listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func modelNames(pc config.ProviderConfig) []string {
	names := make([]string, 0, len(pc.Models))
	for name := range pc.Models {
		names = append(names, name)
	}
	return names
}
