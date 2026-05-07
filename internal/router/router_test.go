package router

import (
	"testing"

	"github.com/frugalsh/frugal/internal/types"
)

func testModels() []ModelEntry {
	return []ModelEntry{
		{
			Name: "cheap-model", Provider: "providerA",
			CostPer1KInput: 0.0001, CostPer1KOutput: 0.0004,
			Reasoning: 0.65, Coding: 0.60, Creative: 0.60, InstructFollowing: 0.68,
			ToolUse: true, JSONMode: true, MaxContext: 128000,
		},
		{
			Name: "mid-model", Provider: "providerB",
			CostPer1KInput: 0.001, CostPer1KOutput: 0.004,
			Reasoning: 0.78, Coding: 0.75, Creative: 0.72, InstructFollowing: 0.80,
			ToolUse: true, JSONMode: true, MaxContext: 200000,
		},
		{
			Name: "premium-model", Provider: "providerC",
			CostPer1KInput: 0.003, CostPer1KOutput: 0.015,
			Reasoning: 0.95, Coding: 0.93, Creative: 0.90, InstructFollowing: 0.95,
			ToolUse: true, JSONMode: true, MaxContext: 200000,
		},
		{
			Name: "no-tools-model", Provider: "providerA",
			CostPer1KInput: 0.00005, CostPer1KOutput: 0.0002,
			Reasoning: 0.60, Coding: 0.55, Creative: 0.55, InstructFollowing: 0.60,
			ToolUse: false, JSONMode: false, MaxContext: 32000,
		},
	}
}

func testThresholds() map[string]Threshold {
	return map[string]Threshold{
		"high": {
			MinReasoning: 0.88, MinCoding: 0.85,
			MinCreative: 0.82, MinInstructFollowing: 0.88,
		},
		"balanced": {
			MinReasoning: 0.70, MinCoding: 0.68,
			MinCreative: 0.65, MinInstructFollowing: 0.72,
		},
		"cost": {
			MinReasoning: 0.0, MinCoding: 0.0,
			MinCreative: 0.0, MinInstructFollowing: 0.0,
		},
	}
}

func TestRoute_CostQuality_SelectsCheapest(t *testing.T) {
	r := New(testModels(), testThresholds())

	features := types.QueryFeatures{
		EstimatedInputTokens:  100,
		EstimatedOutputTokens: 100,
	}

	d := r.Route(features, types.QualityCost, nil)

	if d.SelectedModel != "no-tools-model" {
		t.Errorf("expected cheapest model for cost quality, got %s", d.SelectedModel)
	}
}

func TestRoute_HighQuality_SelectsPremium(t *testing.T) {
	r := New(testModels(), testThresholds())

	features := types.QueryFeatures{
		EstimatedInputTokens:  100,
		EstimatedOutputTokens: 100,
	}

	d := r.Route(features, types.QualityHigh, nil)

	if d.SelectedModel != "premium-model" {
		t.Errorf("expected premium model for high quality, got %s", d.SelectedModel)
	}
}

func TestRoute_BalancedQuality_SelectsMid(t *testing.T) {
	r := New(testModels(), testThresholds())

	features := types.QueryFeatures{
		EstimatedInputTokens:  100,
		EstimatedOutputTokens: 100,
	}

	d := r.Route(features, types.QualityBalanced, nil)

	// Should select cheapest model that meets balanced thresholds
	// mid-model and premium-model both qualify; mid-model is cheaper
	if d.SelectedModel != "mid-model" {
		t.Errorf("expected mid-model for balanced quality, got %s", d.SelectedModel)
	}
}

func TestRoute_ToolUseRequired_FiltersModels(t *testing.T) {
	r := New(testModels(), testThresholds())

	features := types.QueryFeatures{
		EstimatedInputTokens:  100,
		EstimatedOutputTokens: 100,
		RequiresToolUse:       true,
	}

	d := r.Route(features, types.QualityCost, nil)

	if d.SelectedModel == "no-tools-model" {
		t.Error("should not select model without tool_use when tools are required")
	}
}

func TestRoute_LargeContext_FiltersSmallModels(t *testing.T) {
	r := New(testModels(), testThresholds())

	features := types.QueryFeatures{
		EstimatedInputTokens:  50000, // exceeds no-tools-model's 32k context
		EstimatedOutputTokens: 100,
	}

	d := r.Route(features, types.QualityCost, nil)

	if d.SelectedModel == "no-tools-model" {
		t.Error("should not select model with insufficient context window")
	}
}

func TestRoute_CodingDomain_UsesCodingScore(t *testing.T) {
	r := New(testModels(), testThresholds())

	features := types.QueryFeatures{
		EstimatedInputTokens:  100,
		EstimatedOutputTokens: 100,
		HasCode:               true,
		DomainHints:           []string{"coding"},
	}

	d := r.Route(features, types.QualityHigh, nil)

	// High quality coding should route to premium-model (coding=0.93)
	if d.SelectedModel != "premium-model" {
		t.Errorf("expected premium-model for high quality coding, got %s", d.SelectedModel)
	}
}

func TestRoute_NoModels_ReturnsEmpty(t *testing.T) {
	r := New(nil, testThresholds())

	features := types.QueryFeatures{
		EstimatedInputTokens:  100,
		EstimatedOutputTokens: 100,
	}

	d := r.Route(features, types.QualityBalanced, nil)

	if d.SelectedModel != "" {
		t.Errorf("expected empty model when no models available, got %s", d.SelectedModel)
	}
}

func TestRoute_UnknownQualityDefaultsToBalancedThreshold(t *testing.T) {
	r := New(testModels(), testThresholds())

	features := types.QueryFeatures{
		EstimatedInputTokens:  100,
		EstimatedOutputTokens: 100,
	}

	d := r.Route(features, types.QualityThreshold("unknown"), nil)

	if d.SelectedModel != "mid-model" {
		t.Errorf("expected balanced fallback model mid-model for unknown quality, got %s", d.SelectedModel)
	}
}

func TestRoute_MissingRequestedThresholdFallsBackToBalanced(t *testing.T) {
	thresholds := testThresholds()
	delete(thresholds, "high")
	r := New(testModels(), thresholds)

	features := types.QueryFeatures{
		EstimatedInputTokens:  100,
		EstimatedOutputTokens: 100,
	}

	d := r.Route(features, types.QualityHigh, nil)

	if d.SelectedModel != "mid-model" {
		t.Errorf("expected balanced fallback model mid-model when high threshold missing, got %s", d.SelectedModel)
	}
}

func TestSatisfiesTier(t *testing.T) {
	r := New(testModels(), testThresholds())
	features := types.QueryFeatures{EstimatedInputTokens: 100, EstimatedOutputTokens: 100}

	cases := []struct {
		model, tier string
		want        bool
	}{
		// premium-model meets every tier.
		{"premium-model", "cost", true},
		{"premium-model", "balanced", true},
		{"premium-model", "high", true},
		// mid-model meets cost & balanced but not high.
		{"mid-model", "cost", true},
		{"mid-model", "balanced", true},
		{"mid-model", "high", false},
		// cheap-model meets only cost (its capabilities are below balanced).
		{"cheap-model", "cost", true},
		{"cheap-model", "balanced", false},
		{"cheap-model", "high", false},
		// Unknown model and tier both return false.
		{"does-not-exist", "balanced", false},
		{"premium-model", "ultra", false},
	}
	for _, c := range cases {
		if got := r.SatisfiesTier(c.model, c.tier, features); got != c.want {
			t.Errorf("SatisfiesTier(%q, %q) = %v, want %v", c.model, c.tier, got, c.want)
		}
	}
}
