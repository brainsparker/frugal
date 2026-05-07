package eval

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Problem is one benchmark item: a prompt, optional system prompt, and one
// scorer selected from a small in-tree palette (see scorer.go). The deterministic
// scorer is the primary Pass gate; JudgeRubric, when set, opts the problem into
// an additive LLM-judge check that runs alongside without replacing it.
type Problem struct {
	ID        string `yaml:"id"`
	Prompt    string `yaml:"prompt"`
	System    string `yaml:"system,omitempty"`
	JSONMode  bool   `yaml:"json_mode,omitempty"`
	MaxTokens int    `yaml:"max_tokens,omitempty"`

	// Category groups problems for per-category reporting (factual / reasoning
	// / hybrid). Empty is allowed for back-compat and renders as "uncategorized".
	Category string `yaml:"category,omitempty"`

	// ToolUse declares whether the agent SHOULD invoke a tool/search for this
	// problem. Used to score tool-use accuracy. Defaults to "optional".
	ToolUse string `yaml:"tool_use,omitempty"`

	// JudgeRubric is the rubric handed to the LLM judge when --judge-model is
	// set. Empty disables the judge for this problem.
	JudgeRubric string `yaml:"judge_rubric,omitempty"`

	// ExpectedMinTier marks the lowest quality tier the chosen model should
	// satisfy for this problem. Used to score decision-correctness independent
	// of answer quality: a leg passes when its model meets this tier's
	// thresholds. Empty opts the problem out of the metric.
	ExpectedMinTier string `yaml:"expected_min_tier,omitempty"`

	// Exactly one of the Expected* fields should be set per problem. The YAML
	// loader infers the scorer type from whichever one is non-zero.
	ExpectedEquals      string   `yaml:"expected_equals,omitempty"`
	ExpectedContains    string   `yaml:"expected_contains,omitempty"`
	ExpectedContainsAll []string `yaml:"expected_contains_all,omitempty"`
	ExpectedKeys        []string `yaml:"expected_keys,omitempty"`
	ExpectedNumber      *float64 `yaml:"expected_number,omitempty"`

	CaseFold  bool    `yaml:"case_fold,omitempty"`
	Tolerance float64 `yaml:"tolerance,omitempty"`
}

// Tool-use expectation values. Anything else fails workload validation.
const (
	ToolUseRequired  = "required"
	ToolUseForbidden = "forbidden"
	ToolUseOptional  = "optional"
)

// Category values. Empty is allowed and treated as uncategorized.
const (
	CategoryFactual   = "factual"
	CategoryReasoning = "reasoning"
	CategoryHybrid    = "hybrid"
)

func validCategory(c string) bool {
	switch c {
	case "", CategoryFactual, CategoryReasoning, CategoryHybrid:
		return true
	}
	return false
}

func validToolUse(t string) bool {
	switch t {
	case "", ToolUseRequired, ToolUseForbidden, ToolUseOptional:
		return true
	}
	return false
}

func validTier(t string) bool {
	switch t {
	case "", "cost", "balanced", "high":
		return true
	}
	return false
}

// EffectiveToolUse returns ToolUse with empty defaulted to optional, so callers
// don't have to repeat the default everywhere.
func (p Problem) EffectiveToolUse() string {
	if p.ToolUse == "" {
		return ToolUseOptional
	}
	return p.ToolUse
}

// EffectiveCategory returns Category with empty replaced by "uncategorized" so
// the report has a stable bucket name to group on.
func (p Problem) EffectiveCategory() string {
	if p.Category == "" {
		return "uncategorized"
	}
	return p.Category
}

// Scorer builds the appropriate Scorer for this problem. Returns an error if
// zero or more than one Expected* field is set — workloads should fail loudly
// on malformed rows rather than silently scoring every response as passing.
func (p Problem) Scorer() (Scorer, error) {
	picks := 0
	if p.ExpectedEquals != "" {
		picks++
	}
	if p.ExpectedContains != "" {
		picks++
	}
	if len(p.ExpectedContainsAll) > 0 {
		picks++
	}
	if len(p.ExpectedKeys) > 0 {
		picks++
	}
	if p.ExpectedNumber != nil {
		picks++
	}
	if picks == 0 {
		return nil, fmt.Errorf("problem %q has no expected_* scorer field", p.ID)
	}
	if picks > 1 {
		return nil, fmt.Errorf("problem %q has multiple expected_* fields; pick one", p.ID)
	}

	switch {
	case p.ExpectedEquals != "":
		return ExactTrimmed{Expected: p.ExpectedEquals}, nil
	case p.ExpectedContains != "":
		return Substring{Expected: p.ExpectedContains, CaseFold: p.CaseFold}, nil
	case len(p.ExpectedContainsAll) > 0:
		return ContainsAll{Keywords: p.ExpectedContainsAll, CaseFold: p.CaseFold}, nil
	case len(p.ExpectedKeys) > 0:
		return JSONHasKeys{RequiredKeys: p.ExpectedKeys}, nil
	case p.ExpectedNumber != nil:
		return Numeric{Expected: *p.ExpectedNumber, Tolerance: p.Tolerance}, nil
	}
	return nil, fmt.Errorf("problem %q: unreachable scorer branch", p.ID)
}

// LiveWorkload is a YAML-authored set of benchmark problems. Distinct from
// Workload (simulation-only) so the benchmark harness can evolve its schema
// without breaking simulation consumers.
type LiveWorkload struct {
	Name        string    `yaml:"name"`
	Description string    `yaml:"description"`
	Baseline    string    `yaml:"baseline"`
	Problems    []Problem `yaml:"problems"`
}

// LoadLiveWorkload reads a YAML workload from disk. All scorers are validated
// up front so a bad row is caught before any API calls happen.
func LoadLiveWorkload(path string) (LiveWorkload, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return LiveWorkload{}, fmt.Errorf("read workload: %w", err)
	}
	var w LiveWorkload
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&w); err != nil {
		return LiveWorkload{}, fmt.Errorf("parse workload: %w", err)
	}
	if w.Name == "" {
		return LiveWorkload{}, fmt.Errorf("workload %q: missing name", path)
	}
	if w.Baseline == "" {
		return LiveWorkload{}, fmt.Errorf("workload %q: missing baseline model", path)
	}
	if len(w.Problems) == 0 {
		return LiveWorkload{}, fmt.Errorf("workload %q: no problems", path)
	}
	seen := map[string]bool{}
	for i, p := range w.Problems {
		if p.ID == "" {
			return LiveWorkload{}, fmt.Errorf("workload %q: problem %d missing id", path, i)
		}
		if seen[p.ID] {
			return LiveWorkload{}, fmt.Errorf("workload %q: duplicate problem id %q", path, p.ID)
		}
		seen[p.ID] = true
		if !validCategory(p.Category) {
			return LiveWorkload{}, fmt.Errorf("workload %q: problem %q: invalid category %q (want factual | reasoning | hybrid)", path, p.ID, p.Category)
		}
		if !validToolUse(p.ToolUse) {
			return LiveWorkload{}, fmt.Errorf("workload %q: problem %q: invalid tool_use %q (want required | forbidden | optional)", path, p.ID, p.ToolUse)
		}
		if !validTier(p.ExpectedMinTier) {
			return LiveWorkload{}, fmt.Errorf("workload %q: problem %q: invalid expected_min_tier %q (want cost | balanced | high)", path, p.ID, p.ExpectedMinTier)
		}
		if _, err := p.Scorer(); err != nil {
			return LiveWorkload{}, fmt.Errorf("workload %q: %w", path, err)
		}
	}
	return w, nil
}
