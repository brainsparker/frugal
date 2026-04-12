package types

import "strings"

// QualityThreshold controls how aggressively Frugal routes to cheaper models.
type QualityThreshold string

const (
	QualityHigh     QualityThreshold = "high"
	QualityBalanced QualityThreshold = "balanced"
	QualityCost     QualityThreshold = "cost"
)

// ParseQualityThreshold parses a string into a QualityThreshold, defaulting to balanced.
func ParseQualityThreshold(s string) QualityThreshold {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "high":
		return QualityHigh
	case "cost":
		return QualityCost
	default:
		return QualityBalanced
	}
}

// QueryFeatures is the output of the classifier's feature extraction.
type QueryFeatures struct {
	EstimatedInputTokens  int      `json:"estimated_input_tokens"`
	EstimatedOutputTokens int      `json:"estimated_output_tokens"`
	HasCode               bool     `json:"has_code"`
	HasMath               bool     `json:"has_math"`
	HasSystemPrompt       bool     `json:"has_system_prompt"`
	SystemPromptLength    int      `json:"system_prompt_length"`
	ConversationTurns     int      `json:"conversation_turns"`
	RequiresJSON          bool     `json:"requires_json"`
	RequiresToolUse       bool     `json:"requires_tool_use"`
	DomainHints           []string `json:"domain_hints"`
	ComplexityScore       float64  `json:"complexity_score"` // 0.0 - 1.0
}

// RoutingDecision captures why a particular model was chosen.
type RoutingDecision struct {
	SelectedModel    string        `json:"selected_model"`
	SelectedProvider string        `json:"selected_provider"`
	Quality          string        `json:"quality_threshold"`
	Features         QueryFeatures `json:"features"`
	Candidates       int           `json:"candidates_considered"`
	Reason           string        `json:"reason"`
	EstimatedCost    float64       `json:"estimated_cost"`
	FallbackChain    []string      `json:"fallback_chain,omitempty"`
	Pinned           bool          `json:"pinned"`
}
