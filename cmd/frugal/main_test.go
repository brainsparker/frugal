package main

import (
	"context"
	"testing"

	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/router"
	"github.com/frugalsh/frugal/internal/types"
)

type testProvider struct {
	name   string
	models []string
}

func (p *testProvider) Name() string     { return p.name }
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
