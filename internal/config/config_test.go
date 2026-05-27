package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestValidate_RejectsWhitespaceOnlyProviderConfigFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	yaml := `search_providers:
  bad:
    api_key_env: "   "
    base_url: "  "
    base_url_env: "\t"
    cost_per_call: 0.001
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatalf("expected validation error for whitespace-only api_key_env/base_url/base_url_env")
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
search_providers:
  serper:
    api_key_env: SERPER_API_KEY
    cost_per_call: 0.001
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FRUGAL_CONFIG", "  "+path+"  ")

	cfg, err := Load("/definitely/not/used.yaml")
	if err != nil {
		t.Fatalf("expected trimmed FRUGAL_CONFIG to load, got error: %v", err)
	}
	if _, ok := cfg.SearchProviders["serper"]; !ok {
		t.Fatalf("expected provider loaded from trimmed FRUGAL_CONFIG, got %+v", cfg.SearchProviders)
	}
}

func TestLoad_RejectsMultipleYAMLDocuments(t *testing.T) {
	t.Setenv("FRUGAL_CONFIG", "")
	content := `
search_providers:
  serper:
    api_key_env: SERPER_API_KEY
    cost_per_call: 0.001
---
search_providers: {}
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
