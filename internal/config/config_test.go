package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_StarterModelsYAMLLoads(t *testing.T) {
	// The in-tree starter config should parse cleanly with no unknown fields.
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
	if _, err := Load("/does/not/exist.yaml"); err == nil {
		t.Fatalf("expected error for missing file")
	}
}

func TestLoad_IgnoresFrugalConfigEnv(t *testing.T) {
	// Load is filesystem-pure: an ambient FRUGAL_CONFIG (e.g. from the
	// curl installer's rc block on a dev machine) must not hijack the
	// explicitly named path. Env resolution belongs to LoadAuto.
	other := filepath.Join(t.TempDir(), "other.yaml")
	if err := os.WriteFile(other, []byte("search_providers: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FRUGAL_CONFIG", other)
	if _, err := Load("/does/not/exist.yaml"); err == nil {
		t.Fatalf("Load must read the named path, not FRUGAL_CONFIG")
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

func TestLoadAuto_UsesTrimmedFrugalConfigEnv(t *testing.T) {
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

	cfg, src, err := LoadAuto()
	if err != nil {
		t.Fatalf("expected trimmed FRUGAL_CONFIG to load, got error: %v", err)
	}
	if !strings.Contains(src, path) {
		t.Errorf("source should name the env path; got %q", src)
	}
	if _, ok := cfg.SearchProviders["serper"]; !ok {
		t.Fatalf("expected provider loaded from trimmed FRUGAL_CONFIG, got %+v", cfg.SearchProviders)
	}
}

func TestLoadAuto_BadEnvPathErrors(t *testing.T) {
	// An explicit FRUGAL_CONFIG pointing nowhere is an operator mistake to
	// surface, not a case to paper over with the embedded default.
	t.Setenv("FRUGAL_CONFIG", "/does/not/exist.yaml")
	if _, _, err := LoadAuto(); err == nil {
		t.Fatalf("expected error for broken FRUGAL_CONFIG path")
	}
}

func TestLoadAuto_FallsBackToEmbeddedDefault(t *testing.T) {
	// No env, no checkout-relative file, no installer file → the embedded
	// default keeps a bare binary (npm wrapper, GUI-spawned) bootable.
	t.Setenv("FRUGAL_CONFIG", "")
	home := t.TempDir()
	t.Setenv("HOME", home)        // os.UserHomeDir on linux/darwin
	t.Setenv("USERPROFILE", home) // …and on windows
	t.Chdir(t.TempDir())

	cfg, src, err := LoadAuto()
	if err != nil {
		t.Fatalf("LoadAuto: %v", err)
	}
	if src != "embedded default" {
		t.Errorf("source = %q, want embedded default", src)
	}
	if _, ok := cfg.SearchProviders["marginalia"]; !ok {
		t.Errorf("embedded default should carry the zero-key marginalia provider; got %+v", cfg.SearchProviders)
	}
}

func TestLoadAuto_PrefersInstallerFileOverEmbedded(t *testing.T) {
	t.Setenv("FRUGAL_CONFIG", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(t.TempDir())

	dir := filepath.Join(home, ".frugal", "config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `
search_providers:
  serper:
    api_key_env: SERPER_API_KEY
    cost_per_call: 0.001
`
	path := filepath.Join(dir, "models.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, src, err := LoadAuto()
	if err != nil {
		t.Fatalf("LoadAuto: %v", err)
	}
	if src != path {
		t.Errorf("source = %q, want %q", src, path)
	}
	if len(cfg.SearchProviders) != 1 {
		t.Errorf("expected exactly the installer file's provider, got %+v", cfg.SearchProviders)
	}
}

func TestLoadTrusted_SkipsCwdConfig(t *testing.T) {
	// A config/models.yaml in whatever directory install runs from must
	// not get to choose which env vars are harvested into client configs.
	t.Setenv("FRUGAL_CONFIG", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	crafted := `
search_providers:
  serper:
    api_key_env: TOTALLY_NOT_A_PROVIDER_KEY
    base_url: https://attacker.example
    cost_per_call: 0.001
`
	if err := os.WriteFile(filepath.Join(cwd, "config", "models.yaml"), []byte(crafted), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	// LoadAuto trusts the cwd candidate; LoadTrusted must skip it.
	if _, src, err := LoadAuto(); err != nil || src != filepath.Join("config", "models.yaml") {
		t.Fatalf("precondition: LoadAuto should pick the cwd file; got src=%q err=%v", src, err)
	}
	cfg, src, err := LoadTrusted()
	if err != nil {
		t.Fatalf("LoadTrusted: %v", err)
	}
	if src != "embedded default" {
		t.Errorf("LoadTrusted source = %q, want embedded default", src)
	}
	if p, ok := cfg.SearchProviders["serper"]; ok && p.APIKeyEnv == "TOTALLY_NOT_A_PROVIDER_KEY" {
		t.Errorf("LoadTrusted must not consume the cwd config's env names")
	}
}

func TestLoad_RejectsMultipleYAMLDocuments(t *testing.T) {
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
