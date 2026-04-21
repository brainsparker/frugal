package google

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

func (p *Provider) Name() string { return "google" }

func (p *Provider) Models() []string { return p.models }

// -- Gemini API types --

type generateContentRequest struct {
	Contents          []geminiContent   `json:"contents"`
	SystemInstruction *geminiContent    `json:"systemInstruction,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
	Tools             []geminiToolDecl  `json:"tools,omitempty"`
	// CachedContent points at a Gemini cached content resource. Forwarded
	// verbatim when the client supplies `frugal_cached_content` in their
	// Metadata so Gemini context caching works without Frugal stripping it.
	CachedContent string `json:"cachedContent,omitempty"`
}

type geminiToolDecl struct {
	FunctionDeclarations []geminiFuncDecl `json:"functionDeclarations"`
}

type geminiFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text         string            `json:"text,omitempty"`
	InlineData   *geminiInlineData `json:"inlineData,omitempty"`
	FileData     *geminiFileData   `json:"fileData,omitempty"`
	FunctionCall *geminiFuncCall   `json:"functionCall,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFileData struct {
	MimeType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri"`
}

type geminiFuncCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type generationConfig struct {
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"topP,omitempty"`
	MaxOutputTokens  *int     `json:"maxOutputTokens,omitempty"`
	StopSequences    []string `json:"stopSequences,omitempty"`
	ResponseMimeType string   `json:"responseMimeType,omitempty"`
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
				Parts: toGeminiParts(msg),
			}
			continue
		}

		role := "user"
		if msg.Role == "assistant" {
			role = "model"
		}
		gr.Contents = append(gr.Contents, geminiContent{
			Role:  role,
			Parts: toGeminiParts(msg),
		})
	}

	if tools := toGeminiTools(req.Tools); len(tools) > 0 {
		gr.Tools = tools
	}

	if cached := cachedContentFromMetadata(req.Metadata); cached != "" {
		gr.CachedContent = cached
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
	} else if req.MaxCompletionTokens != nil {
		gc.MaxOutputTokens = req.MaxCompletionTokens
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

// toGeminiParts converts OpenAI content parts to Gemini parts. Text parts map
// to {text}; image_url with a data URL maps to {inlineData}; image_url with a
// remote URL maps to {fileData}. Empty content produces a single empty-text
// part so the request remains valid.
func toGeminiParts(msg types.Message) []geminiPart {
	parts := msg.ContentParts()
	if len(parts) == 0 {
		return []geminiPart{{Text: ""}}
	}
	out := make([]geminiPart, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "", "text":
			out = append(out, geminiPart{Text: p.Text})
		case "image_url":
			if p.ImageURL == nil {
				continue
			}
			if strings.HasPrefix(p.ImageURL.URL, "data:") {
				if part := dataURLToInlinePart(p.ImageURL.URL); part != nil {
					out = append(out, *part)
				}
				continue
			}
			if strings.HasPrefix(p.ImageURL.URL, "http://") || strings.HasPrefix(p.ImageURL.URL, "https://") {
				out = append(out, geminiPart{FileData: &geminiFileData{FileURI: p.ImageURL.URL}})
			}
		}
	}
	if len(out) == 0 {
		return []geminiPart{{Text: ""}}
	}
	return out
}

// cachedContentFromMetadata reads a Gemini cached-content resource name from
// the request's OpenAI-style metadata field. Clients opt in with
// `metadata: {"frugal_cached_content": "projects/.../cachedContents/..."}`;
// absent or non-string values skip the field entirely.
func cachedContentFromMetadata(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	return m["frugal_cached_content"]
}

// toGeminiTools maps OpenAI tool declarations to a single Gemini tool entry
// containing all function declarations. Gemini supports exactly one tools[]
// element carrying many functionDeclarations.
func toGeminiTools(tools []types.Tool) []geminiToolDecl {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]geminiFuncDecl, 0, len(tools))
	for _, t := range tools {
		if t.Type != "" && t.Type != "function" {
			continue
		}
		decls = append(decls, geminiFuncDecl{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}
	if len(decls) == 0 {
		return nil
	}
	return []geminiToolDecl{{FunctionDeclarations: decls}}
}

func dataURLToInlinePart(url string) *geminiPart {
	rest := url[len("data:"):]
	semi := strings.Index(rest, ";")
	comma := strings.Index(rest, ",")
	if semi < 0 || comma < 0 || semi > comma {
		return nil
	}
	return &geminiPart{InlineData: &geminiInlineData{
		MimeType: rest[:semi],
		Data:     rest[comma+1:],
	}}
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
		var toolCalls []types.ToolCall
		for _, part := range cand.Content.Parts {
			if part.Text != "" {
				content += part.Text
			}
			if part.FunctionCall != nil {
				args := string(part.FunctionCall.Args)
				if args == "" {
					args = "{}"
				}
				toolCalls = append(toolCalls, types.ToolCall{
					ID:   fmt.Sprintf("call_%s_%d_%d", part.FunctionCall.Name, i, len(toolCalls)),
					Type: "function",
					Function: types.ToolCallFunction{
						Name:      part.FunctionCall.Name,
						Arguments: args,
					},
				})
			}
		}
		finishReason := mapFinishReason(cand.FinishReason)
		if len(toolCalls) > 0 {
			finishReason = "tool_calls"
		}
		resp.Choices = append(resp.Choices, types.Choice{
			Index: i,
			Message: types.Message{
				Role:      "assistant",
				Content:   mustMarshal(content),
				ToolCalls: toolCalls,
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
		return nil, fmt.Errorf("gemini error %d: %s", resp.StatusCode, readErrorBody(resp.Body))
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
		return nil, fmt.Errorf("gemini error %d: %s", resp.StatusCode, readErrorBody(resp.Body))
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

			var gr generateContentResponse
			if err := json.Unmarshal([]byte(data), &gr); err != nil {
				continue
			}

			for _, cand := range gr.Candidates {
				text := ""
				var toolDeltas []types.ToolCallDelta
				for _, part := range cand.Content.Parts {
					if part.Text != "" {
						text += part.Text
					}
					if part.FunctionCall != nil {
						args := string(part.FunctionCall.Args)
						if args == "" {
							args = "{}"
						}
						toolDeltas = append(toolDeltas, types.ToolCallDelta{
							Index: len(toolDeltas),
							ID:    fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, len(toolDeltas)),
							Type:  "function",
							Function: &types.ToolCallFunction{
								Name:      part.FunctionCall.Name,
								Arguments: args,
							},
						})
					}
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
								Delta: types.MessageDelta{
									Content:   text,
									ToolCalls: toolDeltas,
								},
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
