package types

import (
	"encoding/json"
	"strings"
)

// ChatCompletionRequest is the OpenAI-compatible inbound request.
//
// Fields not used by Frugal's classifier or router are still decoded and
// forwarded verbatim to the upstream OpenAI provider so that clients using
// newer SDK features ("no code changes") continue to work. Anthropic and
// Google translators only read the subset of fields they can map.
type ChatCompletionRequest struct {
	Model                string          `json:"model"`
	Messages             []Message       `json:"messages"`
	Temperature          *float64        `json:"temperature,omitempty"`
	TopP                 *float64        `json:"top_p,omitempty"`
	N                    *int            `json:"n,omitempty"`
	Stream               bool            `json:"stream,omitempty"`
	StreamOptions        json.RawMessage `json:"stream_options,omitempty"`
	Stop                 json.RawMessage `json:"stop,omitempty"`
	MaxTokens            *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens  *int            `json:"max_completion_tokens,omitempty"`
	PresencePenalty      *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty     *float64        `json:"frequency_penalty,omitempty"`
	LogitBias            json.RawMessage `json:"logit_bias,omitempty"`
	LogProbs             *bool           `json:"logprobs,omitempty"`
	TopLogprobs          *int            `json:"top_logprobs,omitempty"`
	Seed                 *int            `json:"seed,omitempty"`
	User                 string          `json:"user,omitempty"`
	ResponseFormat       *ResponseFormat `json:"response_format,omitempty"`
	Tools                []Tool          `json:"tools,omitempty"`
	ToolChoice           json.RawMessage `json:"tool_choice,omitempty"`
	ParallelToolCalls    *bool           `json:"parallel_tool_calls,omitempty"`
	ReasoningEffort      *string         `json:"reasoning_effort,omitempty"`
	ServiceTier          *string         `json:"service_tier,omitempty"`
	Store                *bool           `json:"store,omitempty"`
	Metadata             json.RawMessage `json:"metadata,omitempty"`
	Modalities           []string        `json:"modalities,omitempty"`
	Prediction           json.RawMessage `json:"prediction,omitempty"`
	Audio                json.RawMessage `json:"audio,omitempty"`
}

type Message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// ContentPart mirrors an OpenAI message content element. Content may be either
// a plain string (classic) or an array of parts (multimodal, tool results).
// Unknown part fields are preserved via the Raw field so translators can
// forward them without loss. CacheControl holds an Anthropic-style hint
// ({"type":"ephemeral"}) that the Anthropic translator forwards verbatim.
type ContentPart struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	ImageURL     *ImageURL       `json:"image_url,omitempty"`
	InputAudio   json.RawMessage `json:"input_audio,omitempty"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
	Raw          json.RawMessage `json:"-"`
}

type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// ContentParts normalizes Content into a slice of parts. A string Content is
// returned as a single text part. Returns nil if Content is absent or opaque
// (e.g. structured tool-result objects that aren't arrays) — callers should
// fall back to the raw bytes in that case.
func (m *Message) ContentParts() []ContentPart {
	if len(m.Content) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return []ContentPart{{Type: "text", Text: s}}
	}
	var parts []ContentPart
	if err := json.Unmarshal(m.Content, &parts); err == nil {
		return parts
	}
	return nil
}

// ContentText joins the text of every text part in Content. Non-text parts
// (images, audio) are skipped. This is the right accessor for classifier
// feature extraction and for providers that do not support multimodal input.
func (m *Message) ContentText() string {
	parts := m.ContentParts()
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" || p.Type == "" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// HasNonTextContent reports whether Content includes any part that is not
// plain text (image_url, input_audio, tool results). Used by the router to
// exclude non-vision models.
func (m *Message) HasNonTextContent() bool {
	for _, p := range m.ContentParts() {
		if p.Type != "" && p.Type != "text" {
			return true
		}
	}
	return false
}

type ResponseFormat struct {
	Type string `json:"type"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

type Choice struct {
	Index        int      `json:"index"`
	Message      Message  `json:"message"`
	FinishReason *string  `json:"finish_reason,omitempty"`
}

type ChatCompletionChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []ChunkChoice `json:"choices"`
	Usage   *Usage        `json:"usage,omitempty"`
}

type ChunkChoice struct {
	Index        int          `json:"index"`
	Delta        MessageDelta `json:"delta"`
	FinishReason *string      `json:"finish_reason,omitempty"`
}

type MessageDelta struct {
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	ToolCalls []ToolCallDelta `json:"tool_calls,omitempty"`
}

type ToolCallDelta struct {
	Index    int               `json:"index"`
	ID       string            `json:"id,omitempty"`
	Type     string            `json:"type,omitempty"`
	Function *ToolCallFunction `json:"function,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
