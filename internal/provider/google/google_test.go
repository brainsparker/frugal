package google

import (
	"encoding/json"
	"testing"

	"github.com/frugalsh/frugal/internal/types"
)

func TestTranslateRequest_PassesToolsThrough(t *testing.T) {
	params := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
	req := &types.ChatCompletionRequest{
		Model: "gemini-2.5-pro",
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"search the web"`)},
		},
		Tools: []types.Tool{
			{Type: "function", Function: types.ToolFunction{
				Name: "web_search", Description: "search", Parameters: params,
			}},
		},
	}

	gr := translateRequest(req)

	if len(gr.Tools) != 1 {
		t.Fatalf("expected 1 Gemini tool entry, got %d", len(gr.Tools))
	}
	decls := gr.Tools[0].FunctionDeclarations
	if len(decls) != 1 || decls[0].Name != "web_search" {
		t.Fatalf("expected web_search declaration, got %+v", decls)
	}
	if string(decls[0].Parameters) != string(params) {
		t.Fatalf("parameters not preserved: got %s", string(decls[0].Parameters))
	}
}

func TestTranslateRequest_MultimodalImage_ProducesInlineData(t *testing.T) {
	req := &types.ChatCompletionRequest{
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`[
				{"type":"text","text":"what is this"},
				{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}
			]`)},
		},
	}

	gr := translateRequest(req)

	if len(gr.Contents) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(gr.Contents))
	}
	parts := gr.Contents[0].Parts
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %+v", len(parts), parts)
	}
	if parts[0].Text != "what is this" {
		t.Fatalf("first part text = %q", parts[0].Text)
	}
	if parts[1].InlineData == nil {
		t.Fatalf("second part missing inlineData: %+v", parts[1])
	}
	if parts[1].InlineData.MimeType != "image/png" || parts[1].InlineData.Data != "AAAA" {
		t.Fatalf("inlineData mis-translated: %+v", parts[1].InlineData)
	}
}

func TestTranslateResponse_FunctionCall_MapsToToolCalls(t *testing.T) {
	gr := &generateContentResponse{
		Candidates: []candidate{
			{
				Content: geminiContent{
					Parts: []geminiPart{
						{FunctionCall: &geminiFuncCall{
							Name: "web_search",
							Args: json.RawMessage(`{"q":"go"}`),
						}},
					},
				},
				FinishReason: "STOP",
			},
		},
	}

	resp := translateResponse(gr, "gemini-2.5-pro")

	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	choice := resp.Choices[0]
	if choice.FinishReason == nil || *choice.FinishReason != "tool_calls" {
		t.Fatalf("expected finish_reason=tool_calls, got %v", choice.FinishReason)
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %+v", choice.Message.ToolCalls)
	}
	tc := choice.Message.ToolCalls[0]
	if tc.Function.Name != "web_search" || tc.Function.Arguments != `{"q":"go"}` {
		t.Fatalf("tool call mis-translated: %+v", tc)
	}
}
