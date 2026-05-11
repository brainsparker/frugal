package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/router"
	"github.com/frugalsh/frugal/internal/types"
)

type testProvider struct {
	name   string
	models []string
}

func (p *testProvider) Name() string      { return p.name }
func (p *testProvider) Models() []string { return p.models }

func (p *testProvider) ChatCompletion(_ context.Context, _ string, _ *types.ChatCompletionRequest) (*types.ChatCompletionResponse, error) {
	return &types.ChatCompletionResponse{}, nil
}

func (p *testProvider) ChatCompletionStream(_ context.Context, _ string, _ *types.ChatCompletionRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

func TestFilterRegisteredModels(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&testProvider{name: "openai", models: []string{"gpt-4o-mini"}})

	entries := []router.ModelEntry{
		{Name: "gpt-4o-mini", Provider: "openai"},
		{Name: "claude-sonnet-4-20250514", Provider: "anthropic"},
	}

	filtered := filterRegisteredModels(entries, reg)
	if got := len(filtered); got != 1 {
		t.Fatalf("expected 1 registered model, got %d", got)
	}
	if filtered[0].Name != "gpt-4o-mini" {
		t.Fatalf("expected gpt-4o-mini to remain, got %s", filtered[0].Name)
	}
}

func TestNewHTTPServerDefaults(t *testing.T) {
	t.Setenv("FRUGAL_READ_HEADER_TIMEOUT", "")
	t.Setenv("FRUGAL_READ_TIMEOUT", "")
	t.Setenv("FRUGAL_WRITE_TIMEOUT", "")
	t.Setenv("FRUGAL_IDLE_TIMEOUT", "")
	t.Setenv("FRUGAL_MAX_HEADER_BYTES", "")

	srv := newHTTPServer(":8080", http.NewServeMux())

	if srv.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("expected default read header timeout 5s, got %s", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 15*time.Second {
		t.Fatalf("expected default read timeout 15s, got %s", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 120*time.Second {
		t.Fatalf("expected default write timeout 120s, got %s", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 60*time.Second {
		t.Fatalf("expected default idle timeout 60s, got %s", srv.IdleTimeout)
	}
	if srv.MaxHeaderBytes != http.DefaultMaxHeaderBytes {
		t.Fatalf("expected default max header bytes %d, got %d", http.DefaultMaxHeaderBytes, srv.MaxHeaderBytes)
	}
}

func TestNewHTTPServerEnvOverrides(t *testing.T) {
	t.Setenv("FRUGAL_READ_HEADER_TIMEOUT", "6s")
	t.Setenv("FRUGAL_READ_TIMEOUT", "20s")
	t.Setenv("FRUGAL_WRITE_TIMEOUT", "150s")
	t.Setenv("FRUGAL_IDLE_TIMEOUT", "75s")
	t.Setenv("FRUGAL_MAX_HEADER_BYTES", "65536")

	srv := newHTTPServer(":8080", http.NewServeMux())

	if srv.ReadHeaderTimeout != 6*time.Second {
		t.Fatalf("expected read header timeout 6s, got %s", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 20*time.Second {
		t.Fatalf("expected read timeout 20s, got %s", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 150*time.Second {
		t.Fatalf("expected write timeout 150s, got %s", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 75*time.Second {
		t.Fatalf("expected idle timeout 75s, got %s", srv.IdleTimeout)
	}
	if srv.MaxHeaderBytes != 65536 {
		t.Fatalf("expected max header bytes 65536, got %d", srv.MaxHeaderBytes)
	}
}

func TestEnvDurationOrDefaultInvalidValues(t *testing.T) {
	const key = "FRUGAL_TIMEOUT_TEST"

	t.Setenv(key, "not-a-duration")
	if got := envDurationOrDefault(key, 3*time.Second); got != 3*time.Second {
		t.Fatalf("expected fallback for invalid duration, got %s", got)
	}

	t.Setenv(key, "0s")
	if got := envDurationOrDefault(key, 3*time.Second); got != 3*time.Second {
		t.Fatalf("expected fallback for zero duration, got %s", got)
	}

	t.Setenv(key, "-2s")
	if got := envDurationOrDefault(key, 3*time.Second); got != 3*time.Second {
		t.Fatalf("expected fallback for negative duration, got %s", got)
	}
}

func TestEnvIntOrDefaultInvalidValues(t *testing.T) {
	const key = "FRUGAL_INT_TEST"

	t.Setenv(key, "not-an-int")
	if got := envIntOrDefault(key, 1234); got != 1234 {
		t.Fatalf("expected fallback for invalid int, got %d", got)
	}

	t.Setenv(key, "0")
	if got := envIntOrDefault(key, 1234); got != 1234 {
		t.Fatalf("expected fallback for zero int, got %d", got)
	}

	t.Setenv(key, "-10")
	if got := envIntOrDefault(key, 1234); got != 1234 {
		t.Fatalf("expected fallback for negative int, got %d", got)
	}
}

func TestEnvIntInRangeOrDefaultOutOfRange(t *testing.T) {
	const key = "FRUGAL_RANGE_INT_TEST"

	t.Setenv(key, "2048")
	if got := envIntInRangeOrDefault(key, 4096, 1024, 8192); got != 2048 {
		t.Fatalf("expected in-range value, got %d", got)
	}

	t.Setenv(key, "100")
	if got := envIntInRangeOrDefault(key, 4096, 1024, 8192); got != 4096 {
		t.Fatalf("expected fallback for below-min int, got %d", got)
	}

	t.Setenv(key, "9999999")
	if got := envIntInRangeOrDefault(key, 4096, 1024, 8192); got != 4096 {
		t.Fatalf("expected fallback for above-max int, got %d", got)
	}
}
