package usecase

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_StarterSetRegisters(t *testing.T) {
	// Resolve the in-tree starter directory so the test proves the shipped
	// config loads cleanly.
	dir := filepath.Join("..", "..", "config", "use_cases")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("starter use_cases dir not present: %v", err)
	}

	r, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := []string{"code-dev", "factual-qa", "research-synthesis", "structured-extraction"}
	got := r.IDs()
	if len(got) != len(want) {
		t.Fatalf("expected %d use cases, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ids[%d]: got %q want %q", i, got[i], want[i])
		}
	}

	// Every starter case must declare all three tiers with a chat model.
	for _, id := range want {
		uc, ok := r.Get(id)
		if !ok {
			t.Fatalf("expected %q to be registered", id)
		}
		for _, tier := range ValidTiers {
			b, ok := uc.Bundles[tier]
			if !ok {
				t.Errorf("%s: missing tier %q", id, tier)
				continue
			}
			if b.Chat == "" {
				t.Errorf("%s@%s: empty chat model", id, tier)
			}
		}
	}
}

func TestBundle_UnknownUseCaseReturnsFalse(t *testing.T) {
	r, err := Load(filepath.Join("..", "..", "config", "use_cases"))
	if err != nil {
		t.Skipf("starter dir not loadable: %v", err)
	}
	if _, ok := r.Bundle("does-not-exist", "balanced"); ok {
		t.Fatalf("expected false for unknown use case")
	}
}

func TestBundle_UnknownTierReturnsFalse(t *testing.T) {
	r, err := Load(filepath.Join("..", "..", "config", "use_cases"))
	if err != nil {
		t.Skipf("starter dir not loadable: %v", err)
	}
	if _, ok := r.Bundle("factual-qa", "premium"); ok {
		t.Fatalf("expected false for unknown tier")
	}
}

func TestLoad_EmptyDirNoError(t *testing.T) {
	// Running without any use-case configs should produce an empty registry,
	// not an error — use-case routing is opt-in.
	tmp := t.TempDir()
	r, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load empty dir: %v", err)
	}
	if r.Len() != 0 {
		t.Fatalf("expected empty registry, got %d entries", r.Len())
	}
}

func TestLoad_NonexistentDirNoError(t *testing.T) {
	r, err := Load("/this/does/not/exist/anywhere")
	if err != nil {
		t.Fatalf("expected no error for missing dir, got %v", err)
	}
	if r.Len() != 0 {
		t.Fatalf("expected empty registry, got %d entries", r.Len())
	}
}

func TestLoad_RejectsMissingTier(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte(`
id: bad
description: only-balanced-tier
source: curated
as_of: "2026-04-21"
confidence: low
bundles:
  balanced:
    chat: gpt-4o-mini
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(dir); err == nil {
		t.Fatalf("expected error when required tier is missing")
	}
}

func TestLoad_RejectsEmptyChatModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte(`
id: bad
description: empty-chat
source: curated
as_of: "2026-04-21"
confidence: low
bundles:
  high:
    chat: ""
  balanced:
    chat: gpt-4o-mini
  cost:
    chat: gpt-4o-mini
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(dir); err == nil {
		t.Fatalf("expected error when chat model is empty at any tier")
	}
}

func TestLoad_RejectsDuplicateID(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`
id: dup
description: d
source: curated
as_of: "2026-04-21"
confidence: low
bundles:
  high:    { chat: gpt-4o-mini }
  balanced:{ chat: gpt-4o-mini }
  cost:    { chat: gpt-4o-mini }
`)
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatalf("expected error on duplicate ids across files")
	}
}
