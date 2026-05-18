package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	chatErr   error
	streamErr error

	mu              sync.Mutex
	chatCalls       int
	streamCalls     int
	lastChatModel   string
	lastStreamModel string
}

func (m *mockProvider) Name() string    { return m.name }
func (m *mockProvider) Models() []string { return m.models }

func (m *mockProvider) ChatCompletion(ctx context.Context, model string, req *types.ChatCompletionRequest) (*types.ChatCompletionResponse, error) {
	m.mu.Lock()
	m.chatCalls++
	m.lastChatModel = model
	m.mu.Unlock()

	if m.chatErr != nil {
		return nil, m.chatErr
	}

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
	m.mu.Lock()
	m.streamCalls++
	m.lastStreamModel = model
	m.mu.Unlock()

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

func (m *mockProvider) ChatCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.chatCalls
}

func (m *mockProvider) LastChatModel() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastChatModel
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
			ToolUse: true, JSONMode: true, Vision: true, MaxContext: 200000,
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

func TestChatCompletions_AcceptsUnknownFields(t *testing.T) {
	// Real OpenAI SDKs send fields Frugal does not explicitly model
	// (parallel_tool_calls, seed, reasoning_effort, service_tier, etc.).
	// The proxy must accept them so the "no code changes" promise holds.
	_, ts := setupHandler()
	defer ts.Close()

	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hello"}],"unexpected":true,"seed":42,"parallel_tool_calls":false}`)
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}
}

func TestChatCompletions_RejectsOversizedBody(t *testing.T) {
	_, ts := setupHandler()
	defer ts.Close()

	oversized := bytes.Repeat([]byte("a"), int(maxChatCompletionsBodyBytes)+1)
	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"`)
	body = append(body, oversized...)
	body = append(body, []byte(`"}]}`)...)

	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, string(b))
	}

	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !strings.Contains(payload.Error.Message, "request body exceeds") {
		t.Fatalf("expected stable oversized body message, got %q", payload.Error.Message)
	}
}

func TestChatCompletions_RelaxedFromHeader_EmittedWhenDowngraded(t *testing.T) {
	// Force a downgrade: no model clears the "high" threshold for a simple
	// English prompt (both mock models have reasoning below the high bar
	// of 0.88 on non-coding/non-math queries, except mock-premium at 0.95).
	// The premium model does clear it, so we instead construct a setup that
	// cannot clear high but can clear balanced.
	reg := newStubMockRegistry()
	models := []router.ModelEntry{
		{
			Name: "cheap", Provider: "mock",
			CostPer1KInput: 0.001, CostPer1KOutput: 0.002,
			Reasoning: 0.70, Coding: 0.70, Creative: 0.70, InstructFollowing: 0.70,
			ToolUse: true, JSONMode: true, MaxContext: 100000,
		},
	}
	thresholds := map[string]router.Threshold{
		"high":     {MinReasoning: 0.99, MinCoding: 0.99, MinCreative: 0.99, MinInstructFollowing: 0.99},
		"balanced": {MinReasoning: 0.60, MinCoding: 0.60, MinCreative: 0.60, MinInstructFollowing: 0.60},
		"cost":     {},
	}
	cls := classifier.NewRuleBased()
	rtr := router.New(models, thresholds)
	h := NewHandler(cls, rtr, reg)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", h.ChatCompletions)
	ts := httptest.NewServer(HeaderExtractionMiddleware(mux))
	defer ts.Close()

	body, _ := json.Marshal(types.ChatCompletionRequest{
		Model:    "auto",
		Messages: []types.Message{{Role: "user", Content: mustMarshalJSON("Hello")}},
	})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Frugal-Quality", "high")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Frugal-Relaxed-From"); got != "high" {
		t.Fatalf("expected X-Frugal-Relaxed-From=high, got %q", got)
	}
}

// newStubMockRegistry returns a registry with a single mock model used when
// the default setupHandler wiring is too opinionated for a specific test.
func newStubMockRegistry() *provider.Registry {
	reg := provider.NewRegistry()
	mock := &mockProvider{name: "mock", models: []string{"cheap"}}
	reg.Register(mock)
	return reg
}

func TestChatCompletions_MultimodalContent_IsAccepted(t *testing.T) {
	// Router accepts array-typed Content and forwards the request. The
	// classifier must still see a meaningful text feature via ContentText;
	// the provider must still be invoked.
	_, ts := setupHandler()
	defer ts.Close()

	body := []byte(`{"model":"auto","messages":[{"role":"user","content":[
		{"type":"text","text":"what is in this picture"},
		{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}
	]}]}`)
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 for multimodal request, got %d: %s", resp.StatusCode, string(b))
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
			ID      string `json:"id"`
			Created int64  `json:"created"`
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
	if len(result.Data) > 1 {
		created := result.Data[0].Created
		for i := 1; i < len(result.Data); i++ {
			if result.Data[i].Created != created {
				t.Fatalf("expected deterministic created timestamps across model list, got %d and %d", created, result.Data[i].Created)
			}
		}
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

func TestChatCompletions_FallbackAttemptsAreBounded(t *testing.T) {
	reg := provider.NewRegistry()

	failing := &mockProvider{name: "failing", models: []string{"primary", "fb1", "fb2", "fb3", "fb4"}, chatErr: errors.New("boom")}
	reg.Register(failing)

	models := []router.ModelEntry{{
		Name: "primary", Provider: "failing",
		CostPer1KInput: 0.0001, CostPer1KOutput: 0.0002,
		Reasoning: 0.8, Coding: 0.8, Creative: 0.8, InstructFollowing: 0.8,
		ToolUse: true, JSONMode: true, MaxContext: 128000,
	}}
	thresholds := map[string]router.Threshold{
		"balanced": {MinReasoning: 0.1, MinCoding: 0.1, MinCreative: 0.1, MinInstructFollowing: 0.1},
	}

	h := NewHandler(classifier.NewRuleBased(), router.New(models, thresholds), reg)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", h.ChatCompletions)
	ts := httptest.NewServer(HeaderExtractionMiddleware(mux))
	defer ts.Close()

	body, _ := json.Marshal(types.ChatCompletionRequest{
		Model:    "auto",
		Messages: []types.Message{{Role: "user", Content: mustMarshalJSON("Hello")}},
	})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Frugal-Fallback", "fb1,fb2,fb3,fb4")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 502, got %d: %s", resp.StatusCode, string(b))
	}

	// 1 primary attempt + maxFallbackAttempts fallbacks.
	wantCalls := 1 + maxFallbackAttempts
	if got := failing.ChatCallCount(); got != wantCalls {
		t.Fatalf("expected %d total attempts, got %d", wantCalls, got)
	}

	if got := failing.LastChatModel(); got != "fb3" {
		t.Fatalf("expected last attempted fallback model fb3, got %s", got)
	}
}

func TestBoundedFallbacks_SkipsSelectedModelAndDuplicates(t *testing.T) {
	// nil allowed => no allowlist, so all non-empty deduped entries pass.
	got := boundedFallbacks(context.Background(), []string{" gpt-4o ", "gpt-4o", "", "claude-3-5-sonnet", "CLAUDE-3-5-SONNET", "gemini-2.5-flash"}, "gpt-4o", nil)
	want := []string{"claude-3-5-sonnet", "gemini-2.5-flash"}

	if len(got) != len(want) {
		t.Fatalf("expected %d fallbacks, got %d (%v)", len(want), len(got), got)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected fallback %d = %s, got %s", i, want[i], got[i])
		}
	}
}

func TestBoundedFallbacks_AllowlistRejectsUnknownModels(t *testing.T) {
	allowed := map[string]string{"mock-cheap": "mock-cheap", "mock-premium": "mock-premium"}
	got := boundedFallbacks(context.Background(), []string{"mock-cheap", "claude-opus", "mock-premium"}, "", allowed)
	want := []string{"mock-cheap", "mock-premium"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry %d: expected %s, got %s", i, want[i], got[i])
		}
	}
}

func TestBoundedFallbacks_DedupesBeforeApplyingAttemptLimit(t *testing.T) {
	got := boundedFallbacks(context.Background(), []string{"a", "a", "b", "c", "d"}, "", nil)
	want := []string{"a", "b", "c"}

	if len(got) != len(want) {
		t.Fatalf("expected %d fallbacks, got %d (%v)", len(want), len(got), got)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected fallback %d = %s, got %s", i, want[i], got[i])
		}
	}
}

func TestBoundedFallbacks_AllowlistIsCaseInsensitiveAndCanonicalizes(t *testing.T) {
	allowed := map[string]string{"mock-cheap": "mock-cheap", "mock-premium": "mock-premium"}
	got := boundedFallbacks(context.Background(), []string{"MOCK-CHEAP", "Mock-Premium"}, "", allowed)
	want := []string{"mock-cheap", "mock-premium"}

	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry %d: expected %s, got %s", i, want[i], got[i])
		}
	}
}
