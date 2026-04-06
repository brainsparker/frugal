package router

import (
	"fmt"
	"sort"

	"github.com/frugalsh/frugal/internal/types"
)

// Router selects the cheapest model that meets quality requirements.
type Router struct {
	models     []ModelEntry
	thresholds map[string]Threshold
}

func New(models []ModelEntry, thresholds map[string]Threshold) *Router {
	return &Router{models: models, thresholds: thresholds}
}

// Route selects a model based on query features and quality threshold.
func (r *Router) Route(features types.QueryFeatures, quality types.QualityThreshold, fallbacks []string) types.RoutingDecision {
	threshold := r.thresholds[string(quality)]

	// Filter candidates
	var candidates []ModelEntry
	for _, m := range r.models {
		if !r.meetsRequirements(m, features, threshold) {
			continue
		}
		candidates = append(candidates, m)
	}

	if len(candidates) == 0 {
		// Fallback: relax threshold to balanced, then cost
		for _, fallbackQuality := range []string{"balanced", "cost"} {
			ft := r.thresholds[fallbackQuality]
			for _, m := range r.models {
				if r.meetsRequirements(m, features, ft) {
					candidates = append(candidates, m)
				}
			}
			if len(candidates) > 0 {
				break
			}
		}
	}

	if len(candidates) == 0 {
		// Last resort: pick the cheapest model that has hard requirements
		candidates = r.filterHardRequirements(features)
	}

	if len(candidates) == 0 {
		return types.RoutingDecision{
			Quality: string(quality),
			Features: features,
			Reason:  "no models available",
		}
	}

	// Sort by estimated cost (cheapest first)
	sort.Slice(candidates, func(i, j int) bool {
		return r.estimateCost(candidates[i], features) < r.estimateCost(candidates[j], features)
	})

	selected := candidates[0]
	return types.RoutingDecision{
		SelectedModel:    selected.Name,
		SelectedProvider: selected.Provider,
		Quality:          string(quality),
		Features:         features,
		Candidates:       len(candidates),
		Reason:           r.buildReason(selected, features, quality),
		EstimatedCost:    r.estimateCost(selected, features),
		FallbackChain:    fallbacks,
	}
}

func (r *Router) meetsRequirements(m ModelEntry, f types.QueryFeatures, t Threshold) bool {
	// Hard requirements
	if f.RequiresToolUse && !m.ToolUse {
		return false
	}
	if f.RequiresJSON && !m.JSONMode {
		return false
	}
	if f.EstimatedInputTokens > m.MaxContext {
		return false
	}

	// Capability thresholds based on dominant dimension
	score := r.dominantCapability(m, f)
	minScore := r.dominantThreshold(t, f)

	return score >= minScore
}

func (r *Router) filterHardRequirements(f types.QueryFeatures) []ModelEntry {
	var out []ModelEntry
	for _, m := range r.models {
		if f.RequiresToolUse && !m.ToolUse {
			continue
		}
		if f.RequiresJSON && !m.JSONMode {
			continue
		}
		if f.EstimatedInputTokens > m.MaxContext {
			continue
		}
		out = append(out, m)
	}
	return out
}

// dominantCapability returns the model's score on the most important dimension for this query.
func (r *Router) dominantCapability(m ModelEntry, f types.QueryFeatures) float64 {
	if f.HasCode || containsDomain(f, "coding") {
		return m.Coding
	}
	if f.HasMath || containsDomain(f, "math") {
		return m.Reasoning
	}
	if containsDomain(f, "creative") {
		return m.Creative
	}
	// Default: blend of reasoning and instruction following
	return (m.Reasoning + m.InstructFollowing) / 2
}

// dominantThreshold returns the minimum score for the most important dimension.
func (r *Router) dominantThreshold(t Threshold, f types.QueryFeatures) float64 {
	if f.HasCode || containsDomain(f, "coding") {
		return t.MinCoding
	}
	if f.HasMath || containsDomain(f, "math") {
		return t.MinReasoning
	}
	if containsDomain(f, "creative") {
		return t.MinCreative
	}
	return (t.MinReasoning + t.MinInstructFollowing) / 2
}

func (r *Router) estimateCost(m ModelEntry, f types.QueryFeatures) float64 {
	inputCost := float64(f.EstimatedInputTokens) / 1000 * m.CostPer1KInput
	outputCost := float64(f.EstimatedOutputTokens) / 1000 * m.CostPer1KOutput
	return inputCost + outputCost
}

func (r *Router) buildReason(m ModelEntry, f types.QueryFeatures, q types.QualityThreshold) string {
	dominant := "general"
	if f.HasCode || containsDomain(f, "coding") {
		dominant = "coding"
	} else if f.HasMath || containsDomain(f, "math") {
		dominant = "reasoning"
	} else if containsDomain(f, "creative") {
		dominant = "creative"
	}

	return fmt.Sprintf("selected %s (%s) as cheapest model meeting %s threshold for %s queries (complexity=%.2f)",
		m.Name, m.Provider, q, dominant, f.ComplexityScore)
}

func containsDomain(f types.QueryFeatures, domain string) bool {
	for _, d := range f.DomainHints {
		if d == domain {
			return true
		}
	}
	return false
}
