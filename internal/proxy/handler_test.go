package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/classifier"
	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/router"
	"github.com/frugalsh/frugal/internal/types"
)

// mockProvider implements provider.Provider for testing.
type mockProvider struct {
	name      string
	models    []string
	response  *types.ChatCompletionResponse
	streamErr error
}

func (m *mockProvider) Name() string    { return m.name }
func (m *mockProvider) Models() []string { return m.models }

func (m *mockProvider) ChatCompletion(ctx context.Context, model string, req *types.ChatCompletionRequest) (*types.ChatCompletionResponse, error) {
	if m.response != nil {
		return m.response, nil
	}
	content, _ := json.Marshal("Hello from " + model)
	fr := "stop"
	return &types.ChatCompletionResponse{
		ID:      "chatcmpl-test",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []types.Choice{
			{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: content},
				FinishReason: &fr,
			},
		},
		Usage: &types.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}, nil
}

func (m *mockProvider) ChatCompletionStream(ctx context.Context, model string, req *types.ChatCompletionRequest) (<-chan provider.StreamChunk, error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}

	ch := make(chan provider.StreamChunk, 4)
	go func() {
		defer close(ch)
		for i, word := range []string{"Hello", " from", " stream"} {
			ch <- provider.StreamChunk{
				Data: &types.ChatCompletionChunk{
					ID:      "chatcmpl-stream",
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   model,
					Choices: []types.ChunkChoice{
						{Index: 0, Delta: types.MessageDelta{Content: word}},
					},
				},
			}
			_ = i
		}
		fr := "stop"
		ch <- provider.StreamChunk{
			Data: &types.ChatCompletionChunk{
				ID:      "chatcmpl-stream",
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   model,
				Choices: []types.ChunkChoice{
					{Index: 0, FinishReason: &fr},
				},
			},
		}
		ch <- provider.StreamChunk{Done: true}
	}()
	return ch, nil
}

func setupHandler() (*Handler, *httptest.Server) {
	reg := provider.NewRegistry()
	mock := &mockProvider{
		name:   "mock",
		models: []string{"mock-cheap", "mock-premium"},
	}
	reg.Register(mock)

	models := []router.ModelEntry{
		{
			Name: "mock-cheap", Provider: "mock",
			CostPer1KInput: 0.0001, CostPer1KOutput: 0.0004,
			Reasoning: 0.70, Coding: 0.68, Creative: 0.65, InstructFollowing: 0.72,
			ToolUse: true, JSONMode: true, MaxContext: 128000,
		},
		{
			Name: "mock-premium", Provider: "mock",
			CostPer1KInput: 0.003, CostPer1KOutput: 0.015,
			Reasoning: 0.95, Coding: 0.93, Creative: 0.90, InstructFollowing: 0.95,
			ToolUse: true, JSONMode: true, MaxContext: 200000,
		},
	}
	thresholds := map[string]router.Threshold{
		"high":     {MinReasoning: 0.88, MinCoding: 0.85, MinCreative: 0.82, MinInstructFollowing: 0.88},
		"balanced": {MinReasoning: 0.70, MinCoding: 0.68, MinCreative: 0.65, MinInstructFollowing: 0.72},
		"cost":     {},
	}

	cls := classifier.NewRuleBased()
	rtr := router.New(models, thresholds)
	h := NewHandler(cls, rtr, reg)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", h.ChatCompletions)
	mux.HandleFunc("GET /v1/models", h.ListModels)
	mux.HandleFunc("GET /v1/routing/explain", h.RoutingExplain)

	ts := httptest.NewServer(HeaderExtractionMiddleware(mux))
	return h, ts
}

func TestChatCompletions_NonStreaming(t *testing.T) {
	_, ts := setupHandler()
	defer ts.Close()

	body, _ := json.Marshal(types.ChatCompletionRequest{
		Model:    "auto",
		Messages: []types.Message{{Role: "user", Content: mustMarshalJSON("Hello")}},
	})

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}

	var result types.ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if result.ID == "" {
		t.Error("expected non-empty ID")
	}
	if len(result.Choices) == 0 {
		t.Error("expected at least one choice")
	}

	// Check routing headers
	if resp.Header.Get("X-Frugal-Model") == "" {
		t.Error("expected X-Frugal-Model header")
	}
}

func TestChatCompletions_Streaming(t *testing.T) {
	_, ts := setupHandler()
	defer ts.Close()

	body, _ := json.Marshal(types.ChatCompletionRequest{
		Model:    "auto",
		Messages: []types.Message{{Role: "user", Content: mustMarshalJSON("Hello")}},
		Stream:   true,
	})

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %s", ct)
	}

	// Read SSE events
	data, _ := io.ReadAll(resp.Body)
	body_str := string(data)

	if !strings.Contains(body_str, "data: ") {
		t.Error("expected SSE data lines")
	}
	if !strings.Contains(body_str, "data: [DONE]") {
		t.Error("expected [DONE] terminator")
	}
}

func TestChatCompletions_ModelPinning(t *testing.T) {
	_, ts := setupHandler()
	defer ts.Close()

	body, _ := json.Marshal(types.ChatCompletionRequest{
		Model:    "mock-premium",
		Messages: []types.Message{{Role: "user", Content: mustMarshalJSON("Hello")}},
	})

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("X-Frugal-Model") != "mock-premium" {
		t.Errorf("expected pinned model mock-premium, got %s", resp.Header.Get("X-Frugal-Model"))
	}
}

func TestChatCompletions_RequestBodyTooLarge(t *testing.T) {
	_, ts := setupHandler()
	defer ts.Close()

	bigPrompt := strings.Repeat("a", int(maxChatCompletionBodyBytes)+1024)
	body, _ := json.Marshal(types.ChatCompletionRequest{
		Model:    "auto",
		Messages: []types.Message{{Role: "user", Content: mustMarshalJSON(bigPrompt)}},
	})

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 413, got %d: %s", resp.StatusCode, string(b))
	}
}

func TestChatCompletions_QualityHeader(t *testing.T) {
	_, ts := setupHandler()
	defer ts.Close()

	body, _ := json.Marshal(types.ChatCompletionRequest{
		Model:    "auto",
		Messages: []types.Message{{Role: "user", Content: mustMarshalJSON("Hello")}},
	})

	// Request with cost quality — should route to cheaper model
	req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Frugal-Quality", "cost")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	model := resp.Header.Get("X-Frugal-Model")
	if model != "mock-cheap" {
		t.Errorf("expected mock-cheap for cost quality, got %s", model)
	}
}

func TestListModels(t *testing.T) {
	_, ts := setupHandler()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if result.Object != "list" {
		t.Errorf("expected object=list, got %s", result.Object)
	}
	// Should have mock-cheap, mock-premium, and auto
	if len(result.Data) < 3 {
		t.Errorf("expected at least 3 models, got %d", len(result.Data))
	}
}

func TestRoutingExplain_NoDecisions(t *testing.T) {
	_, ts := setupHandler()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/routing/explain")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 when no decisions, got %d", resp.StatusCode)
	}
}

func TestRoutingExplain_AfterRequest(t *testing.T) {
	_, ts := setupHandler()
	defer ts.Close()

	// Make a chat request first
	body, _ := json.Marshal(types.ChatCompletionRequest{
		Model:    "auto",
		Messages: []types.Message{{Role: "user", Content: mustMarshalJSON("Hello")}},
	})
	resp, _ := http.Post(ts.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	// Now check explain
	resp, err := http.Get(ts.URL + "/v1/routing/explain")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var decision types.RoutingDecision
	if err := json.NewDecoder(resp.Body).Decode(&decision); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if decision.SelectedModel == "" {
		t.Error("expected selected model in routing decision")
	}
}

func mustMarshalJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
