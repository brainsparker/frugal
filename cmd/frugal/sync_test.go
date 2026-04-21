package main

import (
	"testing"

	msync "github.com/frugalsh/frugal/internal/sync"
)

// TestLookupModel_NoFuzzyCrossBinding guards the sync path against the
// regression that shipped in an earlier version: a strings.Contains fallback
// matched gpt-4 → gpt-4o, silently overwriting local pricing with a different
// model's cost. Ensure the lookup only succeeds on exact keys or explicit
// aliases.
func TestLookupModel_NoFuzzyCrossBinding(t *testing.T) {
	catalog := map[string]msync.ModelsDevEntry{
		"openai/gpt-4o": {ID: "gpt-4o"},
		"gpt-4o":        {ID: "gpt-4o"},
	}

	// Exact match should succeed.
	if _, ok := lookupModel(catalog, "openai", "gpt-4o"); !ok {
		t.Fatalf("expected exact match for openai/gpt-4o")
	}

	// A different model name must NOT pick up gpt-4o's entry via substring.
	if entry, ok := lookupModel(catalog, "openai", "gpt-4"); ok {
		t.Fatalf("unexpected fuzzy match: gpt-4 resolved to %+v", entry)
	}
	if entry, ok := lookupModel(catalog, "openai", "gpt-4o-mini"); ok {
		t.Fatalf("unexpected fuzzy match: gpt-4o-mini resolved to %+v", entry)
	}
}

func TestLookupModel_AliasesStillResolve(t *testing.T) {
	catalog := map[string]msync.ModelsDevEntry{
		"claude-3-5-haiku": {ID: "claude-3-5-haiku"},
	}

	entry, ok := lookupModel(catalog, "anthropic", "claude-haiku-3.5")
	if !ok {
		t.Fatalf("expected alias claude-haiku-3.5 → claude-3-5-haiku to resolve")
	}
	if entry.ID != "claude-3-5-haiku" {
		t.Fatalf("alias resolved to wrong entry: %+v", entry)
	}
}
