package classifier

import (
	"encoding/json"
	"testing"

	"github.com/frugalsh/frugal/internal/types"
)

func msg(role, content string) types.Message {
	return types.Message{
		Role:    role,
		Content: mustMarshal(content),
	}
}

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func TestClassify_SimpleQuestion(t *testing.T) {
	c := NewRuleBased()
	req := &types.ChatCompletionRequest{
		Messages: []types.Message{msg("user", "What is the capital of France?")},
	}

	f := c.Classify(req)

	if f.HasCode {
		t.Error("expected HasCode=false for simple question")
	}
	if f.HasMath {
		t.Error("expected HasMath=false for simple question")
	}
	if f.ComplexityScore > 0.3 {
		t.Errorf("expected low complexity, got %f", f.ComplexityScore)
	}
	if f.ConversationTurns != 1 {
		t.Errorf("expected 1 turn, got %d", f.ConversationTurns)
	}
}

func TestClassify_CodeRequest(t *testing.T) {
	c := NewRuleBased()
	req := &types.ChatCompletionRequest{
		Messages: []types.Message{
			msg("user", "Write a function to sort an array:\n```python\ndef sort_array(arr):\n    pass\n```"),
		},
	}

	f := c.Classify(req)

	if !f.HasCode {
		t.Error("expected HasCode=true for code request")
	}
	if f.ComplexityScore < 0.2 {
		t.Errorf("expected higher complexity for code, got %f", f.ComplexityScore)
	}
	if len(f.DomainHints) == 0 {
		t.Error("expected domain hints for code request")
	}
}

func TestClassify_MathRequest(t *testing.T) {
	c := NewRuleBased()
	req := &types.ChatCompletionRequest{
		Messages: []types.Message{
			msg("user", "Solve the equation $x^2 + 3x - 4 = 0$ using the quadratic formula"),
		},
	}

	f := c.Classify(req)

	if !f.HasMath {
		t.Error("expected HasMath=true")
	}
}

func TestClassify_WithSystemPrompt(t *testing.T) {
	c := NewRuleBased()
	longSystem := "You are a helpful assistant. " // short
	req := &types.ChatCompletionRequest{
		Messages: []types.Message{
			msg("system", longSystem),
			msg("user", "Hello"),
		},
	}

	f := c.Classify(req)

	if !f.HasSystemPrompt {
		t.Error("expected HasSystemPrompt=true")
	}
	if f.SystemPromptLength == 0 {
		t.Error("expected non-zero SystemPromptLength")
	}
}

func TestClassify_ToolUse(t *testing.T) {
	c := NewRuleBased()
	req := &types.ChatCompletionRequest{
		Messages: []types.Message{msg("user", "Get the weather")},
		Tools: []types.Tool{
			{Type: "function", Function: types.ToolFunction{Name: "get_weather"}},
		},
	}

	f := c.Classify(req)

	if !f.RequiresToolUse {
		t.Error("expected RequiresToolUse=true")
	}
	if f.ComplexityScore < 0.1 {
		t.Errorf("expected complexity bump from tool use, got %f", f.ComplexityScore)
	}
}

func TestClassify_JSONOutput(t *testing.T) {
	c := NewRuleBased()
	req := &types.ChatCompletionRequest{
		Messages:       []types.Message{msg("user", "Extract entities")},
		ResponseFormat: &types.ResponseFormat{Type: "json_object"},
	}

	f := c.Classify(req)

	if !f.RequiresJSON {
		t.Error("expected RequiresJSON=true")
	}
}

func TestClassify_MultiTurn(t *testing.T) {
	c := NewRuleBased()
	req := &types.ChatCompletionRequest{
		Messages: []types.Message{
			msg("user", "Hello"),
			msg("assistant", "Hi there"),
			msg("user", "How are you?"),
			msg("assistant", "I'm good"),
			msg("user", "What about the weather?"),
			msg("assistant", "It's sunny"),
			msg("user", "Thanks"),
			msg("assistant", "You're welcome"),
			msg("user", "One more thing"),
		},
	}

	f := c.Classify(req)

	if f.ConversationTurns < 5 {
		t.Errorf("expected 5+ turns, got %d", f.ConversationTurns)
	}
	if f.ComplexityScore < 0.1 {
		t.Errorf("expected complexity bump from multi-turn, got %f", f.ComplexityScore)
	}
}

func TestComplexity_Clamped(t *testing.T) {
	// Create features that would exceed 1.0
	f := types.QueryFeatures{
		HasCode:               true,
		HasMath:               true,
		HasSystemPrompt:       true,
		SystemPromptLength:    1000,
		RequiresToolUse:       true,
		RequiresJSON:          true,
		EstimatedInputTokens:  5000,
		ConversationTurns:     10,
	}

	score := computeComplexity(f)
	if score > 1.0 {
		t.Errorf("complexity should be clamped to 1.0, got %f", score)
	}
}
