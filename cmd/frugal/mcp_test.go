package main

import (
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/config"
	"github.com/frugalsh/frugal/internal/routing"
)

func boolPtr(b bool) *bool { return &b }

func TestBuildSearchers_SkipsDisabledProviders(t *testing.T) {
	// `enabled: false` is the operator's opt-out from the config-load
	// default overlay: the entry stays in the config as a tombstone, but
	// must never register a driver. Marginalia and Wikipedia are keyless,
	// so absent the flag they always register.
	cfg := &config.Config{
		SearchProviders: map[string]config.SearchProviderConfig{
			"marginalia": {BaseURL: "https://api.marginalia.nu"},
			"wikipedia":  {BaseURL: "https://en.wikipedia.org", Enabled: boolPtr(false)},
		},
	}
	searchers := buildSearchers(cfg)
	names := make(map[string]bool, len(searchers))
	for _, s := range searchers {
		names[s.Name()] = true
	}
	if !names["marginalia"] {
		t.Errorf("marginalia should register (no enabled flag); got %v", names)
	}
	if names["wikipedia"] {
		t.Errorf("wikipedia is enabled: false and must not register; got %v", names)
	}
}

func TestBuildSearchers_YoucomFreeIsKeylessAndOrdered(t *testing.T) {
	// youcom-free needs no key and no URL, so a bare entry registers,
	// and the canonical order places it ahead of Marginalia and
	// Wikipedia: a general web index serves first, and the two narrower
	// free indexes are the fallback once the free daily allowance is
	// spent. The keyed youcom entry stays gated on its env var.
	t.Setenv("YDC_API_KEY", "")
	cfg := &config.Config{
		SearchProviders: map[string]config.SearchProviderConfig{
			"wikipedia":   {BaseURL: "https://en.wikipedia.org"},
			"youcom-free": {},
			"marginalia":  {BaseURL: "https://api.marginalia.nu"},
			"youcom":      {APIKeyEnv: "YDC_API_KEY", CostPerCall: 0.005},
		},
	}
	searchers := buildSearchers(cfg)
	var names []string
	for _, s := range searchers {
		names = append(names, s.Name())
		if s.CostPerCall() != 0 {
			t.Errorf("%s: keyless rungs must be free; got cost %v", s.Name(), s.CostPerCall())
		}
	}
	want := []string{"youcom-free", "marginalia", "wikipedia"}
	if len(names) != len(want) {
		t.Fatalf("registered %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("registration order %v, want %v", names, want)
		}
	}
	if !wireableProviders["search"]["youcom-free"] {
		t.Errorf("youcom-free must be wireable so rack-rate accounting and the switch agree")
	}
}

func TestBuildExtractors_SkipsDisabledProviders(t *testing.T) {
	cfg := &config.Config{
		ExtractProviders: map[string]config.SearchProviderConfig{
			"goreadability": {Enabled: boolPtr(false)},
		},
	}
	if got := buildExtractors(cfg); len(got) != 0 {
		t.Errorf("disabled goreadability must not register; got %d extractors", len(got))
	}
}

func TestBuildExtractors_JinaRegistersKeylessAndKeyed(t *testing.T) {
	// jina is a keyless hosted rung: unlike firecrawl it must register
	// with no key at all, and a set api_key_env upgrades the same driver
	// to the keyed tier rather than gating registration.
	t.Setenv("JINA_TEST_KEY", "")
	cfg := &config.Config{
		ExtractProviders: map[string]config.SearchProviderConfig{
			"jina": {BaseURL: "https://r.jina.ai"},
		},
	}
	got := buildExtractors(cfg)
	if len(got) != 1 || got[0].Name() != "jina" {
		t.Fatalf("keyless jina must register; got %d extractors", len(got))
	}
	if got[0].CostPerCall() != 0 {
		t.Errorf("shipped jina entry is free; CostPerCall = %v", got[0].CostPerCall())
	}

	cfg.ExtractProviders["jina"] = config.SearchProviderConfig{APIKeyEnv: "JINA_TEST_KEY"}
	if got := buildExtractors(cfg); len(got) != 1 {
		t.Fatalf("jina with an UNSET api_key_env must still register keyless; got %d", len(got))
	}

	t.Setenv("JINA_TEST_KEY", "jina_abc")
	got = buildExtractors(cfg)
	if len(got) != 1 {
		t.Fatalf("jina with a set api_key_env must register; got %d", len(got))
	}
	type keyed interface{ Keyed() bool }
	k, ok := got[0].(keyed)
	if !ok || !k.Keyed() {
		t.Errorf("jina should report the keyed tier when its api_key_env is set")
	}
}

func TestBuildExtractors_CanonicalOrderKeepsLocalBeforeHosted(t *testing.T) {
	// Same-cost ties resolve by canonical registration order: the local
	// Readability pass must come before the hosted Jina render so a
	// static page never leaves the machine.
	cfg := &config.Config{
		ExtractProviders: map[string]config.SearchProviderConfig{
			"jina":          {BaseURL: "https://r.jina.ai"},
			"goreadability": {},
		},
	}
	got := buildExtractors(cfg)
	if len(got) != 2 || got[0].Name() != "goreadability" || got[1].Name() != "jina" {
		names := make([]string, 0, len(got))
		for _, e := range got {
			names = append(names, e.Name())
		}
		t.Errorf("extract order = %v, want [goreadability jina]", names)
	}
}

func TestRackRates_SkipsDisabledAndUnwireable(t *testing.T) {
	no := false
	cfg := &config.Config{
		SearchProviders: map[string]config.SearchProviderConfig{
			"serper": {APIKeyEnv: "X", CostPerCall: 0.001},
			// Disabled premium: the operator opted out — must not anchor
			// the counterfactual.
			"youcom": {APIKeyEnv: "Y", CostPerCall: 0.005, Enabled: &no},
			// No driver exists for this name: buildSearchers would skip it,
			// so the receipt must too.
			"internal-search": {APIKeyEnv: "Z", CostPerCall: 1.0},
		},
	}
	got := rackRates(cfg)
	if got["search"] != 0.001 {
		t.Errorf("search rack rate = %v, want 0.001 (serper; youcom disabled, internal-search unwireable)", got["search"])
	}
}

func TestLoopGuardSettings_DefaultsOffSwitchAndWindow(t *testing.T) {
	intPtr := func(i int) *int { return &i }

	// No routing section: product defaults.
	if limit, window := loopGuardSettings(&config.Config{}); limit != routing.DefaultLoopLimit || window != routing.DefaultLoopWindow {
		t.Errorf("no routing: got %d/%s, want defaults", limit, window)
	}
	// Routing section without loop keys: still defaults.
	if limit, window := loopGuardSettings(&config.Config{Routing: &config.RoutingConfig{Cooldown: "90s"}}); limit != routing.DefaultLoopLimit || window != routing.DefaultLoopWindow {
		t.Errorf("no loop keys: got %d/%s, want defaults", limit, window)
	}
	// Explicit 0 is the off switch.
	if limit, _ := loopGuardSettings(&config.Config{Routing: &config.RoutingConfig{LoopLimit: intPtr(0)}}); limit != 0 {
		t.Errorf("loop_limit 0: got %d, want 0", limit)
	}
	// Explicit values win.
	if limit, window := loopGuardSettings(&config.Config{Routing: &config.RoutingConfig{LoopLimit: intPtr(8), LoopWindow: "2m"}}); limit != 8 || window != 2*time.Minute {
		t.Errorf("explicit: got %d/%s, want 8/2m", limit, window)
	}
	// Invalid or non-positive windows fall back without failing.
	for _, raw := range []string{"soon", "-5s", "0"} {
		if _, window := loopGuardSettings(&config.Config{Routing: &config.RoutingConfig{LoopWindow: raw}}); window != routing.DefaultLoopWindow {
			t.Errorf("loop_window %q: got %s, want default", raw, window)
		}
	}

	// And the guard buildGuard hands back has the rail in the configured state.
	if g := buildGuard(&config.Config{}); !g.LoopGuardEnabled() {
		t.Error("buildGuard with no config must enable the loop guard by default")
	}
	if g := buildGuard(&config.Config{Routing: &config.RoutingConfig{LoopLimit: intPtr(0)}}); g.LoopGuardEnabled() {
		t.Error("buildGuard with loop_limit 0 must leave the rail off")
	}
}

func TestPolicyFor_MapsYAMLOntoRoutingPolicy(t *testing.T) {
	rc := &config.RoutingConfig{
		Search: &config.RoutePolicy{
			Strategy: "fast",
			Order:    []string{"serper"},
			Deny:     []string{"youcom"},
		},
		Extract: &config.RoutePolicy{Strategy: "premium"},
	}

	p := policyFor(rc, "search")
	if p.Strategy != routing.StrategyFast {
		t.Errorf("search strategy = %v, want fast", p.Strategy)
	}
	if len(p.Order) != 1 || p.Order[0] != "serper" {
		t.Errorf("search order = %v", p.Order)
	}
	if !p.Deny["youcom"] {
		t.Errorf("search deny = %v, want youcom denied", p.Deny)
	}

	if p := policyFor(rc, "extract"); p.Strategy != routing.StrategyPremium {
		t.Errorf("extract strategy = %v, want premium", p.Strategy)
	}

	// browse has no section → zero value (cheap default).
	if p := policyFor(rc, "browse"); p.Strategy != routing.StrategyCheap || p.Deny != nil || len(p.Order) != 0 {
		t.Errorf("browse policy = %+v, want zero value", p)
	}

	// nil routing config entirely → zero value.
	if p := policyFor(nil, "search"); p.Strategy != routing.StrategyCheap || p.Deny != nil {
		t.Errorf("nil routing policy = %+v, want zero value", p)
	}
}

func TestBuildSearchers_BraveGatesOnKeyAndSortsAfterYoucom(t *testing.T) {
	// Brave is a keyed provider: without BRAVE_API_KEY in the environment
	// the entry must not register (an unset paid rung is deliberate, and
	// an empty token would only buy a 401). With the key set it registers
	// under the stable name "brave" and, because it ties You.com on list
	// price, the canonical order keeps You.com first so existing chains
	// don't reorder when an operator adds a Brave key.
	cfg := &config.Config{
		SearchProviders: map[string]config.SearchProviderConfig{
			"brave":  {APIKeyEnv: "FRUGAL_TEST_BRAVE_KEY", CostPerCall: 0.005},
			"youcom": {APIKeyEnv: "FRUGAL_TEST_YDC_KEY", CostPerCall: 0.005},
		},
	}

	t.Setenv("FRUGAL_TEST_BRAVE_KEY", "")
	t.Setenv("FRUGAL_TEST_YDC_KEY", "")
	if got := buildSearchers(cfg); len(got) != 0 {
		t.Fatalf("keyless brave/youcom must not register; got %v", searcherNames(got))
	}

	t.Setenv("FRUGAL_TEST_BRAVE_KEY", "brave-token")
	t.Setenv("FRUGAL_TEST_YDC_KEY", "ydc-token")
	names := searcherNames(buildSearchers(cfg))
	if len(names) != 2 || names[0] != "youcom" || names[1] != "brave" {
		t.Fatalf("expected [youcom brave] (canonical tie-break at equal price); got %v", names)
	}
	if !wireableProviders["search"]["brave"] {
		t.Errorf("brave must be wireable so rack rates and policy warnings recognize it")
	}
}
