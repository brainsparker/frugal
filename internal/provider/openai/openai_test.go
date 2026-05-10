package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frugalsh/frugal/internal/types"
)

func TestChatCompletion_RejectsNonJSONContentType(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `<html><body>proxy error page</body></html>`)
	}))
	defer ts.Close()

	p := New("test-key", ts.URL, []string{"gpt-4o-mini"})
	p.client = ts.Client()

	_, err := p.ChatCompletion(context.Background(), "gpt-4o-mini", &types.ChatCompletionRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Content-Type must be application/json") {
		t.Fatalf("expected content-type validation error, got %v", err)
	}
}

func TestChatCompletion_AllowsJSONWithCharset(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer ts.Close()

	p := New("test-key", ts.URL, []string{"gpt-4o-mini"})
	p.client = ts.Client()

	resp, err := p.ChatCompletion(context.Background(), "gpt-4o-mini", &types.ChatCompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || len(resp.Choices) == 0 {
		t.Fatalf("expected response with choices, got %#v", resp)
	}
}
