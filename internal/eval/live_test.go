package eval

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/classifier"
	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/router"
	"github.com/frugalsh/frugal/internal/types"
	"github.com/frugalsh/frugal/internal/usecase"
)

// mockProv returns a canned response per model so we can exercise both the
// Frugal and baseline legs deterministically without a network.
type mockProv struct {
	responses map[string]string
}

func (m *mockProv) Name() string { return "mock" }
func (m *mockProv) Models() []string {
	out := make([]string, 0, len(m.responses))
	for k := range m.responses {
		out = append(out, k)
	}
	return out
}
func (m *mockProv) ChatCompletion(ctx context.Context, model string, req *types.ChatCompletionRequest) (*types.ChatCompletionResponse, error) {
	text, ok := m.responses[model]
	if !ok {
		text = ""
	}
	content, _ := json.Marshal(text)
	fr := "stop"
	return &types.ChatCompletionResponse{
		ID:      "test-" + model,
		Object:  "chat.completion",
		Model:   model,
		Choices: []types.Choice{{Index: 0, Message: types.Message{Role: "assistant", Content: content}, FinishReason: &fr}},
		Usage:   &types.Usage{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25},
	}, nil
}
func (m *mockProv) ChatCompletionStream(ctx context.Context, model string, req *types.ChatCompletionRequest) (<-chan provider.StreamChunk, error) {
	return nil, nil
}

func TestLiveRunner_ScoresBothLegsAndComputesDelta(t *testing.T) {
	reg := provider.NewRegistry()
	// cheap answers wrong on math, right on classify; expensive is always right.
	reg.Register(&mockProv{responses: map[string]string{
		"cheap":     "positive",         // correct for classify; wrong for math (no number)
		"expensive": "The answer is 42", // contains 42 for math; wrong word for classify
	}})

	models := []router.ModelEntry{
		{
			Name: "cheap", Provider: "mock",
			CostPer1KInput: 0.0001, CostPer1KOutput: 0.0004,
			Reasoning: 0.5, Coding: 0.5, Creative: 0.5, InstructFollowing: 0.5,
			MaxContext: 10000,
		},
		{
			Name: "expensive", Provider: "mock",
			CostPer1KInput: 0.003, CostPer1KOutput: 0.015,
			Reasoning: 0.95, Coding: 0.95, Creative: 0.95, InstructFollowing: 0.95,
			MaxContext: 10000,
		},
	}
	thresholds := map[string]router.Threshold{
		"cost":     {},
		"balanced": {MinReasoning: 0.3, MinCoding: 0.3, MinCreative: 0.3, MinInstructFollowing: 0.3},
	}

	runner := &LiveRunner{
		Router:     router.New(models, thresholds),
		Classifier: classifier.NewRuleBased(),
		Registry:   reg,
		ModelCosts: map[string]ModelCost{
			"cheap":     {InputPer1K: 0.0001, OutputPer1K: 0.0004},
			"expensive": {InputPer1K: 0.003, OutputPer1K: 0.015},
		},
	}

	workload := LiveWorkload{
		Name:     "unit",
		Baseline: "expensive",
		Problems: []Problem{
			{ID: "classify-pos", Prompt: "is it nice?", ExpectedEquals: "positive"},
			{ID: "math-forty-two", Prompt: "What is 6*7?", ExpectedContains: "42"},
		},
	}

	s, err := runner.Run(context.Background(), workload, types.QualityCost)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if s.ProblemCount != 2 {
		t.Fatalf("want 2 problems, got %d", s.ProblemCount)
	}
	// At quality=cost, Frugal should prefer "cheap".
	if s.ModelBreakdown["cheap"] != 2 {
		t.Errorf("expected Frugal to pick cheap twice, breakdown=%v", s.ModelBreakdown)
	}
	// Baseline (expensive) contains "42" — correct on math; wrong on classify.
	// Cheap returns "positive" — right on classify, wrong on math.
	// Each leg scores 1/2 = 50%.
	if s.FrugalPassRate != 50.0 {
		t.Errorf("expected FrugalPassRate=50, got %.1f", s.FrugalPassRate)
	}
	if s.BaselinePassRate != 50.0 {
		t.Errorf("expected BaselinePassRate=50, got %.1f", s.BaselinePassRate)
	}
	// Costs computed from Usage × per-token rates.
	if s.BaselineCostUSD <= s.FrugalCostUSD {
		t.Errorf("expected baseline cost to exceed frugal cost; frugal=%.6f baseline=%.6f",
			s.FrugalCostUSD, s.BaselineCostUSD)
	}
	if s.SavingsPct <= 0 {
		t.Errorf("expected positive savings, got %.2f%%", s.SavingsPct)
	}
}

func TestToolUsePass(t *testing.T) {
	cases := []struct {
		expected string
		used     bool
		want     bool
	}{
		{ToolUseRequired, true, true},
		{ToolUseRequired, false, false},
		{ToolUseForbidden, false, true},
		{ToolUseForbidden, true, false},
		{ToolUseOptional, true, true},
		{ToolUseOptional, false, true},
		{"", true, true},
		{"", false, true},
	}
	for _, c := range cases {
		if got := toolUsePass(c.expected, c.used); got != c.want {
			t.Errorf("toolUsePass(%q, %v) = %v, want %v", c.expected, c.used, got, c.want)
		}
	}
}

func TestPercentiles(t *testing.T) {
	if p50, p95 := percentiles(nil); p50 != 0 || p95 != 0 {
		t.Errorf("empty percentiles: got %d/%d, want 0/0", p50, p95)
	}
	xs := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	p50, p95 := percentiles(xs)
	// idx for p50 = int(9*0.5) = 4 → cp[4] = 50
	// idx for p95 = int(9*0.95) = 8 → cp[8] = 90
	if p50 != 50 {
		t.Errorf("p50: got %d, want 50", p50)
	}
	if p95 != 90 {
		t.Errorf("p95: got %d, want 90", p95)
	}
	// Single element: both percentiles equal that element.
	one, _ := percentiles([]int64{42})
	if one != 42 {
		t.Errorf("single-element p50: got %d, want 42", one)
	}
}

func TestComputeCategoryStats_GroupsAndAverages(t *testing.T) {
	results := []LiveProblemResult{
		{Category: "factual", FrugalPass: true, BaselinePass: true, FrugalCostUSD: 0.001, BaselineCostUSD: 0.01, FrugalLatencyMS: 100, BaselineLatencyMS: 300},
		{Category: "factual", FrugalPass: false, BaselinePass: true, FrugalCostUSD: 0.001, BaselineCostUSD: 0.01, FrugalLatencyMS: 200, BaselineLatencyMS: 500},
		{Category: "reasoning", FrugalPass: true, BaselinePass: false, FrugalCostUSD: 0.002, BaselineCostUSD: 0.02, FrugalLatencyMS: 400, BaselineLatencyMS: 800},
	}
	stats := computeCategoryStats(results)
	if len(stats) != 2 {
		t.Fatalf("expected 2 categories, got %d", len(stats))
	}
	f := stats["factual"]
	if f.Count != 2 || f.FrugalPassRate != 50.0 || f.BaselinePassRate != 100.0 {
		t.Errorf("factual stats off: %+v", f)
	}
	if f.FrugalLatencyMS != 150 || f.BaselineLatencyMS != 400 {
		t.Errorf("factual latency averages off: frugal=%d baseline=%d", f.FrugalLatencyMS, f.BaselineLatencyMS)
	}
	r := stats["reasoning"]
	if r.Count != 1 || r.FrugalPassRate != 100.0 || r.BaselinePassRate != 0.0 {
		t.Errorf("reasoning stats off: %+v", r)
	}
}

// streamingMock supports both non-streaming and streaming. The streaming
// channel emits one chunk with content (so TTFT > 0), then a final Usage
// chunk, then closes. Used to exercise the streaming code path without a
// network.
type streamingMock struct {
	model     string
	content   string
	toolCalls []types.ToolCall
	usage     *types.Usage
	// recordedTools stores the tools attached to the last received request,
	// so tests can assert search-tool wiring.
	recordedTools []types.Tool
}

func (s *streamingMock) Name() string     { return "stream-mock" }
func (s *streamingMock) Models() []string { return []string{s.model} }
func (s *streamingMock) ChatCompletion(ctx context.Context, model string, req *types.ChatCompletionRequest) (*types.ChatCompletionResponse, error) {
	s.recordedTools = req.Tools
	content, _ := json.Marshal(s.content)
	return &types.ChatCompletionResponse{
		Model: model,
		Choices: []types.Choice{{
			Index:   0,
			Message: types.Message{Role: "assistant", Content: content, ToolCalls: s.toolCalls},
		}},
		Usage: s.usage,
	}, nil
}
func (s *streamingMock) ChatCompletionStream(ctx context.Context, model string, req *types.ChatCompletionRequest) (<-chan provider.StreamChunk, error) {
	s.recordedTools = req.Tools
	ch := make(chan provider.StreamChunk, 3)
	go func() {
		defer close(ch)
		// Real providers always have a measurable network round-trip before
		// the first chunk arrives. Sleep here so TTFT is non-zero in tests.
		time.Sleep(2 * time.Millisecond)
		ch <- provider.StreamChunk{Data: &types.ChatCompletionChunk{
			Model: model,
			Choices: []types.ChunkChoice{{
				Index: 0,
				Delta: types.MessageDelta{Role: "assistant", Content: s.content},
			}},
		}}
		if s.usage != nil {
			ch <- provider.StreamChunk{Data: &types.ChatCompletionChunk{
				Model: model,
				Usage: s.usage,
			}}
		}
		ch <- provider.StreamChunk{Done: true}
	}()
	return ch, nil
}

func TestLiveRunner_StreamingCapturesTTFTAndCost(t *testing.T) {
	mock := &streamingMock{
		model:   "stream-model",
		content: "the answer is 42",
		usage:   &types.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
	}
	reg := provider.NewRegistry()
	reg.Register(mock)

	models := []router.ModelEntry{{
		Name: "stream-model", Provider: "stream-mock",
		CostPer1KInput: 0.001, CostPer1KOutput: 0.004,
		Reasoning: 0.5, Coding: 0.5, Creative: 0.5, InstructFollowing: 0.5,
		MaxContext: 10000,
	}}
	thresholds := map[string]router.Threshold{"cost": {}}

	runner := &LiveRunner{
		Router:     router.New(models, thresholds),
		Classifier: classifier.NewRuleBased(),
		Registry:   reg,
		ModelCosts: map[string]ModelCost{"stream-model": {InputPer1K: 0.001, OutputPer1K: 0.004}},
		Stream:     true,
	}

	w := LiveWorkload{
		Name:     "stream-test",
		Baseline: "stream-model",
		Problems: []Problem{{ID: "p1", Prompt: "What is 6*7?", ExpectedContains: "42"}},
	}

	s, err := runner.Run(context.Background(), w, types.QualityCost)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !s.StreamingUsed {
		t.Errorf("expected StreamingUsed=true")
	}
	if s.FrugalTTFTP50MS == 0 {
		t.Errorf("expected non-zero TTFT p50 from streaming path")
	}
	if !s.Results[0].FrugalPass || !s.Results[0].BaselinePass {
		t.Errorf("expected pass on both legs (content streamed = %q)", mock.content)
	}
	wantCost := 100.0/1000*0.001 + 20.0/1000*0.004
	if abs(s.FrugalCostUSD-wantCost) > 1e-9 {
		t.Errorf("streaming cost: want %v, got %v", wantCost, s.FrugalCostUSD)
	}
}

func TestLiveRunner_AttachesSearchToolWhenBundleHasSearch(t *testing.T) {
	mock := &streamingMock{model: "m", content: "ok"}
	reg := provider.NewRegistry()
	reg.Register(mock)

	models := []router.ModelEntry{{
		Name: "m", Provider: "stream-mock", ToolUse: true,
		CostPer1KInput: 0.001, CostPer1KOutput: 0.001,
		Reasoning: 0.5, Coding: 0.5, Creative: 0.5, InstructFollowing: 0.5, MaxContext: 10000,
	}}
	runner := &LiveRunner{
		Router:     router.New(models, map[string]router.Threshold{"cost": {}}),
		Classifier: classifier.NewRuleBased(),
		Registry:   reg,
		ModelCosts: map[string]ModelCost{"m": {InputPer1K: 0.001, OutputPer1K: 0.001}},
		Bundle:     usecase.Bundle{Chat: "m", Search: "search-stub"},
	}

	w := LiveWorkload{
		Name: "tool-test", Baseline: "m",
		Problems: []Problem{{ID: "p1", Prompt: "anything", ExpectedContains: "ok"}},
	}
	if _, err := runner.Run(context.Background(), w, types.QualityCost); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(mock.recordedTools) == 0 {
		t.Fatalf("expected search tool attached when bundle.Search is set")
	}
	if mock.recordedTools[0].Function.Name != "search" {
		t.Errorf("expected tool name=search, got %q", mock.recordedTools[0].Function.Name)
	}
}

func TestLiveRunner_DecisionCorrectnessAggregates(t *testing.T) {
	// Only "premium-like" satisfies tier=high; "cheap-like" satisfies cost only.
	reg := provider.NewRegistry()
	reg.Register(&mockProv{responses: map[string]string{"cheap-like": "yes", "premium-like": "yes"}})

	models := []router.ModelEntry{
		{Name: "cheap-like", Provider: "mock",
			CostPer1KInput: 0.0001, CostPer1KOutput: 0.0001,
			Reasoning: 0.30, Coding: 0.30, Creative: 0.30, InstructFollowing: 0.30, MaxContext: 10000},
		{Name: "premium-like", Provider: "mock",
			CostPer1KInput: 0.01, CostPer1KOutput: 0.01,
			Reasoning: 0.95, Coding: 0.95, Creative: 0.95, InstructFollowing: 0.95, MaxContext: 10000},
	}
	thresholds := map[string]router.Threshold{
		"cost":     {},
		"balanced": {MinReasoning: 0.6, MinCoding: 0.6, MinCreative: 0.6, MinInstructFollowing: 0.6},
		"high":     {MinReasoning: 0.85, MinCoding: 0.85, MinCreative: 0.85, MinInstructFollowing: 0.85},
	}

	runner := &LiveRunner{
		Router:     router.New(models, thresholds),
		Classifier: classifier.NewRuleBased(),
		Registry:   reg,
		ModelCosts: map[string]ModelCost{"cheap-like": {}, "premium-like": {}},
	}

	// Frugal at quality=cost picks cheap-like; baseline pinned to premium-like.
	w := LiveWorkload{
		Name: "tier-test", Baseline: "premium-like",
		Problems: []Problem{
			{ID: "p1", Prompt: "y", ExpectedContains: "yes", ExpectedMinTier: "cost"},
			{ID: "p2", Prompt: "y", ExpectedContains: "yes", ExpectedMinTier: "high"},
		},
	}
	s, err := runner.Run(context.Background(), w, types.QualityCost)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.DecisionScored != 2 {
		t.Errorf("expected 2 decision-scored problems, got %d", s.DecisionScored)
	}
	// Frugal hits cost tier (always) but not high tier with cheap-like → 50%.
	if s.FrugalDecisionAccuracy != 50.0 {
		t.Errorf("frugal decision accuracy: want 50, got %v", s.FrugalDecisionAccuracy)
	}
	// Baseline (premium-like) hits both tiers → 100%.
	if s.BaselineDecisionAccuracy != 100.0 {
		t.Errorf("baseline decision accuracy: want 100, got %v", s.BaselineDecisionAccuracy)
	}
}

func TestLiveRunner_RejectsUnregisteredBaseline(t *testing.T) {
	runner := &LiveRunner{
		Registry:   provider.NewRegistry(),
		Classifier: classifier.NewRuleBased(),
		Router:     router.New(nil, nil),
	}
	_, err := runner.Run(context.Background(), LiveWorkload{Name: "x", Baseline: "nope", Problems: []Problem{{ID: "p1", Prompt: "hi", ExpectedContains: "hi"}}}, types.QualityCost)
	if err == nil {
		t.Fatalf("expected error when baseline is unregistered")
	}
}
