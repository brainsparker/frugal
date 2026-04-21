package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/types"
)

const anthropicVersion = "2023-06-01"

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

func (p *Provider) Name() string { return "anthropic" }

func (p *Provider) Models() []string { return p.models }

// -- Anthropic API types --

type messagesRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system,omitempty"`
	Messages  []anthropicMsg  `json:"messages"`
	Stream    bool            `json:"stream,omitempty"`
	Tools     []anthropicTool `json:"tools,omitempty"`
}

type anthropicMsg struct {
	Role    string              `json:"role"`
	Content []anthropicContent  `json:"content"`
}

// anthropicContent is Anthropic's content block. Emitted block types today:
// text, image, tool_result. tool_use for assistant-origin tool calls is
// handled by the upstream model; Frugal does not synthesize it.
type anthropicContent struct {
	Type      string           `json:"type"`
	Text      string           `json:"text,omitempty"`
	Source    *anthropicSource `json:"source,omitempty"`
	ToolUseID string           `json:"tool_use_id,omitempty"`
	Content   string           `json:"content,omitempty"` // tool_result body
	// CacheControl is forwarded verbatim so callers can opt into Anthropic
	// prompt caching without Frugal stripping the hint.
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

type anthropicSource struct {
	Type      string `json:"type"`                 // "base64" or "url"
	MediaType string `json:"media_type,omitempty"` // required for base64
	Data      string `json:"data,omitempty"`       // base64 payload
	URL       string `json:"url,omitempty"`        // for type:"url"
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type messagesResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []contentBlock `json:"content"`
	Model      string         `json:"model"`
	Usage      anthropicUsage `json:"usage"`
	StopReason string         `json:"stop_reason"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// -- Streaming event types --

type streamEvent struct {
	Type  string          `json:"type"`
	Delta json.RawMessage `json:"delta,omitempty"`
	Index int             `json:"index,omitempty"`
}

type textDelta struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type messageDelta struct {
	StopReason string `json:"stop_reason"`
}

// -- Translation --

func translateRequest(req *types.ChatCompletionRequest, model string) *messagesRequest {
	ar := &messagesRequest{
		Model:  model,
		Stream: req.Stream,
	}

	maxTokens := 4096
	if req.MaxTokens != nil {
		maxTokens = *req.MaxTokens
	} else if req.MaxCompletionTokens != nil {
		maxTokens = *req.MaxCompletionTokens
	}
	ar.MaxTokens = maxTokens

	for _, msg := range req.Messages {
		if msg.Role == "system" {
			ar.System = msg.ContentText()
			continue
		}

		// Tool results ride on a user message as a tool_result block so the
		// upstream model can correlate them to the prior tool_use. OpenAI
		// represents them as role="tool" with tool_call_id.
		if msg.Role == "tool" {
			ar.Messages = append(ar.Messages, anthropicMsg{
				Role: "user",
				Content: []anthropicContent{{
					Type:      "tool_result",
					ToolUseID: msg.ToolCallID,
					Content:   msg.ContentText(),
				}},
			})
			continue
		}

		ar.Messages = append(ar.Messages, anthropicMsg{
			Role:    msg.Role,
			Content: toAnthropicContent(msg),
		})
	}

	for _, tool := range req.Tools {
		ar.Tools = append(ar.Tools, anthropicTool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		})
	}

	return ar
}

// toAnthropicContent translates an OpenAI message into Anthropic's content
// block array. Text parts become {type:"text"}; image_url parts become
// {type:"image"} with either a base64 or url source depending on the input.
// Per-part cache_control hints forward verbatim so Anthropic prompt-caching
// works without Frugal stripping the marker.
func toAnthropicContent(msg types.Message) []anthropicContent {
	parts := msg.ContentParts()
	if len(parts) == 0 {
		return []anthropicContent{{Type: "text", Text: ""}}
	}
	out := make([]anthropicContent, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "", "text":
			out = append(out, anthropicContent{Type: "text", Text: p.Text, CacheControl: p.CacheControl})
		case "image_url":
			if p.ImageURL == nil {
				continue
			}
			src := imageURLToAnthropicSource(p.ImageURL.URL)
			if src == nil {
				continue
			}
			out = append(out, anthropicContent{Type: "image", Source: src, CacheControl: p.CacheControl})
		}
	}
	if len(out) == 0 {
		out = append(out, anthropicContent{Type: "text", Text: ""})
	}
	return out
}

func imageURLToAnthropicSource(url string) *anthropicSource {
	const dataPrefix = "data:"
	if strings.HasPrefix(url, dataPrefix) {
		// data:image/png;base64,<payload>
		rest := url[len(dataPrefix):]
		semi := strings.Index(rest, ";")
		comma := strings.Index(rest, ",")
		if semi < 0 || comma < 0 || semi > comma {
			return nil
		}
		media := rest[:semi]
		data := rest[comma+1:]
		return &anthropicSource{Type: "base64", MediaType: media, Data: data}
	}
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return &anthropicSource{Type: "url", URL: url}
	}
	return nil
}

func translateResponse(ar *messagesResponse) *types.ChatCompletionResponse {
	content := ""
	for _, block := range ar.Content {
		if block.Type == "text" {
			content += block.Text
		}
	}

	finishReason := mapStopReason(ar.StopReason)

	return &types.ChatCompletionResponse{
		ID:      ar.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   ar.Model,
		Choices: []types.Choice{
			{
				Index: 0,
				Message: types.Message{
					Role:    "assistant",
					Content: mustMarshal(content),
				},
				FinishReason: &finishReason,
			},
		},
		Usage: &types.Usage{
			PromptTokens:     ar.Usage.InputTokens,
			CompletionTokens: ar.Usage.OutputTokens,
			TotalTokens:      ar.Usage.InputTokens + ar.Usage.OutputTokens,
		},
	}
}

func mapStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// -- API calls --

func (p *Provider) ChatCompletion(ctx context.Context, model string, req *types.ChatCompletionRequest) (*types.ChatCompletionResponse, error) {
	ar := translateRequest(req, model)
	ar.Stream = false

	body, err := json.Marshal(ar)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic error %d: %s", resp.StatusCode, readErrorBody(resp.Body))
	}

	var result messagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return translateResponse(&result), nil
}

func (p *Provider) ChatCompletionStream(ctx context.Context, model string, req *types.ChatCompletionRequest) (<-chan provider.StreamChunk, error) {
	ar := translateRequest(req, model)
	ar.Stream = true

	body, err := json.Marshal(ar)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic stream request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, fmt.Errorf("anthropic error %d: %s", resp.StatusCode, readErrorBody(resp.Body))
	}

	ch := make(chan provider.StreamChunk, 8)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		chunkID := fmt.Sprintf("chatcmpl-%s", ar.Model)
		scanner := provider.NewSSEScanner(resp.Body)

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			var event streamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			switch event.Type {
			case "content_block_delta":
				var td textDelta
				if err := json.Unmarshal(event.Delta, &td); err != nil {
					continue
				}
				ch <- provider.StreamChunk{
					Data: &types.ChatCompletionChunk{
						ID:      chunkID,
						Object:  "chat.completion.chunk",
						Created: time.Now().Unix(),
						Model:   model,
						Choices: []types.ChunkChoice{
							{
								Index: 0,
								Delta: types.MessageDelta{Content: td.Text},
							},
						},
					},
				}

			case "message_delta":
				var md messageDelta
				if err := json.Unmarshal(event.Delta, &md); err != nil {
					continue
				}
				finishReason := mapStopReason(md.StopReason)
				ch <- provider.StreamChunk{
					Data: &types.ChatCompletionChunk{
						ID:      chunkID,
						Object:  "chat.completion.chunk",
						Created: time.Now().Unix(),
						Model:   model,
						Choices: []types.ChunkChoice{
							{
								Index:        0,
								FinishReason: &finishReason,
							},
						},
					},
				}

			case "message_stop":
				ch <- provider.StreamChunk{Done: true}
				return
			}
		}
	}()

	return ch, nil
}
