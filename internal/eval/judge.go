package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/types"
)

// Judge is an LLM-as-judge scorer. It runs alongside the deterministic scorer
// rather than replacing it: the deterministic Pass remains the primary gate so
// runs are reproducible without judge API access. Judge cost is tracked
// separately from agent cost so the report can split them cleanly.
type Judge struct {
	Model     string
	Provider  provider.Provider
	ModelCost ModelCost
}

// JudgeResult is one judgment. CostUSD is computed from the provider's Usage
// block at the judge model's per-token rates. Hallucination is a coarse
// signal: the judge marks it true when the answer contains specific factual
// claims that are unsupported by the prompt or commonly known facts. Use it
// as a rate across many problems, not as a per-problem ground truth.
type JudgeResult struct {
	Pass          bool
	Score         float64
	Reason        string
	Hallucination bool
	CostUSD       float64
}

const judgeSystemPrompt = `You are a strict grader. Given a prompt, an answer, and a rubric, decide whether the answer satisfies the rubric AND whether the answer contains hallucinations.

Reply with a single JSON object: {"pass": true|false, "score": 0-1, "reason": "<one sentence>", "contains_unsupported_claims": true|false}.

No markdown, no preamble. score is 0 for total failure, 1 for a flawless answer; pass is true only if score >= 0.7. Set contains_unsupported_claims=true when the answer asserts specific facts (names, numbers, citations) that are not supported by the prompt and are not common knowledge.`

// Evaluate calls the judge model with the prompt + agent answer + rubric and
// parses the JSON verdict. Errors from the upstream call or malformed JSON are
// surfaced — the caller decides how to render a missing judgment.
func (j *Judge) Evaluate(ctx context.Context, prompt, answer, rubric string) (JudgeResult, error) {
	if j == nil || j.Provider == nil || j.Model == "" {
		return JudgeResult{}, fmt.Errorf("judge: not configured")
	}

	user := fmt.Sprintf("Prompt:\n%s\n\nAnswer:\n%s\n\nRubric:\n%s", prompt, answer, rubric)
	sys, _ := json.Marshal(judgeSystemPrompt)
	usr, _ := json.Marshal(user)
	req := &types.ChatCompletionRequest{
		Model: j.Model,
		Messages: []types.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: usr},
		},
		ResponseFormat: &types.ResponseFormat{Type: "json_object"},
	}

	resp, err := j.Provider.ChatCompletion(ctx, j.Model, req)
	if err != nil {
		return JudgeResult{}, fmt.Errorf("judge call: %w", err)
	}
	if len(resp.Choices) == 0 {
		return JudgeResult{}, fmt.Errorf("judge: empty choices")
	}

	out := strings.TrimSpace(extractText(resp.Choices[0].Message.Content))
	out = stripJSONFence(out)

	var verdict struct {
		Pass          bool    `json:"pass"`
		Score         float64 `json:"score"`
		Reason        string  `json:"reason"`
		Hallucination bool    `json:"contains_unsupported_claims"`
	}
	if err := json.Unmarshal([]byte(out), &verdict); err != nil {
		return JudgeResult{}, fmt.Errorf("judge: parse verdict %q: %w", out, err)
	}

	r := JudgeResult{Pass: verdict.Pass, Score: verdict.Score, Reason: verdict.Reason, Hallucination: verdict.Hallucination}
	if resp.Usage != nil {
		r.CostUSD = float64(resp.Usage.PromptTokens)/1000*j.ModelCost.InputPer1K +
			float64(resp.Usage.CompletionTokens)/1000*j.ModelCost.OutputPer1K
	}
	return r, nil
}
