package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
	// The installer file's own entry survives as written — entry-level
	// merge, so the embedded serper's base_url must not leak in.
	sp, ok := cfg.SearchProviders["serper"]
	if !ok {
		t.Fatalf("expected the installer file's serper, got %+v", cfg.SearchProviders)
	}
	if sp.BaseURL != "" {
		t.Errorf("serper.BaseURL = %q, want the installer file's empty value, not the embedded default", sp.BaseURL)
	}
}

func TestLoadAuto_OverlaysMissingDefaults(t *testing.T) {
	// A config written before a provider shipped must gain it on load:
	// installs keep their file across upgrades, and the overlay is how
	// the zero-key wikipedia rung reaches them.
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
	// Keyless in-process defaults fill omitted capability maps too.
	if _, ok := cfg.ExtractProviders["goreadability"]; !ok {
		t.Errorf("extract_providers should gain the keyless embedded defaults; got %+v", cfg.ExtractProviders)
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
	// Endpoint-less entries naming a provider the section doesn't know are
	// misplaced lines that would silently do nothing at runtime — a bare
	// wikipedia under extract_providers, or a tombstone in the wrong map
	// that fails to disable anything. Both must fail the load with a hint.
	for _, y := range []string{
		"extract_providers:\n    wikipedia: {}\n",
		"extract_providers:\n    wikipedia:\n        enabled: false\n",
		"search_providers:\n    goreadability: {}\n",
	} {
		if _, err := Parse([]byte(y)); err == nil {
			t.Errorf("misplaced entry should fail validation (yaml: %q)", y)
		}
	}
}

func TestParse_BareTombstoneOfShippedKeyedProviderIsValid(t *testing.T) {
	// `youcom: {enabled: false}` without endpoint fields is the exact form
	// the overlay hint tells users to write; the scope ships youcom, so it
	// must validate.
	y := "search_providers:\n    youcom:\n        enabled: false\n"
	if _, err := Parse([]byte(y)); err != nil {
		t.Errorf("bare tombstone of a shipped provider should validate; got %v", err)
	}
}

func TestParse_BareKeylessEntryIsValid(t *testing.T) {
	// A tombstone flipped back on (`wikipedia: {enabled: true}`) or a bare
	// `marginalia:` entry has no endpoint fields — the drivers default
	// their public base URL in code, so the config must load, not brick
	// the server with a demand for api_key_env.
	for _, y := range []string{
		"search_providers:\n    wikipedia:\n        enabled: true\n",
		"search_providers:\n    marginalia: {}\n",
	} {
		if _, err := Parse([]byte(y)); err != nil {
			t.Errorf("bare keyless entry should validate; got %v (yaml: %q)", err, y)
		}
	}
}

func TestLoadAuto_UserOverrideWins(t *testing.T) {
	// An entry the operator's file names is theirs entirely; the overlay
	// only fills absent entries.
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
	// `enabled: false` is the durable opt-out: the entry is present, so
	// the overlay must not replace it with the shipped default, and it
	// needs no endpoint fields to validate.
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
	// On the embedded-default path the overlay is a no-op: the loaded
	// config must equal a straight parse of the embedded models.yaml.
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
	// goreadability is an extract provider — naming it under
	// routing.search is a misplaced line.
	_, err := Parse([]byte("routing:\n  search:\n    deny: [goreadability]\n"))
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("want unknown-provider error, got %v", err)
	}
}

func TestParse_RoutingAcceptsOperatorOwnProviders(t *testing.T) {
	// A provider declared in the operator's own file is fair game for
	// policy even if the shipped defaults don't know it.
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
