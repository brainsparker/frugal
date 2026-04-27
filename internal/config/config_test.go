package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_StarterModelsYAMLLoads(t *testing.T) {
	// The in-tree starter config should parse cleanly with no unknown fields.
	// Clear FRUGAL_CONFIG so a stale installer-side config doesn't override
	// the in-tree path under test.
	t.Setenv("FRUGAL_CONFIG", "")
	path := filepath.Join("..", "..", "config", "models.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("starter models.yaml not present: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.SearchProviders["youcom"]; !ok {
		t.Errorf("expected 'youcom' in SearchProviders, got %+v", cfg.SearchProviders)
	}
	if _, ok := cfg.SearchProviders["serper"]; !ok {
		t.Errorf("expected 'serper' in SearchProviders, got %+v", cfg.SearchProviders)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	t.Setenv("FRUGAL_CONFIG", "")
	if _, err := Load("/does/not/exist.yaml"); err == nil {
		t.Fatalf("expected error for missing file")
	}
}

func TestValidate_RejectsNegativeCost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	yaml := `search_providers:
  bad:
    api_key_env: X
    cost_per_call: -0.01
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatalf("expected validation error for negative cost_per_call")
	}
}

func TestValidate_RejectsMissingAPIKeyAndBaseURL(t *testing.T) {
	// Either an api_key_env (hosted) or a base_url / base_url_env
	// (self-hosted) is required — without one we have no way to dispatch.
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	yaml := `search_providers:
  bad:
    cost_per_call: 0.001
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatalf("expected validation error for missing api_key_env and base_url")
	}
}

func TestLoad_RejectsUnknownTopLevelField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	yaml := `search_providers:
  serper:
    api_key_env: X
    cost_per_call: 0.001
mystery_field: oops
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatalf("expected error for unknown top-level field")
	}
}

func TestLoad_UsesTrimmedFrugalConfigEnv(t *testing.T) {
	content := `
providers:
  openai:
    api_key_env: OPENAI_API_KEY
    models:
      gpt-4o:
        cost_per_1k_input: 0.0025
        cost_per_1k_output: 0.01
        capabilities:
          reasoning: 0.95
          coding: 0.92
          creative: 0.90
          instruction_following: 0.95
          max_context: 128000
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FRUGAL_CONFIG", "  "+path+"  ")

	_, err := Load("/definitely/not/used.yaml")
	if err != nil {
		t.Fatalf("expected trimmed FRUGAL_CONFIG to load, got error: %v", err)
	}
}

func TestLoad_RejectsMultipleYAMLDocuments(t *testing.T) {
	content := `
providers:
  openai:
    api_key_env: OPENAI_API_KEY
    base_url: https://api.openai.com/v1
    models:
      gpt-4o:
        cost_per_1k_input: 0.0025
        cost_per_1k_output: 0.01
        capabilities:
          reasoning: 0.95
          coding: 0.92
          creative: 0.90
          instruction_following: 0.95
          tool_use: true
          json_mode: true
          max_context: 128000
quality_thresholds:
  balanced:
    min_reasoning: 0.70
    min_coding: 0.68
    min_creative: 0.65
    min_instruction_following: 0.72
---
providers: {}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for multiple YAML documents")
	}
	if !strings.Contains(err.Error(), "single YAML document") {
		t.Fatalf("expected single-document error, got: %v", err)
	}
}
