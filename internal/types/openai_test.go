package types

import (
	"encoding/json"
	"testing"
)

func TestContentText_StringContent(t *testing.T) {
	m := Message{Role: "user", Content: json.RawMessage(`"hello world"`)}
	if got := m.ContentText(); got != "hello world" {
		t.Fatalf("ContentText = %q, want %q", got, "hello world")
	}
	if m.HasNonTextContent() {
		t.Fatalf("HasNonTextContent = true for string content")
	}
}

func TestContentText_ArrayContent_JoinsTextParts(t *testing.T) {
	m := Message{
		Role: "user",
		Content: json.RawMessage(`[
			{"type":"text","text":"describe this"},
			{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}},
			{"type":"text","text":" please"}
		]`),
	}
	if got := m.ContentText(); got != "describe this please" {
		t.Fatalf("ContentText = %q, want %q", got, "describe this please")
	}
	if !m.HasNonTextContent() {
		t.Fatalf("HasNonTextContent = false; expected true for array with image part")
	}
}

func TestContentParts_PreservesImageURL(t *testing.T) {
	m := Message{
		Role:    "user",
		Content: json.RawMessage(`[{"type":"image_url","image_url":{"url":"https://example.com/i.png","detail":"low"}}]`),
	}
	parts := m.ContentParts()
	if len(parts) != 1 || parts[0].ImageURL == nil {
		t.Fatalf("expected 1 image part with ImageURL set, got %#v", parts)
	}
	if parts[0].ImageURL.URL != "https://example.com/i.png" || parts[0].ImageURL.Detail != "low" {
		t.Fatalf("unexpected image URL: %+v", parts[0].ImageURL)
	}
}

func TestContentText_EmptyContent(t *testing.T) {
	m := Message{Role: "user"}
	if got := m.ContentText(); got != "" {
		t.Fatalf("ContentText = %q, want empty", got)
	}
	if m.HasNonTextContent() {
		t.Fatalf("HasNonTextContent = true for empty content")
	}
}
