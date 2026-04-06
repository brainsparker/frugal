package router

import "github.com/frugalsh/frugal/internal/config"

// ModelEntry is a routing-friendly view of a model from the config.
type ModelEntry struct {
	Name             string
	Provider         string
	CostPer1KInput   float64
	CostPer1KOutput  float64
	Reasoning        float64
	Coding           float64
	Creative         float64
	InstructFollowing float64
	ToolUse          bool
	JSONMode         bool
	MaxContext       int
}

// Threshold holds the minimum capability scores for a quality level.
type Threshold struct {
	MinReasoning        float64
	MinCoding           float64
	MinCreative         float64
	MinInstructFollowing float64
}

// BuildTaxonomy converts config data into routing-friendly structures.
func BuildTaxonomy(cfg *config.Config) ([]ModelEntry, map[string]Threshold) {
	var models []ModelEntry

	for providerName, pc := range cfg.Providers {
		for modelName, mc := range pc.Models {
			models = append(models, ModelEntry{
				Name:              modelName,
				Provider:          providerName,
				CostPer1KInput:    mc.CostPer1KInput,
				CostPer1KOutput:   mc.CostPer1KOutput,
				Reasoning:         mc.Capabilities.Reasoning,
				Coding:            mc.Capabilities.Coding,
				Creative:          mc.Capabilities.Creative,
				InstructFollowing: mc.Capabilities.InstructionFollowing,
				ToolUse:           mc.Capabilities.ToolUse,
				JSONMode:          mc.Capabilities.JSONMode,
				MaxContext:        mc.Capabilities.MaxContext,
			})
		}
	}

	thresholds := make(map[string]Threshold)
	for name, tc := range cfg.QualityThresholds {
		thresholds[name] = Threshold{
			MinReasoning:         tc.MinReasoning,
			MinCoding:            tc.MinCoding,
			MinCreative:          tc.MinCreative,
			MinInstructFollowing: tc.MinInstructionFollowing,
		}
	}

	return models, thresholds
}
