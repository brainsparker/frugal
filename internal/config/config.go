// Package config loads Frugal's runtime configuration from models.yaml:
// the per-capability provider tables (search / extract / browse) and the
// optional routing policies that order them. Chat-model pricing /
// capability scores live outside the binary today; they come back in
// Phase 2 with the frugal__chat MCP tool.
package config

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	defaults "github.com/frugalsh/frugal/config"
)

// Config is the on-disk model.yaml decoded.
type Config struct {
	SearchProviders  map[string]SearchProviderConfig `yaml:"search_providers,omitempty"`
	ExtractProviders map[string]SearchProviderConfig `yaml:"extract_providers,omitempty"`
	BrowseProviders  map[string]SearchProviderConfig `yaml:"browse_providers,omitempty"`
	// Routing is the optional per-capability routing policy. Absent means
	// every capability routes cheapest-first — the historical default.
	Routing *RoutingConfig `yaml:"routing,omitempty"`
}

// RoutingConfig declares routing policy per capability. Each entry is
// optional; a capability without one keeps the cheap default.
type RoutingConfig struct {
	Search  *RoutePolicy `yaml:"search,omitempty"`
	Extract *RoutePolicy `yaml:"extract,omitempty"`
	Browse  *RoutePolicy `yaml:"browse,omitempty"`
	// Cooldown is the rate-limit circuit-breaker window applied to every
	// provider across all capabilities: after a provider returns a 429 the
	// router skips it for this long. A Go duration string like "90s" or
	// "2m"; empty or invalid falls back to the 60s default (invalid values
	// warn at wiring time rather than failing the load, since a
	// mistyped cooldown shouldn't brick an otherwise-good config). Top
	// level, not per capability: a provider's rate limit is a fact about
	// the provider, not about which capability called it.
	Cooldown string `yaml:"cooldown,omitempty"`
}

// RoutePolicy is one capability's routing rule.
//
//   - strategy: cheap (default — effective cost ascending) | fast (this
//     machine's observed OK-attempt latency from the local usage ledger,
//     cost order until enough history exists) | premium (list price
//     descending).
//   - order: explicit provider preference. Listed providers are tried
//     first in the listed order; unlisted ones still follow as fallback.
//     Wins over strategy when both are set.
//   - deny: providers that must never be called — not by fallback, not
//     even when a tool call pins them by name.
type RoutePolicy struct {
	Strategy string   `yaml:"strategy,omitempty"`
	Order    []string `yaml:"order,omitempty"`
	Deny     []string `yaml:"deny,omitempty"`
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
	// DailyBudgetUSD, when set above zero, caps this provider's spend per
	// UTC day: once reached, the router skips the provider (and errors a
	// pin to it) until UTC midnight. Zero or absent means no cap. Shared
	// by all three provider tables. Enforced in internal/routing.Guard,
	// not here: validation only rejects non-finite / negative values,
	// same as cost_per_call.
	DailyBudgetUSD float64 `yaml:"daily_budget_usd,omitempty"`
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
	return validateRouting(cfg)
}

// validRouteStrategies is the config-file spelling of the routing
// strategies internal/routing implements. Empty means "cheap".
var validRouteStrategies = map[string]bool{"": true, "cheap": true, "fast": true, "premium": true}

// validateRouting checks the optional routing section: strategy must be
// a known spelling, and every provider named in order / deny must be a
// provider this capability's scope knows — either from the shipped
// defaults or the operator's own file. A policy naming a foreign
// provider would silently do nothing at runtime, so it fails the load
// with a wrong-section hint instead, matching the tombstone rule above.
func validateRouting(cfg *Config) error {
	if cfg.Routing == nil {
		return nil
	}
	for _, c := range []struct {
		capability string
		scope      string
		pol        *RoutePolicy
		providers  map[string]SearchProviderConfig
	}{
		{"search", "search_providers", cfg.Routing.Search, cfg.SearchProviders},
		{"extract", "extract_providers", cfg.Routing.Extract, cfg.ExtractProviders},
		{"browse", "browse_providers", cfg.Routing.Browse, cfg.BrowseProviders},
	} {
		if c.pol == nil {
			continue
		}
		if !validRouteStrategies[strings.TrimSpace(c.pol.Strategy)] {
			return fmt.Errorf("routing.%s.strategy %q: want cheap | fast | premium", c.capability, c.pol.Strategy)
		}
		known := func(name string) bool {
			if _, ok := c.providers[name]; ok {
				return true
			}
			return defaultScopeNames()[c.scope][name]
		}
		seen := map[string]string{} // name → "order" | "deny"
		for _, list := range []struct {
			field string
			names []string
		}{{"order", c.pol.Order}, {"deny", c.pol.Deny}} {
			for _, name := range list.names {
				if !known(name) {
					return fmt.Errorf("routing.%s.%s: unknown provider %q for this capability (wrong section?)", c.capability, list.field, name)
				}
				if prev, dup := seen[name]; dup {
					if prev == list.field {
						return fmt.Errorf("routing.%s.%s: provider %q listed twice", c.capability, list.field, name)
					}
					return fmt.Errorf("routing.%s: provider %q appears in both order and deny — ordering a provider you deny is ambiguous", c.capability, name)
				}
				seen[name] = list.field
			}
		}
	}
	return nil
}

// keylessDefaults names the providers whose drivers need no endpoint
// config, PER capability scope: goreadability is pure in-process, and
// marginalia / wikipedia default their public base URL in code. The
// scoping matters — a bare `wikipedia:` under extract_providers is a
// misplaced entry that would silently do nothing at runtime, so it must
// fail validation there, not slide through a scope-blind whitelist.
var keylessDefaults = map[string]map[string]bool{
	"search_providers":  {"marginalia": true, "wikipedia": true},
	"extract_providers": {"goreadability": true},
}

// defaultScopeNames lists the provider names the embedded default
// models.yaml declares per scope. Decoded lazily and WITHOUT Parse —
// Parse validates, and validateProviders consulting Parse would recurse.
var defaultScopeNames = sync.OnceValue(func() map[string]map[string]bool {
	var cfg Config
	out := map[string]map[string]bool{}
	if err := yaml.Unmarshal(defaults.DefaultModelsYAML, &cfg); err != nil {
		return out // build bug, covered by tests; don't brick validation
	}
	for scope, m := range map[string]map[string]SearchProviderConfig{
		"search_providers":  cfg.SearchProviders,
		"extract_providers": cfg.ExtractProviders,
		"browse_providers":  cfg.BrowseProviders,
	} {
		names := make(map[string]bool, len(m))
		for n := range m {
			names[n] = true
		}
		out[scope] = names
	}
	return out
})

// validateProviders enforces the shared validity rules across any
// capability-keyed provider map. Each entry must have a non-negative
// cost and at least one of api_key_env (hosted) / base_url /
// base_url_env (self-hosted). Two exceptions: an entry disabled with
// `enabled: false` is a pure tombstone — it exists to block the default
// overlay, never dispatches, and so needs no endpoint — and this scope's
// keylessDefaults, whose drivers carry their endpoint in code. Both
// exceptions are scope-checked: an endpoint-less entry (tombstone or
// bare) naming a provider this scope doesn't know is a misplaced line
// that would silently have no effect, and the load fails fast with a
// wrong-section hint instead.
func validateProviders(scope string, providers map[string]SearchProviderConfig) error {
	for name, sp := range providers {
		if math.IsNaN(sp.CostPerCall) || math.IsInf(sp.CostPerCall, 0) {
			return fmt.Errorf("%s.%s.cost_per_call must be finite", scope, name)
		}
		if sp.CostPerCall < 0 {
			return fmt.Errorf("%s.%s.cost_per_call must be non-negative", scope, name)
		}
		if math.IsNaN(sp.DailyBudgetUSD) || math.IsInf(sp.DailyBudgetUSD, 0) {
			return fmt.Errorf("%s.%s.daily_budget_usd must be finite", scope, name)
		}
		if sp.DailyBudgetUSD < 0 {
			return fmt.Errorf("%s.%s.daily_budget_usd must be non-negative", scope, name)
		}
		hasEndpoint := strings.TrimSpace(sp.APIKeyEnv) != "" || strings.TrimSpace(sp.BaseURL) != "" || strings.TrimSpace(sp.BaseURLEnv) != ""
		if sp.Disabled() {
			// A bare tombstone is fine for any provider this scope ships
			// (`youcom: {enabled: false}` under search_providers), but one
			// naming a provider the scope doesn't know is a misplaced line
			// that would silently fail to disable anything.
			if !hasEndpoint && !keylessDefaults[scope][name] && !defaultScopeNames()[scope][name] {
				return fmt.Errorf("%s.%s: unknown provider for this section — the tombstone would have no effect (wrong section?)", scope, name)
			}
			continue
		}
		if hasEndpoint {
			continue
		}
		if keylessDefaults[scope][name] {
			continue
		}
		return fmt.Errorf("%s.%s: set api_key_env (hosted) or base_url / base_url_env (self-hosted)", scope, name)
	}
	return nil
}
