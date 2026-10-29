package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/frugalsh/frugal/internal/browse"
	"github.com/frugalsh/frugal/internal/config"
	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/install"
	"github.com/frugalsh/frugal/internal/ledger"
	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// frugal doctor is the preflight: it answers "will my next routed call
// work, and what will it cost" before an agent finds out the hard way.
//
// An MCP server has no place to surface a bad key, an exhausted budget,
// or a provider that quietly started returning 401. The agent sees a
// failover to a narrower rung or an empty result and carries on. doctor
// runs the same provider constructors, config parsing, and policy
// resolution as `mcp serve`, reports every provider's state in one
// table, and (with --probe) makes one real request per free provider
// through the same driver code the tools use. Paid providers are never
// probed unless --probe-paid is passed, and the cost is disclosed.
//
// Exit codes: 0 healthy (warnings allowed), 1 at least one failure,
// 2 usage error. --strict promotes warnings to failures for CI.

// Check levels, ordered from best to worst. The summary and exit code
// derive from the worst level present.
const (
	levelOK   = "ok"
	levelWarn = "warn"
	levelFail = "fail"
	levelSkip = "skip"
)

// doctorCheck is one non-provider finding (config, policy, ledger, client).
type doctorCheck struct {
	Area   string `json:"area"`
	Name   string `json:"name"`
	Level  string `json:"level"`
	Detail string `json:"detail"`
}

// providerCheck is the per-provider row of the report.
type providerCheck struct {
	Capability string `json:"capability"`
	Name       string `json:"name"`
	// Status is the registration verdict: ready | disabled | missing_key |
	// missing_url | unknown_driver | invalid_url.
	Status string `json:"status"`
	Level  string `json:"level"`
	Detail string `json:"detail,omitempty"`
	// KeyEnv / URLEnv name the environment variables the entry reads, so
	// the fix is on the row ("set BRAVE_API_KEY").
	KeyEnv string `json:"key_env,omitempty"`
	URLEnv string `json:"url_env,omitempty"`
	// Price and budget. SpentTodayUSD comes from the local usage ledger
	// (UTC day), which is the same number the server's daily cap counts
	// toward within one process; across restarts the ledger is the
	// longer memory.
	CostPerCall    float64      `json:"cost_per_call"`
	Paid           bool         `json:"paid"`
	DailyBudgetUSD float64      `json:"daily_budget_usd,omitempty"`
	SpentTodayUSD  float64      `json:"spent_today_usd"`
	BudgetLeftUSD  *float64     `json:"budget_left_usd,omitempty"`
	Denied         bool         `json:"denied,omitempty"`
	Probe          *probeResult `json:"probe,omitempty"`
}

// probeResult is the outcome of one live request through the driver.
type probeResult struct {
	// Status: ok | empty | error | skipped.
	Status    string  `json:"status"`
	LatencyMS int64   `json:"latency_ms,omitempty"`
	HTTPCode  int     `json:"http_status,omitempty"`
	Kind      string  `json:"kind,omitempty"`
	CostUSD   float64 `json:"cost_usd"`
	Detail    string  `json:"detail,omitempty"`
}

// doctorReport is the whole picture, also the --json shape.
type doctorReport struct {
	Version      string          `json:"version"`
	ConfigSource string          `json:"config_source"`
	Checks       []doctorCheck   `json:"checks"`
	Providers    []providerCheck `json:"providers"`
	Probed       bool            `json:"probed"`
	ProbeCostUSD float64         `json:"probe_cost_usd"`
	Summary      struct {
		OK   int `json:"ok"`
		Warn int `json:"warn"`
		Fail int `json:"fail"`
	} `json:"summary"`
}

// doctorOptions are the resolved flags plus the seams tests use to point
// probes at httptest servers and to pin the clock.
type doctorOptions struct {
	Probe     bool
	ProbePaid bool
	ProbeURL  string
	Timeout   time.Duration
	Strict    bool
	now       func() time.Time
}

func runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	probe := fs.Bool("probe", false, "make one live request per configured free provider (cost $0) and report reachability, auth, and latency")
	probePaid := fs.Bool("probe-paid", false, "also probe paid providers; one billed request each at the configured cost_per_call (implies --probe)")
	probeURL := fs.String("probe-url", "https://example.com/", "page the extract and browse probes fetch")
	timeout := fs.Duration("timeout", 10*time.Second, "per-probe deadline")
	asJSON := fs.Bool("json", false, "emit the report as JSON")
	strict := fs.Bool("strict", false, "exit 1 on warnings too (for CI)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: frugal doctor [--probe] [--probe-paid] [--json] [--strict]")
		fmt.Fprintln(os.Stderr, "Check the config, every provider's key and budget, and the MCP clients")
		fmt.Fprintln(os.Stderr, "frugal is installed in, before an agent finds a broken rung the hard way.")
		fmt.Fprintln(os.Stderr, "--probe makes one real request per free provider; paid providers are")
		fmt.Fprintln(os.Stderr, "only called with --probe-paid, and the spend is printed.")
		fmt.Fprintln(os.Stderr, "Exit 0 healthy, 1 a failure (or a warning with --strict), 2 usage.")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "frugal doctor: --timeout must be positive")
		return 2
	}
	if _, err := url.ParseRequestURI(*probeURL); err != nil {
		fmt.Fprintf(os.Stderr, "frugal doctor: --probe-url: %v\n", err)
		return 2
	}
	opts := doctorOptions{
		Probe:     *probe || *probePaid,
		ProbePaid: *probePaid,
		ProbeURL:  *probeURL,
		Timeout:   *timeout,
		Strict:    *strict,
	}
	quietStartupLogs()
	rep := diagnose(opts)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintf(os.Stderr, "frugal doctor: encode: %v\n", err)
			return 1
		}
	} else {
		renderDoctor(os.Stdout, rep, opts)
	}
	if rep.Summary.Fail > 0 || (opts.Strict && rep.Summary.Warn > 0) {
		return 1
	}
	return 0
}

// quietStartupLogs raises the default logger to Warn for the run unless
// the operator asked for a level: the provider constructors log their
// registration at Info, and the report already says all of that.
func quietStartupLogs() {
	if strings.TrimSpace(os.Getenv("FRUGAL_LOG_LEVEL")) != "" {
		return
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
}

// diagnose builds the report. It never writes anything: no config, no
// ledger, no client files. Probes are the only side effect, and only
// when asked for.
func diagnose(opts doctorOptions) doctorReport {
	if opts.now == nil {
		opts.now = time.Now
	}
	now := opts.now().UTC()
	rep := doctorReport{Version: version()}
	add := func(area, name, level, detail string) {
		rep.Checks = append(rep.Checks, doctorCheck{Area: area, Name: name, Level: level, Detail: detail})
	}

	cfg, src, err := config.LoadAuto()
	rep.ConfigSource = src
	if err != nil {
		add("config", "load", levelFail, err.Error())
		finishDoctor(&rep)
		return rep
	}
	add("config", "source", levelOK, src)

	// Limits, cooldown, loop guard, cache: the same parsing mcp serve
	// does, reported instead of logged.
	checkLimits(cfg, add)
	checkRoutingKnobs(cfg, add)
	checkCache(cfg, add)
	checkDomains(cfg, add)

	// Today's spend per capability/provider from the ledger, for the
	// budget column. A missing ledger is not an error: nothing has been
	// routed yet.
	spent, ledgerDetail, ledgerLevel := ledgerToday(now)
	add("ledger", "usage", ledgerLevel, ledgerDetail)

	// Provider tables. The constructors are the ones mcp serve uses, so
	// "ready" here means "would be registered there".
	searchers := buildSearchers(cfg)
	extractors := buildExtractors(cfg)
	browsers := buildBrowsers(cfg)
	registered := map[string]map[string]bool{
		"search":  nameSet(searcherNames(searchers)),
		"extract": nameSet(extractorNames(extractors)),
		"browse":  nameSet(browserNames(browsers)),
	}
	policies := map[string]routing.Policy{
		"search":  policyFor(cfg.Routing, "search"),
		"extract": policyFor(cfg.Routing, "extract"),
		"browse":  policyFor(cfg.Routing, "browse"),
	}
	tables := []struct {
		capability string
		providers  map[string]config.SearchProviderConfig
	}{
		{"search", cfg.SearchProviders},
		{"extract", cfg.ExtractProviders},
		{"browse", cfg.BrowseProviders},
	}
	for _, t := range tables {
		for _, name := range sortedProviderNames(t.providers) {
			pc := inspectProvider(t.capability, name, t.providers[name], registered[t.capability][name], policies[t.capability], spent)
			rep.Providers = append(rep.Providers, pc)
		}
		checkPolicy(t.capability, policies[t.capability], registered[t.capability], add)
	}

	// Capability coverage: what tools/list will advertise.
	switch {
	case len(searchers) == 0 && len(extractors) == 0 && len(browsers) == 0:
		add("tools", "coverage", levelFail, "no provider is ready in any table; mcp serve would advertise no tools")
	case len(searchers) == 0:
		add("tools", "frugal__search", levelWarn, "no search provider is ready; the tool will not be advertised (set SEARXNG_URL, SERPER_API_KEY, BRAVE_API_KEY, or YDC_API_KEY, or enable a keyless entry)")
	default:
		add("tools", "frugal__search", levelOK, fmt.Sprintf("%d provider(s): %s", len(searchers), strings.Join(searcherNames(searchers), ", ")))
	}
	if len(extractors) > 0 {
		add("tools", "frugal__extract", levelOK, fmt.Sprintf("%d provider(s): %s", len(extractors), strings.Join(extractorNames(extractors), ", ")))
	} else {
		add("tools", "frugal__extract", levelWarn, "no extract provider is ready; the tool will not be advertised")
	}
	if len(browsers) > 0 {
		add("tools", "frugal__browse", levelOK, fmt.Sprintf("%d provider(s): %s", len(browsers), strings.Join(browserNames(browsers), ", ")))
	} else {
		add("tools", "frugal__browse", levelSkip, "no browse provider configured (set BROWSERLESS_TOKEN to enable headless renders)")
	}

	// MCP clients on this machine, and whether frugal is in their config.
	for _, c := range install.DetectClients() {
		area, name, level, detail := clientCheck(c)
		add(area, name, level, detail)
	}

	if opts.Probe {
		rep.Probed = true
		rep.ProbeCostUSD = runProbes(&rep, searchers, extractors, browsers, opts)
	}

	finishDoctor(&rep)
	return rep
}

// inspectProvider classifies one config entry without touching the
// network: registration verdict, key hygiene, price, budget, deny state.
func inspectProvider(capability, name string, sp config.SearchProviderConfig, isRegistered bool, pol routing.Policy, spent map[string]float64) providerCheck {
	pc := providerCheck{
		Capability:     capability,
		Name:           name,
		KeyEnv:         sp.APIKeyEnv,
		URLEnv:         sp.BaseURLEnv,
		CostPerCall:    sp.CostPerCall,
		Paid:           sp.CostPerCall > 0,
		DailyBudgetUSD: sp.DailyBudgetUSD,
		SpentTodayUSD:  spent[capability+"/"+name],
		Denied:         pol.Deny[name],
	}
	if sp.DailyBudgetUSD > 0 {
		left := sp.DailyBudgetUSD - pc.SpentTodayUSD
		if left < 0 {
			left = 0
		}
		pc.BudgetLeftUSD = &left
	}

	var notes []string
	switch {
	case sp.Disabled():
		pc.Status, pc.Level = "disabled", levelSkip
		notes = append(notes, "enabled: false in config")
	case !wireableProviders[capability][name]:
		pc.Status, pc.Level = "unknown_driver", levelWarn
		notes = append(notes, "no driver for this name; the entry is ignored at startup")
	case sp.APIKeyEnv != "" && !isRegistered:
		pc.Status = "missing_key"
		if _, set := os.LookupEnv(sp.APIKeyEnv); set {
			notes = append(notes, sp.APIKeyEnv+" is set but empty; the provider is skipped")
		} else {
			notes = append(notes, "set "+sp.APIKeyEnv+" to enable")
		}
		// A paid provider without a key is the normal state of a fresh
		// install, not a problem. It is informational.
		pc.Level = levelSkip
	case sp.BaseURLEnv != "" && sp.APIKeyEnv == "" && !isRegistered:
		pc.Status, pc.Level = "missing_url", levelSkip
		notes = append(notes, "set "+sp.BaseURLEnv+" to enable")
	case !isRegistered:
		pc.Status, pc.Level = "not_registered", levelWarn
		notes = append(notes, "constructor declined this entry; check base_url")
	default:
		pc.Status, pc.Level = "ready", levelOK
	}

	// Key hygiene. Only meaningful when a key is present.
	if sp.APIKeyEnv != "" {
		if v, set := os.LookupEnv(sp.APIKeyEnv); set && v != "" {
			for _, w := range keyWarnings(sp.APIKeyEnv, v) {
				notes = append(notes, w)
				if pc.Level == levelOK {
					pc.Level = levelWarn
				}
			}
		}
	}

	// Endpoint hygiene: a base_url that does not parse is a fail for a
	// ready provider because every call would error.
	base := sp.BaseURL
	if sp.BaseURLEnv != "" {
		if v := os.Getenv(sp.BaseURLEnv); v != "" {
			base = v
		}
	}
	if base != "" {
		if u, err := url.Parse(base); err != nil || u.Scheme == "" || u.Host == "" {
			notes = append(notes, fmt.Sprintf("base url %q is not an absolute http(s) URL", base))
			if pc.Status == "ready" {
				pc.Status, pc.Level = "invalid_url", levelFail
			}
		} else if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
			notes = append(notes, "base url is plain http to a non-local host; the key travels in clear text")
			if pc.Level == levelOK {
				pc.Level = levelWarn
			}
		}
	}

	// Budget state. Exhausted means the router skips this provider until
	// UTC midnight, which is a warning for a ready provider: the chain
	// still works, but not the way the operator may expect.
	if pc.BudgetLeftUSD != nil && pc.Status == "ready" {
		switch {
		case *pc.BudgetLeftUSD == 0:
			notes = append(notes, fmt.Sprintf("daily budget $%.4f exhausted today; skipped until UTC midnight", sp.DailyBudgetUSD))
			if pc.Level == levelOK {
				pc.Level = levelWarn
			}
		case sp.CostPerCall > 0 && *pc.BudgetLeftUSD < sp.CostPerCall:
			notes = append(notes, "less than one call left in today's budget")
			if pc.Level == levelOK {
				pc.Level = levelWarn
			}
		case sp.CostPerCall > 0:
			notes = append(notes, fmt.Sprintf("about %d call(s) left in today's budget", int(*pc.BudgetLeftUSD/sp.CostPerCall)))
		}
	}
	if pc.Denied && pc.Status == "ready" {
		notes = append(notes, "denied by routing policy; never called, not even pinned")
	}
	pc.Detail = strings.Join(notes, "; ")
	return pc
}

// keyWarnings flags the key mistakes that produce a confusing 401 rather
// than a clear error: pasted whitespace, shell quotes, a "Bearer " prefix
// copied from a curl example, or a placeholder left in a dotfile.
func keyWarnings(envName, v string) []string {
	var out []string
	if strings.TrimSpace(v) != v {
		out = append(out, envName+" has leading or trailing whitespace; most APIs reject it")
	}
	t := strings.TrimSpace(v)
	if len(t) >= 2 && ((t[0] == '"' && t[len(t)-1] == '"') || (t[0] == '\'' && t[len(t)-1] == '\'')) {
		out = append(out, envName+" is wrapped in quotes; the quotes are sent as part of the key")
	}
	if strings.HasPrefix(strings.ToLower(t), "bearer ") {
		out = append(out, envName+" starts with \"Bearer \"; frugal adds the scheme itself")
	}
	lower := strings.ToLower(t)
	for _, p := range []string{"your-", "your_", "<", ">", "xxx", "changeme", "replace", "example", "todo"} {
		if strings.Contains(lower, p) {
			out = append(out, envName+" looks like a placeholder value")
			break
		}
	}
	if len(t) > 0 && len(t) < 8 {
		out = append(out, envName+" is shorter than any real key")
	}
	return out
}

func isLoopbackHost(h string) bool {
	h = strings.ToLower(h)
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".localhost")
}

func nameSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// checkLimits reports call_timeout, retries, and max_chars as resolved.
func checkLimits(cfg *config.Config, add func(area, name, level, detail string)) {
	callDeadline := routing.DefaultCallTimeout
	if cfg.Limits != nil && cfg.Limits.CallTimeout != nil {
		callDeadline = *cfg.Limits.CallTimeout
	}
	if callDeadline <= 0 {
		add("limits", "call_timeout", levelWarn, "disabled (0s): a slow failover chain can block an agent for minutes")
	} else {
		add("limits", "call_timeout", levelOK, callDeadline.String())
	}
	retries := routing.DefaultRetries
	if cfg.Limits != nil && cfg.Limits.Retries != nil {
		retries = *cfg.Limits.Retries
	}
	add("limits", "retries", levelOK, fmt.Sprintf("%d (free providers only; paid providers never retry)", retries))
	if cfg.Limits != nil && cfg.Limits.MaxChars > 0 {
		add("limits", "max_chars", levelOK, fmt.Sprintf("%d (about %d tokens per read)", cfg.Limits.MaxChars, (cfg.Limits.MaxChars+3)/4))
	} else {
		add("limits", "max_chars", levelSkip, "unset: pages are returned whole; Claude Code rejects MCP results over 25k tokens (limits.max_chars: 40000 is a good start)")
	}
}

// defaultCooldownForDoctor mirrors routing's unexported 60s default so
// the report can name the value the server falls back to.
const defaultCooldownForDoctor = 60 * time.Second

// checkRoutingKnobs parses cooldown and loop guard the way buildGuard
// does, surfacing the fallbacks it would only log.
func checkRoutingKnobs(cfg *config.Config, add func(area, name, level, detail string)) {
	cooldown := defaultCooldownForDoctor
	cooldownNote := "default"
	if cfg.Routing != nil {
		if raw := strings.TrimSpace(cfg.Routing.Cooldown); raw != "" {
			d, err := time.ParseDuration(raw)
			switch {
			case err != nil:
				add("routing", "cooldown", levelWarn, fmt.Sprintf("%q is not a duration; the server falls back to %s", raw, cooldown))
				cooldownNote = ""
			case d <= 0:
				add("routing", "cooldown", levelWarn, fmt.Sprintf("%q must be positive; the server falls back to %s", raw, cooldown))
				cooldownNote = ""
			default:
				cooldown, cooldownNote = d, "configured"
			}
		}
	}
	if cooldownNote != "" {
		add("routing", "cooldown", levelOK, fmt.Sprintf("%s (%s)", cooldown, cooldownNote))
	}

	limit, window := loopGuardSettings(cfg)
	if cfg.Routing != nil {
		if raw := strings.TrimSpace(cfg.Routing.LoopWindow); raw != "" {
			if d, err := time.ParseDuration(raw); err != nil || d <= 0 {
				add("routing", "loop_window", levelWarn, fmt.Sprintf("%q is not a positive duration; the server falls back to %s", raw, window))
			}
		}
	}
	if limit > 0 {
		add("routing", "loop_guard", levelOK, fmt.Sprintf("%d identical calls per %s", limit, window))
	} else {
		add("routing", "loop_guard", levelWarn, "disabled (loop_limit: 0): a stuck agent can burn a daily budget before anything stops it")
	}
}

// checkCache mirrors buildResultCache's TTL parsing.
func checkCache(cfg *config.Config, add func(area, name, level, detail string)) {
	if cfg.Cache == nil || !cfg.Cache.Enabled {
		add("cache", "result_cache", levelSkip, "off (cache.enabled: true answers repeated identical calls from memory at $0)")
		return
	}
	bad := false
	for _, f := range []struct{ name, raw string }{{"search_ttl", cfg.Cache.SearchTTL}, {"extract_ttl", cfg.Cache.ExtractTTL}} {
		raw := strings.TrimSpace(f.raw)
		if raw == "" || raw == "0" {
			continue
		}
		if d, err := time.ParseDuration(raw); err != nil || d < 0 {
			add("cache", f.name, levelWarn, fmt.Sprintf("%q is not a duration; the server uses the default", raw))
			bad = true
		}
	}
	if !bad {
		add("cache", "result_cache", levelOK, "on")
	}
}

// checkDomains reports the fetch-target policy. Config validation has
// already rejected malformed rules, so this is a summary line.
func checkDomains(cfg *config.Config, add func(area, name, level, detail string)) {
	if cfg.Routing == nil || cfg.Routing.Domains == nil {
		add("routing", "domains", levelSkip, "no domain policy: every host is fetchable")
		return
	}
	d := cfg.Routing.Domains
	p, err := routing.NewDomainPolicy(d.Allow, d.Deny, d.DenyPrivate)
	if err != nil {
		add("routing", "domains", levelFail, err.Error())
		return
	}
	if !p.Enabled() {
		add("routing", "domains", levelSkip, "section present but empty: every host is fetchable")
		return
	}
	add("routing", "domains", levelOK, p.Describe())
}

// checkPolicy flags order / deny entries that name no ready provider,
// the same strangers mcp serve warns about at startup.
func checkPolicy(capability string, p routing.Policy, registered map[string]bool, add func(area, name, level, detail string)) {
	if len(p.Order) == 0 && len(p.Deny) == 0 {
		return
	}
	for _, n := range p.Order {
		if !registered[n] {
			add("routing", capability+".order", levelWarn, fmt.Sprintf("%q is listed but not ready; the entry is ignored", n))
		}
	}
	for _, n := range denyNames(p.Deny) {
		if !registered[n] {
			add("routing", capability+".deny", levelWarn, fmt.Sprintf("%q is denied but not ready anyway", n))
		}
	}
}

// ledgerToday sums today's (UTC) spend per "capability/provider" from the
// current month's ledger file. Returns the map, a detail line, and a
// level for the ledger check.
func ledgerToday(now time.Time) (map[string]float64, string, string) {
	spent := map[string]float64{}
	if !ledger.Enabled() {
		return spent, "FRUGAL_STATS=off: no usage ledger, budgets reset on every restart", levelWarn
	}
	dir, err := ledger.Dir()
	if err != nil {
		return spent, "cannot resolve home dir: " + err.Error(), levelWarn
	}
	f, err := os.Open(filepath.Join(dir, ledger.MonthFile(now)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return spent, dir + " (no calls recorded this month yet)", levelOK
		}
		return spent, "cannot read " + dir + ": " + err.Error(), levelWarn
	}
	defer f.Close()
	y, m, d := now.Date()
	var calls, todayCalls int
	var todayUSD float64
	r := bufio.NewReader(f)
	for {
		line, rerr := r.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var rec ledger.Record
			if json.Unmarshal(line, &rec) == nil {
				calls++
				ry, rm, rd := rec.TS.UTC().Date()
				if ry == y && rm == m && rd == d {
					todayCalls++
					todayUSD += rec.CostUSD
					spent[rec.Tool+"/"+rec.Provider] += rec.CostUSD
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	return spent, fmt.Sprintf("%s: %d call(s) this month, %d today for %s", dir, calls, todayCalls, usd(todayUSD)), levelOK
}

// clientCheck reports whether a known MCP client is installed and, for
// file-based clients, whether frugal is already in its server list.
func clientCheck(c install.Client) (area, name, level, detail string) {
	area, name = "clients", c.ID
	if !c.Detected {
		return area, name, levelSkip, "not detected (" + c.DetectionReason + ")"
	}
	if c.Kind != install.KindJSONFile || c.ConfigPath == "" {
		return area, name, levelOK, "detected (" + c.DetectionReason + "); run `claude mcp list` to confirm frugal is registered"
	}
	data, err := os.ReadFile(c.ConfigPath)
	if err != nil {
		return area, name, levelWarn, "detected but " + c.ConfigPath + " is not readable; run `frugal mcp install --client " + c.ID + "`"
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return area, name, levelWarn, c.ConfigPath + " is not valid JSON: " + err.Error()
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if _, ok := servers["frugal"]; ok {
		return area, name, levelOK, "frugal configured in " + c.ConfigPath
	}
	return area, name, levelWarn, "detected, frugal not configured; run `frugal mcp install --client " + c.ID + "`"
}

// runProbes makes one request per registered provider through the same
// driver code the tools use, concurrently, each under opts.Timeout.
// Paid providers are skipped unless opts.ProbePaid. Returns the summed
// cost of the probes that ran.
func runProbes(rep *doctorReport, searchers []search.Searcher, extractors []extract.Extractor, browsers []browse.Browser, opts doctorOptions) float64 {
	type job struct {
		capability, name string
		paid             bool
		run              func(ctx context.Context) (float64, string, error)
	}
	var jobs []job
	for _, s := range searchers {
		s := s
		jobs = append(jobs, job{"search", s.Name(), s.CostPerCall() > 0, func(ctx context.Context) (float64, string, error) {
			res, err := s.Search(ctx, search.Query{Text: "frugal", MaxResults: 1})
			if err != nil {
				return 0, "", err
			}
			if len(res.Items) == 0 {
				return res.CostUSD, "", errEmpty
			}
			return res.CostUSD, fmt.Sprintf("%d hit(s)", len(res.Items)), nil
		}})
	}
	for _, e := range extractors {
		e := e
		jobs = append(jobs, job{"extract", e.Name(), e.CostPerCall() > 0, func(ctx context.Context) (float64, string, error) {
			res, err := e.Extract(ctx, extract.Query{URL: opts.ProbeURL})
			if err != nil {
				return 0, "", err
			}
			n := len(res.Markdown) + len(res.Text) + len(res.HTML)
			if n == 0 {
				return res.CostUSD, "", errEmpty
			}
			return res.CostUSD, fmt.Sprintf("%d chars", n), nil
		}})
	}
	for _, b := range browsers {
		b := b
		jobs = append(jobs, job{"browse", b.Name(), b.CostPerCall() > 0, func(ctx context.Context) (float64, string, error) {
			res, err := b.Render(ctx, browse.Query{URL: opts.ProbeURL, ReturnFormat: "text"})
			if err != nil {
				return 0, "", err
			}
			n := len(res.HTML) + len(res.Text)
			if n == 0 {
				return res.CostUSD, "", errEmpty
			}
			return res.CostUSD, fmt.Sprintf("%d chars", n), nil
		}})
	}

	results := make([]*probeResult, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		if j.paid && !opts.ProbePaid {
			results[i] = &probeResult{Status: "skipped", Detail: "paid provider; pass --probe-paid to spend one call"}
			continue
		}
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
			defer cancel()
			start := time.Now()
			cost, detail, err := j.run(ctx)
			results[i] = classifyProbe(err, cost, detail, time.Since(start))
		}(i, j)
	}
	wg.Wait()

	var total float64
	for i, j := range jobs {
		pr := results[i]
		total += pr.CostUSD
		for k := range rep.Providers {
			p := &rep.Providers[k]
			if p.Capability == j.capability && p.Name == j.name {
				p.Probe = pr
				switch pr.Status {
				case "error":
					// Auth, billing, and bad-request errors are the
					// operator's to fix: fail. Timeouts, 5xx, 429, and
					// network trouble are the provider's problem today:
					// warn, so a third-party blip does not fail CI.
					if pr.Kind == routing.KindPermanent.String() {
						p.Level = levelFail
					} else if p.Level != levelFail {
						p.Level = levelWarn
					}
				case "empty":
					if p.Level == levelOK {
						p.Level = levelWarn
					}
				}
			}
		}
	}
	return total
}

var errEmpty = errors.New("empty")

// classifyProbe turns a driver error into a row the operator can act on.
func classifyProbe(err error, cost float64, detail string, took time.Duration) *probeResult {
	pr := &probeResult{LatencyMS: took.Milliseconds(), CostUSD: cost}
	if err == nil {
		pr.Status, pr.Detail = "ok", detail
		return pr
	}
	if errors.Is(err, errEmpty) {
		pr.Status, pr.Detail = "empty", "responded with no content; the chain would fall through"
		return pr
	}
	pr.Status = "error"
	var re *routing.Error
	if errors.As(err, &re) {
		pr.HTTPCode = re.Status
		pr.Kind = re.Kind.String()
		switch {
		case re.Status == 401 || re.Status == 403:
			pr.Detail = fmt.Sprintf("auth rejected (HTTP %d): the key is wrong, expired, or for another product", re.Status)
		case re.Status == 402:
			pr.Detail = "HTTP 402: quota or billing problem on the provider account"
		case re.Status == 429:
			pr.Detail = "rate limited (HTTP 429): the server would cool this provider down"
		case re.Status >= 500:
			pr.Detail = fmt.Sprintf("provider error (HTTP %d)", re.Status)
		case re.Status >= 400:
			pr.Detail = fmt.Sprintf("HTTP %d: %v", re.Status, re.Err)
		default:
			pr.Detail = re.Err.Error()
		}
		if errors.Is(re.Err, context.DeadlineExceeded) {
			pr.Detail = "no answer within the probe timeout"
		}
		return pr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		pr.Detail = "no answer within the probe timeout"
		return pr
	}
	pr.Detail = err.Error()
	return pr
}

// finishDoctor tallies the summary from every check and provider row.
func finishDoctor(rep *doctorReport) {
	count := func(level string) {
		switch level {
		case levelOK:
			rep.Summary.OK++
		case levelWarn:
			rep.Summary.Warn++
		case levelFail:
			rep.Summary.Fail++
		}
	}
	for _, c := range rep.Checks {
		count(c.Level)
	}
	for _, p := range rep.Providers {
		count(p.Level)
	}
}

const doctorRule = "────────────────────────────────────────────────────────────────"

// renderDoctor prints the human report in the receipt's plain style.
func renderDoctor(w io.Writer, rep doctorReport, opts doctorOptions) {
	fmt.Fprintf(w, "\nfrugal doctor · %s\n", rep.Version)
	fmt.Fprintf(w, "config: %s\n", rep.ConfigSource)

	fmt.Fprintln(w, doctorRule)
	fmt.Fprintf(w, "%-8s %-14s %-15s %9s %12s  %s\n", "tool", "provider", "status", "price", "budget", "")
	for _, p := range rep.Providers {
		budget := "-"
		if p.DailyBudgetUSD > 0 {
			budget = fmt.Sprintf("%s/%s", usd(p.SpentTodayUSD), usd(p.DailyBudgetUSD))
		} else if p.SpentTodayUSD > 0 {
			budget = usd(p.SpentTodayUSD) + " today"
		}
		price := "free"
		if p.Paid {
			price = usd(p.CostPerCall)
		}
		status := p.Status
		if p.Probe != nil {
			switch p.Probe.Status {
			case "ok":
				status = fmt.Sprintf("ok %dms", p.Probe.LatencyMS)
			case "empty":
				status = "empty"
			case "error":
				status = "probe failed"
			case "skipped":
				status = "ready (unprobed)"
			}
		}
		fmt.Fprintf(w, "%-8s %-14s %-15s %9s %12s  %s\n", p.Capability, p.Name, status, price, budget, levelMark(p.Level))
		if p.Detail != "" {
			fmt.Fprintf(w, "         %s\n", p.Detail)
		}
		if p.Probe != nil && p.Probe.Detail != "" && p.Probe.Status != "ok" {
			fmt.Fprintf(w, "         probe: %s\n", p.Probe.Detail)
		}
	}
	fmt.Fprintln(w, doctorRule)

	// Group the other checks by area, in a fixed order.
	order := []string{"config", "limits", "routing", "cache", "ledger", "tools", "clients"}
	byArea := map[string][]doctorCheck{}
	for _, c := range rep.Checks {
		byArea[c.Area] = append(byArea[c.Area], c)
	}
	var extra []string
	for a := range byArea {
		found := false
		for _, o := range order {
			if o == a {
				found = true
				break
			}
		}
		if !found {
			extra = append(extra, a)
		}
	}
	sort.Strings(extra)
	for _, a := range append(order, extra...) {
		for _, c := range byArea[a] {
			fmt.Fprintf(w, "%-4s %-9s %-18s %s\n", levelMark(c.Level), c.Area, c.Name, c.Detail)
		}
	}
	fmt.Fprintln(w, doctorRule)
	if rep.Probed {
		fmt.Fprintf(w, "probes: live requests made; spent %s\n", usd(rep.ProbeCostUSD))
	} else {
		fmt.Fprintln(w, "probes: none (pass --probe for a live check of every free provider)")
	}
	fmt.Fprintf(w, "%d ok · %d warning(s) · %d failure(s)\n", rep.Summary.OK, rep.Summary.Warn, rep.Summary.Fail)
	switch {
	case rep.Summary.Fail > 0:
		fmt.Fprintln(w, "verdict: not healthy. fix the FAIL rows above before routing agent traffic.")
	case rep.Summary.Warn > 0 && opts.Strict:
		fmt.Fprintln(w, "verdict: warnings present and --strict is set.")
	case rep.Summary.Warn > 0:
		fmt.Fprintln(w, "verdict: healthy, with warnings worth a look.")
	default:
		fmt.Fprintln(w, "verdict: healthy.")
	}
}

func levelMark(level string) string {
	switch level {
	case levelOK:
		return "ok"
	case levelWarn:
		return "warn"
	case levelFail:
		return "FAIL"
	case levelSkip:
		return "-"
	}
	return level
}
