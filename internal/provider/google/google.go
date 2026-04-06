package google

import (
	"bufio"
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
		client:  &http.Client{},
	}
}

func (p *Provider) Name() string { return "google" }

func (p *Provider) Models() []string { return p.models }

// -- Gemini API types --

type generateContentRequest struct {
	Contents          []geminiContent    `json:"contents"`
	SystemInstruction *geminiContent     `json:"systemInstruction,omitempty"`
	GenerationConfig  *generationConfig  `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text,omitempty"`
}

type generationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
	ResponseMimeType string  `json:"responseMimeType,omitempty"`
}

type generateContentResponse struct {
	Candidates    []candidate    `json:"candidates"`
	UsageMetadata *usageMetadata `json:"usageMetadata,omitempty"`
}

type candidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type usageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// -- Translation --

func translateRequest(req *types.ChatCompletionRequest) *generateContentRequest {
	gr := &generateContentRequest{}

	for _, msg := range req.Messages {
		if msg.Role == "system" {
			gr.SystemInstruction = &geminiContent{
				Parts: []geminiPart{{Text: msg.ContentString()}},
			}
			continue
		}

		role := "user"
		if msg.Role == "assistant" {
			role = "model"
		}
		gr.Contents = append(gr.Contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: msg.ContentString()}},
		})
	}

	gc := &generationConfig{}
	hasConfig := false
	if req.Temperature != nil {
		gc.Temperature = req.Temperature
		hasConfig = true
	}
	if req.TopP != nil {
		gc.TopP = req.TopP
		hasConfig = true
	}
	if req.MaxTokens != nil {
		gc.MaxOutputTokens = req.MaxTokens
		hasConfig = true
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_object" {
		gc.ResponseMimeType = "application/json"
		hasConfig = true
	}
	if hasConfig {
		gr.GenerationConfig = gc
	}

	return gr
}

func translateResponse(gr *generateContentResponse, model string) *types.ChatCompletionResponse {
	resp := &types.ChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-gemini-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
	}

	for i, cand := range gr.Candidates {
		content := ""
		for _, part := range cand.Content.Parts {
			content += part.Text
		}
		finishReason := mapFinishReason(cand.FinishReason)
		resp.Choices = append(resp.Choices, types.Choice{
			Index: i,
			Message: types.Message{
				Role:    "assistant",
				Content: mustMarshal(content),
			},
			FinishReason: &finishReason,
		})
	}

	if gr.UsageMetadata != nil {
		resp.Usage = &types.Usage{
			PromptTokens:     gr.UsageMetadata.PromptTokenCount,
			CompletionTokens: gr.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      gr.UsageMetadata.TotalTokenCount,
		}
	}

	return resp
}

func mapFinishReason(reason string) string {
	switch reason {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY":
		return "content_filter"
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
	gr := translateRequest(req)

	body, err := json.Marshal(gr)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", p.baseURL, model, p.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini error %d: %s", resp.StatusCode, string(respBody))
	}

	var result generateContentResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return translateResponse(&result, model), nil
}

func (p *Provider) ChatCompletionStream(ctx context.Context, model string, req *types.ChatCompletionRequest) (<-chan provider.StreamChunk, error) {
	gr := translateRequest(req)

	body, err := json.Marshal(gr)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s", p.baseURL, model, p.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini stream request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini error %d: %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan provider.StreamChunk, 8)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			var gr generateContentResponse
			if err := json.Unmarshal([]byte(data), &gr); err != nil {
				continue
			}

			for _, cand := range gr.Candidates {
				text := ""
				for _, part := range cand.Content.Parts {
					text += part.Text
				}
				ch <- provider.StreamChunk{
					Data: &types.ChatCompletionChunk{
						ID:      fmt.Sprintf("chatcmpl-gemini-%d", time.Now().UnixNano()),
						Object:  "chat.completion.chunk",
						Created: time.Now().Unix(),
						Model:   model,
						Choices: []types.ChunkChoice{
							{
								Index: 0,
								Delta: types.MessageDelta{Content: text},
							},
						},
					},
				}
			}
		}

		ch <- provider.StreamChunk{Done: true}
	}()

	return ch, nil
}
