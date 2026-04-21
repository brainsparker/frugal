package main

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"log"
	"log/slog"
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
		slog.Warn("pricing sync failed", "err", err)
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
	modelEntries = filterRegisteredModels(modelEntries, registry)
	if len(modelEntries) == 0 {
		fmt.Fprintln(os.Stderr, "frugal: no routable models available for registered providers")
		return 1
	}
	rtr := router.New(modelEntries, thresholds)
	h := proxy.NewHandler(cls, rtr, registry)

	// Wrap mode always binds loopback, but shared machines still expose the
	// port to any local user. Generate a one-shot bearer token per wrap, seal
	// the proxy behind it, and hand the same token to the child via
	// OPENAI_API_KEY so the SDK authenticates transparently. The user's real
	// upstream key stays in Frugal's environment and never touches the child.
	authToken := os.Getenv("FRUGAL_AUTH_TOKEN")
	if authToken == "" {
		authToken = newSessionToken()
	}
	r := chi.NewRouter()
	r.Use(proxy.RequestIDMiddleware)
	r.Use(proxy.RecoverMiddleware)
	r.Use(proxy.AuthMiddleware(authToken))
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
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Warn("proxy serve error", "err", err)
		}
	}()

	// Wait for proxy to be ready (bounded; fail hard if unreachable).
	if err := waitForReady(fmt.Sprintf("http://127.0.0.1:%d/health", port)); err != nil {
		fmt.Fprintf(os.Stderr, "frugal: proxy did not become ready: %v\n", err)
		_ = server.Close()
		return 1
	}

	fmt.Fprintf(os.Stderr, "frugal: proxy running on :%d → routing across %d models\n", port, len(registry.AllModels()))

	// Run the user's command with OPENAI_BASE_URL set. OPENAI_API_KEY is
	// overwritten with the proxy's session token — the real upstream key
	// stays in Frugal's environment only.
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = injectEnv(os.Environ(), baseURL, authToken)

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "frugal: failed to start command: %v\n", err)
		_ = server.Close()
		return 1
	}

	// Forward signals to the child. signal.Notify installed AFTER Start so
	// signals never race a half-spawned process.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigCh {
			if p := cmd.Process; p != nil {
				_ = p.Signal(sig)
			}
		}
	}()

	exitCode := 0
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			fmt.Fprintf(os.Stderr, "frugal: command exited with error: %v\n", err)
			exitCode = 1
		}
	}
	signal.Stop(sigCh)
	close(sigCh)

	// Drain in-flight proxy requests before the wrapped command's exit code
	// propagates back to the caller.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Warn("proxy shutdown returned error", "err", err)
	}
	return exitCode
}

func registerProviders(cfg *config.Config, registry *provider.Registry) {
	if pc, ok := cfg.Providers["openai"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			registry.Register(provider.WithRetry(provopenai.New(key, pc.BaseURL, modelNames(pc))))
		}
	}
	if pc, ok := cfg.Providers["anthropic"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			registry.Register(provider.WithRetry(provanthropic.New(key, pc.BaseURL, modelNames(pc))))
		}
	}
	if pc, ok := cfg.Providers["google"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			registry.Register(provider.WithRetry(provgoogle.New(key, pc.BaseURL, modelNames(pc))))
		}
	}
}

func injectEnv(environ []string, baseURL, authToken string) []string {
	out := make([]string, 0, len(environ)+3)
	out = append(out, environ...)
	out = upsertEnv(out, "OPENAI_BASE_URL", baseURL)
	out = upsertEnv(out, "OPENAI_API_BASE", baseURL) // older Python SDK
	if authToken != "" {
		out = upsertEnv(out, "OPENAI_API_KEY", authToken)
	}
	return out
}

// newSessionToken produces a random 128-bit bearer token in unpadded base32
// for per-wrap auth. It is only ever passed in-process and to the child via
// environment, never logged.
func newSessionToken() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand failure is unrecoverable; log.Fatalf still works here
		// because obs is initialised before runWrap.
		log.Fatalf("frugal: failed to generate session token: %v", err)
	}
	return "frugal-" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf[:])
}

func upsertEnv(environ []string, key, value string) []string {
	prefix := key + "="
	for i, e := range environ {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			environ[i] = prefix + value
			return environ
		}
	}
	return append(environ, prefix+value)
}

// waitForReady polls the proxy /health endpoint with a tight per-call timeout.
// A misconfigured HTTP proxy env var or bad DNS would otherwise stall on the
// default client's 30s-plus timeout; cap the overall wait at ~2s so we fail
// fast and the wrapped command never inherits a half-started proxy.
func waitForReady(url string) error {
	client := &http.Client{Timeout: 50 * time.Millisecond}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("proxy did not become ready within 2s")
}
