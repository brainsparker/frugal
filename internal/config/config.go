package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Providers         map[string]ProviderConfig  `yaml:"providers"`
	QualityThresholds map[string]ThresholdConfig `yaml:"quality_thresholds"`
}

type ProviderConfig struct {
	APIKeyEnv string                 `yaml:"api_key_env"`
	BaseURL   string                 `yaml:"base_url"`
	Models    map[string]ModelConfig `yaml:"models"`
}

type ModelConfig struct {
	CostPer1KInput  float64          `yaml:"cost_per_1k_input"`
	CostPer1KOutput float64          `yaml:"cost_per_1k_output"`
	Capabilities    CapabilityConfig `yaml:"capabilities"`
}

type CapabilityConfig struct {
	Reasoning            float64 `yaml:"reasoning"`
	Coding               float64 `yaml:"coding"`
	Creative             float64 `yaml:"creative"`
	InstructionFollowing float64 `yaml:"instruction_following"`
	ToolUse              bool    `yaml:"tool_use"`
	JSONMode             bool    `yaml:"json_mode"`
	MaxContext           int     `yaml:"max_context"`
}

type ThresholdConfig struct {
	MinReasoning            float64 `yaml:"min_reasoning"`
	MinCoding               float64 `yaml:"min_coding"`
	MinCreative             float64 `yaml:"min_creative"`
	MinInstructionFollowing float64 `yaml:"min_instruction_following"`
}

// Load reads the config from the given path, or from FRUGAL_CONFIG env var.
func Load(path string) (*Config, error) {
	if envPath := os.Getenv("FRUGAL_CONFIG"); envPath != "" {
		path = envPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	return &cfg, nil
}
