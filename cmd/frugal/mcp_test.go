package main

import (
	"testing"

	"github.com/frugalsh/frugal/internal/config"
)

func boolPtr(b bool) *bool { return &b }

func TestBuildSearchers_SkipsDisabledProviders(t *testing.T) {
	// `enabled: false` is the operator's opt-out from the config-load
	// default overlay: the entry stays in the config as a tombstone, but
	// must never register a driver. Marginalia and Wikipedia are keyless,
	// so absent the flag they always register.
	cfg := &config.Config{
		SearchProviders: map[string]config.SearchProviderConfig{
			"marginalia": {BaseURL: "https://api.marginalia.nu"},
			"wikipedia":  {BaseURL: "https://en.wikipedia.org", Enabled: boolPtr(false)},
		},
	}
	searchers := buildSearchers(cfg)
	names := make(map[string]bool, len(searchers))
	for _, s := range searchers {
		names[s.Name()] = true
	}
	if !names["marginalia"] {
		t.Errorf("marginalia should register (no enabled flag); got %v", names)
	}
	if names["wikipedia"] {
		t.Errorf("wikipedia is enabled: false and must not register; got %v", names)
	}
}

func TestBuildExtractors_SkipsDisabledProviders(t *testing.T) {
	cfg := &config.Config{
		ExtractProviders: map[string]config.SearchProviderConfig{
			"goreadability": {Enabled: boolPtr(false)},
		},
	}
	if got := buildExtractors(cfg); len(got) != 0 {
		t.Errorf("disabled goreadability must not register; got %d extractors", len(got))
	}
}
