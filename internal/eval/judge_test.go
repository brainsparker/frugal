package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/types"
)

type judgeMock struct {
	body  string
	usage *types.Usage
	err   error
}

func (j *judgeMock) Name() string     { return "mock-judge" }
func (j *judgeMock) Models() []string { return []string{"judge-model"} }
func (j *judgeMock) ChatCompletion(ctx context.Context, model string, req *types.ChatCompletionRequest) (*types.ChatCompletionResponse, error) {
	if j.err != nil {
		return nil, j.err
	}
	content, _ := json.Marshal(j.body)
	return &types.ChatCompletionResponse{
		ID:      "j-" + model,
		Model:   model,
		Choices: []types.Choice{{Index: 0, Message: types.Message{Role: "assistant", Content: content}}},
		Usage:   j.usage,
	}, nil
}
func (j *judgeMock) ChatCompletionStream(ctx context.Context, model string, req *types.ChatCompletionRequest) (<-chan provider.StreamChunk, error) {
	return nil, nil
}

func TestJudge_ParsesVerdictAndComputesCost(t *testing.T) {
	body := `{"pass": true, "score": 0.85, "reason": "covers pivot and partition", "contains_unsupported_claims": false}`
	j := &Judge{
		Model:     "judge-model",
		Provider:  &judgeMock{body: body, usage: &types.Usage{PromptTokens: 200, CompletionTokens: 30}},
		ModelCost: ModelCost{InputPer1K: 0.01, OutputPer1K: 0.04},
	}
	r, err := j.Evaluate(context.Background(), "How does quicksort work?", "It picks a pivot and partitions.", "Must mention pivot AND partition.")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !r.Pass {
		t.Errorf("expected pass=true")
	}
	if r.Score != 0.85 {
		t.Errorf("expected score=0.85, got %v", r.Score)
	}
	if r.Hallucination {
		t.Errorf("expected hallucination=false")
	}
	wantCost := 200.0/1000*0.01 + 30.0/1000*0.04 // 0.002 + 0.0012 = 0.0032
	if abs(r.CostUSD-wantCost) > 1e-9 {
		t.Errorf("expected cost=%v, got %v", wantCost, r.CostUSD)
	}
}

func TestJudge_ParsesHallucinationFlag(t *testing.T) {
	body := `{"pass": false, "score": 0.3, "reason": "fabricated citation", "contains_unsupported_claims": true}`
	j := &Judge{
		Model:    "judge-model",
		Provider: &judgeMock{body: body},
	}
	r, err := j.Evaluate(context.Background(), "p", "a", "r")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !r.Hallucination {
		t.Errorf("expected hallucination=true")
	}
}

func TestJudge_ToleratesMarkdownFence(t *testing.T) {
	body := "```json\n{\"pass\": false, \"score\": 0.2, \"reason\": \"missing keyword\"}\n```"
	j := &Judge{
		Model:    "judge-model",
		Provider: &judgeMock{body: body},
	}
	r, err := j.Evaluate(context.Background(), "p", "a", "r")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if r.Pass {
		t.Errorf("expected pass=false")
	}
	if r.Score != 0.2 {
		t.Errorf("expected score=0.2, got %v", r.Score)
	}
}

func TestJudge_FailsOnMalformedJSON(t *testing.T) {
	j := &Judge{
		Model:    "judge-model",
		Provider: &judgeMock{body: "not json"},
	}
	if _, err := j.Evaluate(context.Background(), "p", "a", "r"); err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestJudge_PropagatesProviderError(t *testing.T) {
	j := &Judge{
		Model:    "judge-model",
		Provider: &judgeMock{err: fmt.Errorf("boom")},
	}
	if _, err := j.Evaluate(context.Background(), "p", "a", "r"); err == nil {
		t.Fatalf("expected provider error to propagate")
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
