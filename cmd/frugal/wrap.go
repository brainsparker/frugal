package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/frugalsh/frugal/internal/classifier"
	"github.com/frugalsh/frugal/internal/config"
	"github.com/frugalsh/frugal/internal/provider"
	provanthropic "github.com/frugalsh/frugal/internal/provider/anthropic"
	provgoogle "github.com/frugalsh/frugal/internal/provider/google"
	provopenai "github.com/frugalsh/frugal/internal/provider/openai"
	"github.com/frugalsh/frugal/internal/proxy"
	"github.com/frugalsh/frugal/internal/router"
)

// runWrap starts the proxy on a free port, runs the given command with
// OPENAI_BASE_URL injected, and shuts down the proxy when the command exits.
func runWrap(configPath string, args []string) int {
	// Sync pricing
	if err := runSync(configPath); err != nil {
		log.Printf("warning: pricing sync failed: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal: failed to load config: %v\n", err)
		return 1
	}

	registry := provider.NewRegistry()
	registerProviders(cfg, registry)

	if len(registry.AllModels()) == 0 {
		fmt.Fprintln(os.Stderr, "frugal: no API keys found. Set OPENAI_API_KEY, ANTHROPIC_API_KEY, or GOOGLE_API_KEY.")
		return 1
	}

	cls := classifier.NewRuleBased()
	modelEntries, thresholds := router.BuildTaxonomy(cfg)
	rtr := router.New(modelEntries, thresholds)
	h := proxy.NewHandler(cls, rtr, registry)

	r := chi.NewRouter()
	r.Use(proxy.HeaderExtractionMiddleware)
	r.Post("/v1/chat/completions", h.ChatCompletions)
	r.Get("/v1/models", h.ListModels)
	r.Get("/v1/routing/explain", h.RoutingExplain)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// Find a free port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal: failed to find free port: %v\n", err)
		return 1
	}
	port := listener.Addr().(*net.TCPAddr).Port
	baseURL := fmt.Sprintf("http://127.0.0.1:%d/v1", port)

	// Start proxy in background
	server := &http.Server{Handler: r}
	go func() {
		if err := server.Serve(listener); err != http.ErrServerClosed {
			log.Printf("proxy error: %v", err)
		}
	}()

	// Wait for proxy to be ready
	waitForReady(fmt.Sprintf("http://127.0.0.1:%d/health", port))

	fmt.Fprintf(os.Stderr, "frugal: proxy running on :%d → routing across %d models\n", port, len(registry.AllModels()))

	// Run the user's command with OPENAI_BASE_URL set
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = injectEnv(os.Environ(), baseURL)

	// Forward signals to the child process
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigCh {
			if cmd.Process != nil {
				cmd.Process.Signal(sig)
			}
		}
	}()

	exitCode := 0
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			fmt.Fprintf(os.Stderr, "frugal: failed to run command: %v\n", err)
			exitCode = 1
		}
	}

	// Shut down proxy
	server.Close()
	return exitCode
}

func registerProviders(cfg *config.Config, registry *provider.Registry) {
	if pc, ok := cfg.Providers["openai"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			registry.Register(provopenai.New(key, pc.BaseURL, modelNames(pc)))
		}
	}
	if pc, ok := cfg.Providers["anthropic"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			registry.Register(provanthropic.New(key, pc.BaseURL, modelNames(pc)))
		}
	}
	if pc, ok := cfg.Providers["google"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			registry.Register(provgoogle.New(key, pc.BaseURL, modelNames(pc)))
		}
	}
}

func injectEnv(environ []string, baseURL string) []string {
	out := make([]string, 0, len(environ)+2)
	for _, e := range environ {
		// Don't override if user already set these
		out = append(out, e)
	}
	// Append — last value wins in most runtimes
	out = append(out, "OPENAI_BASE_URL="+baseURL)
	out = append(out, "OPENAI_API_BASE="+baseURL) // older Python SDK
	return out
}

func waitForReady(url string) {
	for i := 0; i < 50; i++ {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
