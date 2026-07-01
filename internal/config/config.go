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
	"log/slog"
	"os"
	"path/filepath"
	"sort"
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
	// Enabled, when explicitly false, keeps the entry out of tool
	// registration. LoadAuto/LoadTrusted overlay providers missing from an
	// operator's file with the shipped defaults, so deleting an entry no
	// longer removes it on the next start — `enabled: false` is the
	// durable way to record "I don't want this provider". Absent (nil) or
	// true means registered as usual.
	Enabled *bool `yaml:"enabled,omitempty"`
}

// Disabled reports whether the operator explicitly opted this provider
// out with `enabled: false`.
func (sp SearchProviderConfig) Disabled() bool {
	return sp.Enabled != nil && !*sp.Enabled
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
//
// Whatever source wins, provider entries it doesn't name are filled in
// from the embedded default (see overlayDefaults) — installs keep their
// config file across upgrades, and this is how a newly shipped provider
// reaches them. `enabled: false` on an entry is the opt-out.
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
	cfg, src, err := resolveSource(trustCwd)
	if err != nil {
		return cfg, src, err
	}
	for _, name := range overlayDefaults(cfg) {
		// Upgraders keep their ~/.frugal config file, so a provider added
		// to the shipped models.yaml after they installed shows up only
		// via this overlay. Say so, and how to opt out.
		slog.Info("config: provider defaulted in from shipped models.yaml",
			"provider", name, "source", src,
			"hint", "add the entry with 'enabled: false' to your config to suppress it")
	}
	return cfg, src, nil
}

func resolveSource(trustCwd bool) (*Config, string, error) {
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

// overlayDefaults fills provider entries missing from cfg with entries
// from the embedded default models.yaml, so upgrades that add a provider
// to the shipped config reach installs whose config file predates it —
// but ONLY free, keyless, secret-free providers (no api_key_env, no
// base_url_env in the default entry: wikipedia, marginalia,
// goreadability). Keyed providers stay strictly opt-in: silently adding
// a youcom entry would make `frugal mcp install` harvest YDC_API_KEY
// from the shell into GUI client configs the operator's file never
// authorized, and an omitted paid provider is far more likely deliberate
// than stale. Entry-level only: a provider the operator's file names —
// customized, or tombstoned with `enabled: false` — is left exactly as
// written. Returns the scope-qualified names it added, sorted, for the
// caller to log. On the embedded-default path the overlay is a no-op by
// construction (nothing is missing from itself).
func overlayDefaults(cfg *Config) []string {
	def, err := Parse(defaults.DefaultModelsYAML)
	if err != nil {
		// The embedded default is compiled in and covered by tests; if it
		// doesn't parse that's a build bug, not the operator's problem.
		// Leave their config untouched rather than failing the load.
		return nil
	}
	var added []string
	fill := func(scope string, dst *map[string]SearchProviderConfig, src map[string]SearchProviderConfig) {
		for name, sp := range src {
			if sp.APIKeyEnv != "" || sp.BaseURLEnv != "" {
				continue // keyed or operator-instance provider: never defaulted in
			}
			if _, ok := (*dst)[name]; ok {
				continue
			}
			if *dst == nil {
				*dst = make(map[string]SearchProviderConfig, len(src))
			}
			(*dst)[name] = sp
			added = append(added, scope+"."+name)
		}
	}
	fill("search_providers", &cfg.SearchProviders, def.SearchProviders)
	fill("extract_providers", &cfg.ExtractProviders, def.ExtractProviders)
	fill("browse_providers", &cfg.BrowseProviders, def.BrowseProviders)
	sort.Strings(added)
	return added
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
// base_url_env (self-hosted). Two exceptions: an entry disabled with
// `enabled: false` is a pure tombstone — it exists to block the default
// overlay, never dispatches, and so needs no endpoint — and the
// goreadability extractor, which has no API key and no base URL because
// it's a pure-in-process driver. Allow both explicitly so the YAML can
// list them without tripping validation.
func validateProviders(scope string, providers map[string]SearchProviderConfig) error {
	for name, sp := range providers {
		if sp.CostPerCall < 0 {
			return fmt.Errorf("%s.%s.cost_per_call must be non-negative", scope, name)
		}
		if sp.Disabled() {
			continue
		}
		if sp.APIKeyEnv != "" || sp.BaseURL != "" || sp.BaseURLEnv != "" {
			continue
		}
		// Drivers that need no endpoint config: goreadability is pure
		// in-process, and marginalia / wikipedia default their public base
		// URL in code — so a bare entry (e.g. a tombstone flipped back to
		// `enabled: true`) is valid for them. Whitelist explicitly.
		switch name {
		case "goreadability", "marginalia", "wikipedia":
			continue
		}
		return fmt.Errorf("%s.%s: set api_key_env (hosted) or base_url / base_url_env (self-hosted)", scope, name)
	}
	return nil
}
