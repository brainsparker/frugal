package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	defaults "github.com/frugalsh/frugal/config"
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
	if sp, ok := cfg.SearchProviders["brave"]; !ok {
		t.Errorf("expected 'brave' in SearchProviders, got %+v", cfg.SearchProviders)
	} else if sp.APIKeyEnv != "BRAVE_API_KEY" || sp.CostPerCall != 0.005 {
		t.Errorf("brave should be keyed on BRAVE_API_KEY at the $0.005 list price, got %+v", sp)
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

func TestValidate_RejectsNonFiniteCost(t *testing.T) {
	tests := []struct {
		name string
		cost string
	}{
		{name: "nan", cost: ".nan"},
		{name: "positive_inf", cost: ".inf"},
		{name: "negative_inf", cost: "-.inf"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "cfg.yaml")
			yaml := `search_providers:
  bad:
    api_key_env: X
    cost_per_call: ` + tc.cost + `
`
			if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatalf("expected validation error for non-finite cost_per_call")
			}
		})
	}
}

func TestValidate_RejectsMissingAPIKeyAndBaseURL(t *testing.T) {
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

func TestLoad_RejectsWhitespaceOnlyProviderFields(t *testing.T) {
	t.Setenv("FRUGAL_CONFIG", "")
	content := `
search_providers:
  bad:
    api_key_env: "   "
    base_url: "\t"
    base_url_env: "\n"
    cost_per_call: 0.001
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for whitespace-only provider fields")
	}
	if !strings.Contains(err.Error(), "set api_key_env") {
		t.Fatalf("expected missing provider configuration error, got: %v", err)
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
	t.Setenv("FRUGAL_CONFIG", "/does/not/exist.yaml")
	if _, _, err := LoadAuto(); err == nil {
		t.Fatalf("expected error for broken FRUGAL_CONFIG path")
	}
}

func TestLoadAuto_FallsBackToEmbeddedDefault(t *testing.T) {
	t.Setenv("FRUGAL_CONFIG", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
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
	sp, ok := cfg.SearchProviders["serper"]
	if !ok {
		t.Fatalf("expected the installer file's serper, got %+v", cfg.SearchProviders)
	}
	if sp.BaseURL != "" {
		t.Errorf("serper.BaseURL = %q, want the installer file's empty value, not the embedded default", sp.BaseURL)
	}
}

func TestLoadAuto_OverlaysMissingDefaults(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dir, "models.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := LoadAuto()
	if err != nil {
		t.Fatalf("LoadAuto: %v", err)
	}
	if _, ok := cfg.SearchProviders["wikipedia"]; !ok {
		t.Errorf("wikipedia should be defaulted in from the embedded models.yaml; got %+v", cfg.SearchProviders)
	}
	if _, ok := cfg.SearchProviders["marginalia"]; !ok {
		t.Errorf("marginalia should be defaulted in from the embedded models.yaml; got %+v", cfg.SearchProviders)
	}
	// youcom-free has a static base_url and no key or env var, so it
	// rides the same overlay: an install that predates it gains the
	// zero-key major-index rung on upgrade.
	if sp, ok := cfg.SearchProviders["youcom-free"]; !ok {
		t.Errorf("youcom-free should be defaulted in from the embedded models.yaml; got %+v", cfg.SearchProviders)
	} else if sp.CostPerCall != 0 || sp.APIKeyEnv != "" {
		t.Errorf("youcom-free default must be keyless and free; got %+v", sp)
	}
	// Keyless in-process defaults fill omitted capability maps too.
	if _, ok := cfg.ExtractProviders["goreadability"]; !ok {
		t.Errorf("extract_providers should gain the keyless embedded defaults; got %+v", cfg.ExtractProviders)
	}
	// The keyless hosted Jina Reader rung ships the same way: an install
	// whose config predates it gains the free JS-render fallback on the
	// next start without a config rewrite.
	if sp, ok := cfg.ExtractProviders["jina"]; !ok {
		t.Errorf("jina should be defaulted in from the embedded models.yaml; got %+v", cfg.ExtractProviders)
	} else if sp.APIKeyEnv != "" || sp.CostPerCall != 0 {
		t.Errorf("shipped jina default must be keyless and free; got %+v", sp)
	}
	// Keyed and operator-instance providers stay strictly opt-in: the
	// overlay must never make `frugal mcp install` harvest a secret the
	// operator's file didn't authorize, or point traffic at an instance
	// they didn't name.
	if _, ok := cfg.SearchProviders["youcom"]; ok {
		t.Errorf("keyed youcom must NOT be defaulted in; got %+v", cfg.SearchProviders)
	}
	if _, ok := cfg.SearchProviders["searxng"]; ok {
		t.Errorf("operator-instance searxng must NOT be defaulted in; got %+v", cfg.SearchProviders)
	}
	if _, ok := cfg.BrowseProviders["browserless"]; ok {
		t.Errorf("keyed browserless must NOT be defaulted in; got %+v", cfg.BrowseProviders)
	}
	if _, ok := cfg.ExtractProviders["firecrawl"]; ok {
		t.Errorf("keyed firecrawl must NOT be defaulted in; got %+v", cfg.ExtractProviders)
	}
}

func TestParse_MisplacedEntriesFailFast(t *testing.T) {
	for _, y := range []string{
		"extract_providers:\n    wikipedia: {}\n",
		"extract_providers:\n    wikipedia:\n        enabled: false\n",
		"search_providers:\n    goreadability: {}\n",
		"search_providers:\n    jina: {}\n",
	} {
		if _, err := Parse([]byte(y)); err == nil {
			t.Errorf("misplaced entry should fail validation (yaml: %q)", y)
		}
	}
}

func TestParse_BareTombstoneOfShippedKeyedProviderIsValid(t *testing.T) {
	y := "search_providers:\n    youcom:\n        enabled: false\n"
	if _, err := Parse([]byte(y)); err != nil {
		t.Errorf("bare tombstone of a shipped provider should validate; got %v", err)
	}
}

func TestParse_BareKeylessEntryIsValid(t *testing.T) {
	for _, y := range []string{
		"search_providers:\n    wikipedia:\n        enabled: true\n",
		"search_providers:\n    marginalia: {}\n",
		"search_providers:\n    youcom-free: {}\n",
		"search_providers:\n    youcom-free:\n        enabled: false\n",
	} {
		if _, err := Parse([]byte(y)); err != nil {
			t.Errorf("bare keyless entry should validate; got %v (yaml: %q)", err, y)
		}
	}
}

func TestLoadAuto_UserOverrideWins(t *testing.T) {
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
    base_url: https://serper-proxy.internal.example
    cost_per_call: 0.0005
`
	if err := os.WriteFile(filepath.Join(dir, "models.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := LoadAuto()
	if err != nil {
		t.Fatalf("LoadAuto: %v", err)
	}
	sp := cfg.SearchProviders["serper"]
	if sp.BaseURL != "https://serper-proxy.internal.example" {
		t.Errorf("serper.BaseURL = %q, want the operator's override", sp.BaseURL)
	}
	if sp.CostPerCall != 0.0005 {
		t.Errorf("serper.CostPerCall = %v, want the operator's override 0.0005", sp.CostPerCall)
	}
}

func TestLoadAuto_DisabledTombstoneSurvivesOverlay(t *testing.T) {
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
  wikipedia:
    enabled: false
`
	if err := os.WriteFile(filepath.Join(dir, "models.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := LoadAuto()
	if err != nil {
		t.Fatalf("LoadAuto: %v", err)
	}
	sp, ok := cfg.SearchProviders["wikipedia"]
	if !ok {
		t.Fatalf("tombstone entry must survive the overlay; got %+v", cfg.SearchProviders)
	}
	if !sp.Disabled() {
		t.Errorf("wikipedia should stay disabled, got %+v", sp)
	}
	if sp.BaseURL != "" {
		t.Errorf("overlay must not merge default fields into a named entry; got BaseURL=%q", sp.BaseURL)
	}
}

func TestLoadAuto_EmbeddedPathIdenticalToDefaults(t *testing.T) {
	t.Setenv("FRUGAL_CONFIG", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(t.TempDir())

	cfg, src, err := LoadAuto()
	if err != nil {
		t.Fatalf("LoadAuto: %v", err)
	}
	if src != "embedded default" {
		t.Fatalf("source = %q, want embedded default", src)
	}
	want, err := Parse(defaults.DefaultModelsYAML)
	if err != nil {
		t.Fatalf("Parse embedded default: %v", err)
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("embedded-path config diverged from the embedded default:\n got %+v\nwant %+v", cfg, want)
	}
}

func TestLoadTrusted_SkipsCwdConfig(t *testing.T) {
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

func TestParse_RoutingSectionRoundTrips(t *testing.T) {
	cfg, err := Parse([]byte(`
search_providers:
  serper:
    api_key_env: SERPER_API_KEY
    cost_per_call: 0.001
routing:
  search:
    strategy: fast
    order: [serper, searxng]
    deny: [youcom]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Routing == nil || cfg.Routing.Search == nil {
		t.Fatal("routing.search missing after parse")
	}
	p := cfg.Routing.Search
	if p.Strategy != "fast" || len(p.Order) != 2 || p.Order[0] != "serper" || len(p.Deny) != 1 || p.Deny[0] != "youcom" {
		t.Errorf("routing.search = %+v", p)
	}
}

func TestParse_RoutingRejectsUnknownStrategy(t *testing.T) {
	_, err := Parse([]byte("routing:\n  search:\n    strategy: fastest\n"))
	if err == nil || !strings.Contains(err.Error(), "routing.search.strategy") {
		t.Fatalf("want strategy error, got %v", err)
	}
}

func TestParse_RoutingRejectsUnknownProvider(t *testing.T) {
	_, err := Parse([]byte("routing:\n  search:\n    deny: [goreadability]\n"))
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("want unknown-provider error, got %v", err)
	}
}

func TestParse_RoutingAcceptsOperatorOwnProviders(t *testing.T) {
	_, err := Parse([]byte(`
search_providers:
  mysearch:
    base_url: https://search.internal
    cost_per_call: 0
routing:
  search:
    order: [mysearch]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

func TestParse_RoutingRejectsOrderDenyOverlap(t *testing.T) {
	_, err := Parse([]byte("routing:\n  search:\n    order: [serper]\n    deny: [serper]\n"))
	if err == nil || !strings.Contains(err.Error(), "both order and deny") {
		t.Fatalf("want order/deny overlap error, got %v", err)
	}
}

func TestParse_RoutingRejectsDuplicateNames(t *testing.T) {
	_, err := Parse([]byte("routing:\n  search:\n    order: [serper, serper]\n"))
	if err == nil || !strings.Contains(err.Error(), "listed twice") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}

func TestParse_ConfigWithoutRoutingUnchanged(t *testing.T) {
	cfg, err := Parse([]byte("search_providers:\n  wikipedia:\n    cost_per_call: 0\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Routing != nil {
		t.Errorf("Routing = %+v, want nil", cfg.Routing)
	}
}

func TestParse_DailyBudgetRoundTrips(t *testing.T) {
	cfg, err := Parse([]byte(`
search_providers:
  youcom:
    api_key_env: YDC_API_KEY
    cost_per_call: 0.005
    daily_budget_usd: 2.50
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cfg.SearchProviders["youcom"].DailyBudgetUSD; got != 2.50 {
		t.Errorf("youcom.daily_budget_usd = %v, want 2.50", got)
	}
}

func TestParse_DailyBudgetDefaultsToZero(t *testing.T) {
	cfg, err := Parse([]byte("search_providers:\n  wikipedia:\n    cost_per_call: 0\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cfg.SearchProviders["wikipedia"].DailyBudgetUSD; got != 0 {
		t.Errorf("absent daily_budget_usd = %v, want 0", got)
	}
}

func TestParse_RejectsNegativeDailyBudget(t *testing.T) {
	_, err := Parse([]byte(`
search_providers:
  bad:
    api_key_env: X
    cost_per_call: 0.001
    daily_budget_usd: -1
`))
	if err == nil || !strings.Contains(err.Error(), "daily_budget_usd") {
		t.Fatalf("want daily_budget_usd error, got %v", err)
	}
}

func TestParse_LoopGuardRoundTripsAndValidates(t *testing.T) {
	// Absent: nil pointer, so wiring applies the product default.
	cfg, err := Parse([]byte("routing:\n  cooldown: 90s\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Routing.LoopLimit != nil || cfg.Routing.LoopWindow != "" {
		t.Fatalf("absent loop guard must stay nil/empty, got %+v", cfg.Routing)
	}

	// Explicit values round-trip; 0 is a valid "off".
	cfg, err = Parse([]byte("routing:\n  loop_limit: 0\n  loop_window: 2m\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Routing.LoopLimit == nil || *cfg.Routing.LoopLimit != 0 || cfg.Routing.LoopWindow != "2m" {
		t.Fatalf("routing loop guard = %+v, want limit 0 window 2m", cfg.Routing)
	}
	cfg, err = Parse([]byte("routing:\n  loop_limit: 8\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Routing.LoopLimit == nil || *cfg.Routing.LoopLimit != 8 {
		t.Fatalf("routing.loop_limit = %+v, want 8", cfg.Routing.LoopLimit)
	}

	// Negative is rejected at load with a hint about 0.
	_, err = Parse([]byte("routing:\n  loop_limit: -1\n"))
	if err == nil || !strings.Contains(err.Error(), "loop_limit") {
		t.Fatalf("want loop_limit error for a negative limit, got %v", err)
	}
}

func TestParse_CooldownRoundTrips(t *testing.T) {
	cfg, err := Parse([]byte("routing:\n  cooldown: 90s\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Routing == nil || cfg.Routing.Cooldown != "90s" {
		t.Fatalf("routing.cooldown = %+v, want \"90s\"", cfg.Routing)
	}
}

func TestParse_LimitsRoundTrips(t *testing.T) {
	cfg, err := Parse([]byte("limits:\n  max_chars: 40000\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Limits == nil || cfg.Limits.MaxChars != 40000 {
		t.Fatalf("limits = %+v, want max_chars 40000", cfg.Limits)
	}
}

func TestParse_LimitsRejectsNegativeMaxChars(t *testing.T) {
	_, err := Parse([]byte("limits:\n  max_chars: -1\n"))
	if err == nil || !strings.Contains(err.Error(), "limits.max_chars") {
		t.Fatalf("expected a limits.max_chars error, got %v", err)
	}
}

func TestParse_ConfigWithoutLimitsIsUncapped(t *testing.T) {
	cfg, err := Parse([]byte("search_providers:\n  wikipedia:\n    cost_per_call: 0\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Limits != nil {
		t.Fatalf("limits should be nil when absent, got %+v", cfg.Limits)
	}
	cfg, err = Parse([]byte("limits:\n  max_chars: 0\n"))
	if err != nil {
		t.Fatalf("Parse zero: %v", err)
	}
	if cfg.Limits == nil || cfg.Limits.MaxChars != 0 {
		t.Fatalf("limits = %+v", cfg.Limits)
	}
}

func TestParse_CacheSectionRoundTrips(t *testing.T) {
	y := "cache:\n    enabled: true\n    search_ttl: 2m\n    extract_ttl: 30m\n    max_entries: 128\n"
	cfg, err := Parse([]byte(y))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Cache == nil || !cfg.Cache.Enabled {
		t.Fatalf("cache section not parsed: %+v", cfg.Cache)
	}
	if cfg.Cache.SearchTTL != "2m" || cfg.Cache.ExtractTTL != "30m" || cfg.Cache.MaxEntries != 128 {
		t.Fatalf("cache fields = %+v", cfg.Cache)
	}
}

func TestParse_CacheAbsentIsNil(t *testing.T) {
	cfg, err := Parse([]byte("search_providers:\n    wikipedia: {}\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Cache != nil {
		t.Fatalf("absent cache section must stay nil, got %+v", cfg.Cache)
	}
}

func TestParse_CacheRejectsNegativeMaxEntries(t *testing.T) {
	if _, err := Parse([]byte("cache:\n    enabled: true\n    max_entries: -1\n")); err == nil {
		t.Fatal("negative max_entries must fail validation")
	}
}

func TestParse_RoutingDomainsRoundTrip(t *testing.T) {
	cfg, err := Parse([]byte(`
routing:
  domains:
    allow: [docs.python.org, "*.github.com"]
    deny: [gist.github.com]
    deny_private: true
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Routing == nil || cfg.Routing.Domains == nil {
		t.Fatal("routing.domains missing after parse")
	}
	d := cfg.Routing.Domains
	if len(d.Allow) != 2 || d.Allow[1] != "*.github.com" || len(d.Deny) != 1 || !d.DenyPrivate {
		t.Errorf("routing.domains = %+v", d)
	}
}

func TestParse_RoutingDomainsRejectsMalformedRules(t *testing.T) {
	for raw, want := range map[string]string{
		"routing:\n  domains:\n    deny: [\"https://example.com\"]\n": "routing.domains.deny",
		"routing:\n  domains:\n    allow: [\"example.com/docs\"]\n":   "routing.domains.allow",
		"routing:\n  domains:\n    allow: [\"example.com:443\"]\n":    "port",
		"routing:\n  domains:\n    deny: [\"\"]\n":                    "empty",
	} {
		_, err := Parse([]byte(raw))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want error mentioning %q, got %v", raw, want, err)
		}
	}
}

func TestParse_RoutingDomainsRejectsDuplicatesAndOverlap(t *testing.T) {
	_, err := Parse([]byte("routing:\n  domains:\n    deny: [Example.com, example.com.]\n"))
	if err == nil || !strings.Contains(err.Error(), "listed twice") {
		t.Fatalf("want duplicate error (case and trailing dot normalized), got %v", err)
	}
	_, err = Parse([]byte("routing:\n  domains:\n    allow: [example.com]\n    deny: [example.com]\n"))
	if err == nil || !strings.Contains(err.Error(), "both allow and deny") {
		t.Fatalf("want overlap error, got %v", err)
	}
}

func TestParse_RoutingDomainsRejectsUnknownKeys(t *testing.T) {
	// KnownFields is on for the whole document; a typo like allowed:
	// must fail rather than silently leaving every host fetchable.
	_, err := Parse([]byte("routing:\n  domains:\n    allowed: [example.com]\n"))
	if err == nil {
		t.Fatal("unknown key under routing.domains should fail the load")
	}
}

func TestParse_LimitsCallTimeout(t *testing.T) {
	cfg, err := Parse([]byte("limits:\n  call_timeout: 45s\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Limits == nil || cfg.Limits.CallTimeout == nil || *cfg.Limits.CallTimeout != 45*time.Second {
		t.Fatalf("limits = %+v, want call_timeout 45s", cfg.Limits)
	}
	if _, err := Parse([]byte("limits:\n  call_timeout: -1s\n")); err == nil {
		t.Fatal("negative call_timeout accepted")
	}
}
