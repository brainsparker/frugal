package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/frugalsh/frugal/internal/types"
)

func TestTranslateRequest_MultimodalImage_ProducesBase64Block(t *testing.T) {
	req := &types.ChatCompletionRequest{
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`[
				{"type":"text","text":"describe"},
				{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}
			]`)},
		},
	}

	ar := translateRequest(req, "claude-sonnet-4-20250514")

	if len(ar.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(ar.Messages))
	}
	blocks := ar.Messages[0].Content
	if len(blocks) != 2 {
		t.Fatalf("expected 2 content blocks, got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].Type != "text" || blocks[0].Text != "describe" {
		t.Fatalf("block 0 wrong: %+v", blocks[0])
	}
	if blocks[1].Type != "image" || blocks[1].Source == nil {
		t.Fatalf("block 1 missing image source: %+v", blocks[1])
	}
	src := blocks[1].Source
	if src.Type != "base64" || src.MediaType != "image/png" || src.Data != "AAAA" {
		t.Fatalf("base64 source mis-translated: %+v", src)
	}
}

func TestTranslateRequest_MultimodalImage_RemoteURL(t *testing.T) {
	req := &types.ChatCompletionRequest{
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`[{"type":"image_url","image_url":{"url":"https://example.com/i.png"}}]`)},
		},
	}

	ar := translateRequest(req, "claude-sonnet-4-20250514")

	if len(ar.Messages) != 1 || len(ar.Messages[0].Content) != 1 {
		t.Fatalf("expected single image block, got %+v", ar.Messages)
	}
	src := ar.Messages[0].Content[0].Source
	if src == nil || src.Type != "url" || src.URL != "https://example.com/i.png" {
		t.Fatalf("url source mis-translated: %+v", src)
	}
}

func TestTranslateRequest_PlainString_StillProducesTextBlock(t *testing.T) {
	req := &types.ChatCompletionRequest{
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"hello"`)},
		},
	}

	ar := translateRequest(req, "claude-sonnet-4-20250514")

	if len(ar.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(ar.Messages))
	}
	blocks := ar.Messages[0].Content
	if len(blocks) != 1 || blocks[0].Type != "text" || blocks[0].Text != "hello" {
		t.Fatalf("string content did not round-trip as text block: %+v", blocks)
	}
}
