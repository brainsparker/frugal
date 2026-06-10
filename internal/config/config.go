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
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	defaults "github.com/frugalsh/frugal/config"
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

// Load reads the config from the given path. Environment resolution
// ($FRUGAL_CONFIG, installer layout, embedded default) lives in LoadAuto
// — Load itself is filesystem-pure so tests and explicit callers get
// exactly the file they named.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return Parse(data)
}

// LoadAuto resolves the config source in precedence order:
//
//  1. $FRUGAL_CONFIG — explicit operator override. A broken path here is
//     an error, not a fall-through: the operator asked for that file.
//  2. ./config/models.yaml — running from a source checkout.
//  3. ~/.frugal/config/models.yaml — the curl-installer layout.
//  4. The default models.yaml embedded in the binary — npm wrapper,
//     MCP-registry installs, and GUI-spawned processes that see no shell
//     environment at all.
//
// A file that exists but fails to parse is an error at every step; a
// silent fall-through would mask the operator's typo with defaults.
// Returns the config and a human-readable source description for logs.
func LoadAuto() (*Config, string, error) {
	return loadResolved(true)
}

// LoadTrusted is LoadAuto minus the cwd-relative ./config/models.yaml
// candidate. `frugal mcp install` uses it to decide which env vars get
// copied out of the shell into client configs: a config file in
// whatever directory the user happened to run install from must not get
// to name which secrets are harvested and persisted. $FRUGAL_CONFIG and
// the home-dir config are operator-owned; the cwd is not.
func LoadTrusted() (*Config, string, error) {
	return loadResolved(false)
}

func loadResolved(trustCwd bool) (*Config, string, error) {
	if envPath := strings.TrimSpace(os.Getenv("FRUGAL_CONFIG")); envPath != "" {
		cfg, err := Load(envPath)
		return cfg, "$FRUGAL_CONFIG (" + envPath + ")", err
	}
	var candidates []string
	if trustCwd {
		candidates = append(candidates, filepath.Join("config", "models.yaml"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".frugal", "config", "models.yaml"))
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			cfg, err := Load(p)
			return cfg, p, err
		}
	}
	cfg, err := Parse(defaults.DefaultModelsYAML)
	return cfg, "embedded default", err
}

// Parse decodes and validates one models.yaml document.
func Parse(data []byte) (*Config, error) {
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
