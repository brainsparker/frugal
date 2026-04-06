package provider

import (
	"context"

	"github.com/frugalsh/frugal/internal/types"
)

// StreamChunk represents one SSE event translated to OpenAI format.
type StreamChunk struct {
	Data *types.ChatCompletionChunk
	Err  error
	Done bool
}

// Provider translates between OpenAI-format requests and a specific LLM API.
type Provider interface {
	// Name returns the provider identifier (e.g., "openai", "anthropic", "google").
	Name() string

	// ChatCompletion sends a non-streaming request and returns the response in OpenAI format.
	ChatCompletion(ctx context.Context, model string, req *types.ChatCompletionRequest) (*types.ChatCompletionResponse, error)

	// ChatCompletionStream sends a streaming request and returns a channel of chunks in OpenAI SSE format.
	ChatCompletionStream(ctx context.Context, model string, req *types.ChatCompletionRequest) (<-chan StreamChunk, error)

	// Models returns the model names this provider supports.
	Models() []string
}
