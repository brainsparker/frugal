package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/types"
)

const errorBodyLimit = 8 << 10 // 8 KiB

func readErrorBody(r io.Reader) string {
	body, err := io.ReadAll(io.LimitReader(r, errorBodyLimit+1))
	if err != nil {
		return "<failed to read error body>"
	}
	if len(body) > errorBodyLimit {
		return string(body[:errorBodyLimit]) + "... (truncated)"
	}
	return string(body)
}

type Provider struct {
	apiKey  string
	baseURL string
	models  []string
	client  *http.Client
}

func New(apiKey, baseURL string, models []string) *Provider {
	return &Provider{
		apiKey:  apiKey,
		baseURL: baseURL,
		models:  models,
		client:  provider.NewHTTPClient(),
	}
}

func (p *Provider) Name() string { return "openai" }

func (p *Provider) Models() []string { return p.models }

func (p *Provider) ChatCompletion(ctx context.Context, model string, req *types.ChatCompletionRequest) (*types.ChatCompletionResponse, error) {
	reqCopy := *req
	reqCopy.Model = model
	reqCopy.Stream = false

	body, err := json.Marshal(reqCopy)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai error %d: %s", resp.StatusCode, readErrorBody(resp.Body))
	}

	var result types.ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &result, nil
}

func (p *Provider) ChatCompletionStream(ctx context.Context, model string, req *types.ChatCompletionRequest) (<-chan provider.StreamChunk, error) {
	reqCopy := *req
	reqCopy.Model = model
	reqCopy.Stream = true

	body, err := json.Marshal(reqCopy)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai stream request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, fmt.Errorf("openai error %d: %s", resp.StatusCode, readErrorBody(resp.Body))
	}

	ch := make(chan provider.StreamChunk, 8)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := provider.NewSSEScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				ch <- provider.StreamChunk{Done: true}
				return
			}

			var chunk types.ChatCompletionChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				ch <- provider.StreamChunk{Err: fmt.Errorf("decode chunk: %w", err)}
				return
			}
			ch <- provider.StreamChunk{Data: &chunk}
		}
		if err := scanner.Err(); err != nil {
			ch <- provider.StreamChunk{Err: err}
		}
	}()

	return ch, nil
}
