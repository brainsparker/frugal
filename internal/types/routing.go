package types

import "strings"

// QualityThreshold controls how aggressively Frugal routes to cheaper models.
type QualityThreshold string

const (
	QualityHigh     QualityThreshold = "high"
	QualityBalanced QualityThreshold = "balanced"
	QualityCost     QualityThreshold = "cost"
)

// ParseQualityThreshold parses a string into a QualityThreshold. It recognises
// high/balanced/cost (case- and whitespace-insensitive). Returns (value, true)
// on a known value and (QualityBalanced, false) on anything else so callers
// can distinguish "client sent a typo" from "client omitted the header".
func ParseQualityThreshold(s string) (QualityThreshold, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "high":
		return QualityHigh, true
	case "balanced":
		return QualityBalanced, true
	case "cost":
		return QualityCost, true
	default:
		return QualityBalanced, false
	}
}

// QueryFeatures is the output of the classifier's feature extraction.
type QueryFeatures struct {
	EstimatedInputTokens        int      `json:"estimated_input_tokens"`
	EstimatedOutputTokens       int      `json:"estimated_output_tokens"`
	HasCode                     bool     `json:"has_code"`
	HasMath                     bool     `json:"has_math"`
	HasSystemPrompt             bool     `json:"has_system_prompt"`
	SystemPromptLength          int      `json:"system_prompt_length"`
	ConversationTurns           int      `json:"conversation_turns"`
	RequiresJSON                bool     `json:"requires_json"`
	RequiresToolUse             bool     `json:"requires_tool_use"`
	RequiresVision              bool     `json:"requires_vision"`
	RequiresMultipleCompletions bool     `json:"requires_multiple_completions"`
	DomainHints                 []string `json:"domain_hints"`
	ComplexityScore             float64  `json:"complexity_score"` // 0.0 - 1.0
}

// RoutingDecision captures why a particular model was chosen.
type RoutingDecision struct {
	SelectedModel    string        `json:"selected_model"`
	SelectedProvider string        `json:"selected_provider"`
	Quality          string        `json:"quality_threshold"`
	// RelaxedFrom is set when no model met the requested threshold and the
	// router fell through to a lower tier. Clients can alarm on it instead
	// of treating degraded routing as a clean win.
	RelaxedFrom      string        `json:"relaxed_from,omitempty"`
	Features         QueryFeatures `json:"features"`
	Candidates       int           `json:"candidates_considered"`
	Reason           string        `json:"reason"`
	EstimatedCost    float64       `json:"estimated_cost"`
	FallbackChain    []string      `json:"fallback_chain,omitempty"`
	Pinned           bool          `json:"pinned"`
}
