package config

import (
	"bytes"
	"fmt"
	"math"
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
	Vision               bool    `yaml:"vision"`
	MaxContext           int     `yaml:"max_context"`
	// Source names the benchmark suite these scores were derived from
	// (e.g. "livebench+aider"). AsOf is an ISO-8601 date string so
	// operators know when the scores were last refreshed. Routing
	// decisions are only as defensible as these fields — keep them current.
	Source string `yaml:"source,omitempty"`
	AsOf   string `yaml:"as_of,omitempty"`
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
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

func validate(cfg *Config) error {
	for providerName, provider := range cfg.Providers {
		for modelName, model := range provider.Models {
			if model.CostPer1KInput < 0 || model.CostPer1KOutput < 0 {
				return fmt.Errorf("providers.%s.models.%s costs must be non-negative", providerName, modelName)
			}
			if err := validateUnitInterval(fmt.Sprintf("providers.%s.models.%s.capabilities.reasoning", providerName, modelName), model.Capabilities.Reasoning); err != nil {
				return err
			}
			if err := validateUnitInterval(fmt.Sprintf("providers.%s.models.%s.capabilities.coding", providerName, modelName), model.Capabilities.Coding); err != nil {
				return err
			}
			if err := validateUnitInterval(fmt.Sprintf("providers.%s.models.%s.capabilities.creative", providerName, modelName), model.Capabilities.Creative); err != nil {
				return err
			}
			if err := validateUnitInterval(fmt.Sprintf("providers.%s.models.%s.capabilities.instruction_following", providerName, modelName), model.Capabilities.InstructionFollowing); err != nil {
				return err
			}
		}
	}

	for thresholdName, threshold := range cfg.QualityThresholds {
		if err := validateUnitInterval(fmt.Sprintf("quality_thresholds.%s.min_reasoning", thresholdName), threshold.MinReasoning); err != nil {
			return err
		}
		if err := validateUnitInterval(fmt.Sprintf("quality_thresholds.%s.min_coding", thresholdName), threshold.MinCoding); err != nil {
			return err
		}
		if err := validateUnitInterval(fmt.Sprintf("quality_thresholds.%s.min_creative", thresholdName), threshold.MinCreative); err != nil {
			return err
		}
		if err := validateUnitInterval(fmt.Sprintf("quality_thresholds.%s.min_instruction_following", thresholdName), threshold.MinInstructionFollowing); err != nil {
			return err
		}
	}

	return nil
}

func validateUnitInterval(path string, value float64) error {
	if math.IsNaN(value) || value < 0 || value > 1 {
		return fmt.Errorf("%s must be between 0 and 1 (got %v)", path, value)
	}
	return nil
}
