package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_ValidConfig(t *testing.T) {
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
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.Providers) != 1 {
		t.Errorf("expected 1 provider, got %d", len(cfg.Providers))
	}

	openai := cfg.Providers["openai"]
	if openai.APIKeyEnv != "OPENAI_API_KEY" {
		t.Errorf("expected OPENAI_API_KEY, got %s", openai.APIKeyEnv)
	}

	gpt4o := openai.Models["gpt-4o"]
	if gpt4o.CostPer1KInput != 0.0025 {
		t.Errorf("expected 0.0025, got %f", gpt4o.CostPer1KInput)
	}
	if gpt4o.Capabilities.Reasoning != 0.95 {
		t.Errorf("expected reasoning 0.95, got %f", gpt4o.Capabilities.Reasoning)
	}
	if !gpt4o.Capabilities.ToolUse {
		t.Error("expected tool_use=true")
	}
	if gpt4o.Capabilities.MaxContext != 128000 {
		t.Errorf("expected max_context 128000, got %d", gpt4o.Capabilities.MaxContext)
	}

	balanced := cfg.QualityThresholds["balanced"]
	if balanced.MinReasoning != 0.70 {
		t.Errorf("expected min_reasoning 0.70, got %f", balanced.MinReasoning)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}
