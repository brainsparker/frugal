// Package config loads Frugal's runtime configuration from models.yaml.
//
// v1.0 ships only the routed search-tool layer; chat-model pricing /
// capability scores moved out of the binary when the recipe layer was
// cut. They'll come back in Phase 2 with the frugal__chat MCP tool.
package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the on-disk model.yaml decoded.
type Config struct {
	SearchProviders  map[string]SearchProviderConfig `yaml:"search_providers,omitempty"`
	ExtractProviders map[string]SearchProviderConfig `yaml:"extract_providers,omitempty"`
	BrowseProviders  map[string]SearchProviderConfig `yaml:"browse_providers,omitempty"`
}

// SearchProviderConfig describes a routed search backend (You.com,
// Serper, SearXNG, …). The frugal__search MCP tool registers one entry
// per provider whose credentials/endpoint are present at startup; the
// auto-router picks the lowest CostPerCall among those.
//
// Providers split into two shapes:
//
//   - Hosted APIs (You.com, Serper): need an API key. APIKeyEnv is set;
//     the driver registers only if that env var is non-empty.
//   - Self-hosted backends (SearXNG): no API key. APIKeyEnv is empty;
//     BaseURLEnv (or BaseURL) supplies the endpoint the operator stood up.
type SearchProviderConfig struct {
	APIKeyEnv   string  `yaml:"api_key_env,omitempty"`
	BaseURL     string  `yaml:"base_url,omitempty"`
	BaseURLEnv  string  `yaml:"base_url_env,omitempty"`
	CostPerCall float64 `yaml:"cost_per_call"`
}

// Load reads the config from the given path, or from FRUGAL_CONFIG env var.
func Load(path string) (*Config, error) {
	if envPath := strings.TrimSpace(os.Getenv("FRUGAL_CONFIG")); envPath != "" {
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

	// Reject multi-document YAML configs. Frugal expects a single config
	// document, so anything after the first doc is treated as an error
	// instead of being silently ignored.
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("parsing config: expected a single YAML document")
		}
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	return &cfg, nil
}

func validate(cfg *Config) error {
	if err := validateProviders("search_providers", cfg.SearchProviders); err != nil {
		return err
	}
	if err := validateProviders("extract_providers", cfg.ExtractProviders); err != nil {
		return err
	}
	if err := validateProviders("browse_providers", cfg.BrowseProviders); err != nil {
		return err
	}
	return nil
}

// validateProviders enforces the shared validity rules across any
// capability-keyed provider map. Each entry must have a non-negative
// cost and at least one of api_key_env (hosted) / base_url /
// base_url_env (self-hosted). The goreadability extractor is the
// special case: no API key, no base URL — it's a pure-in-process
// driver. Allow it explicitly so the YAML can list it for visibility
// without tripping validation.
func validateProviders(scope string, providers map[string]SearchProviderConfig) error {
	for name, sp := range providers {
		if sp.CostPerCall < 0 {
			return fmt.Errorf("%s.%s.cost_per_call must be non-negative", scope, name)
		}
		if sp.APIKeyEnv != "" || sp.BaseURL != "" || sp.BaseURLEnv != "" {
			continue
		}
		// Pure-in-process drivers that don't talk to a network endpoint
		// don't need either field. Whitelist them.
		switch name {
		case "goreadability":
			continue
		}
		return fmt.Errorf("%s.%s: set api_key_env (hosted) or base_url / base_url_env (self-hosted)", scope, name)
	}
	return nil
}
