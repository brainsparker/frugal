package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/install"
	"github.com/frugalsh/frugal/internal/ledger"
	"github.com/frugalsh/frugal/internal/routing"
)

// writeDoctorConfig points FRUGAL_CONFIG at a temp models.yaml.
func writeDoctorConfig(t *testing.T, yaml string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "models.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FRUGAL_CONFIG", p)
	return p
}

// clearProviderEnv makes sure the host's keys never leak into a test.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"SERPER_API_KEY", "BRAVE_API_KEY", "YDC_API_KEY", "FIRECRAWL_API_KEY", "BROWSERLESS_TOKEN", "SEARXNG_URL", "JINA_API_KEY", "FRUGAL_STATS", "FRUGAL_LOG_LEVEL"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func findProvider(t *testing.T, rep doctorReport, capability, name string) providerCheck {
	t.Helper()
	for _, p := range rep.Providers {
		if p.Capability == capability && p.Name == name {
			return p
		}
	}
	t.Fatalf("provider %s/%s missing from report", capability, name)
	return providerCheck{}
}

func findCheck(rep doctorReport, area, name string) (doctorCheck, bool) {
	for _, c := range rep.Checks {
		if c.Area == area && c.Name == name {
			return c, true
		}
	}
	return doctorCheck{}, false
}

func TestRunDoctor_UsageErrors(t *testing.T) {
	isolatedHome(t)
	clearProviderEnv(t)
	for _, args := range [][]string{
		{"--nope"},
		{"--timeout", "0s"},
		{"--timeout", "-1s"},
		{"--probe-url", "not a url"},
	} {
		_, _ = capture(t, func() {
			if got := runDoctor(args); got != 2 {
				t.Errorf("runDoctor(%v) = %d, want 2", args, got)
			}
		})
	}
	// main dispatches the subcommand and help mentions it.
	out, _ := capture(t, printHelp)
	if !strings.Contains(out, "frugal doctor") {
		t.Error("help does not mention frugal doctor")
	}
}

func TestDoctor_DefaultConfigIsHealthy(t *testing.T) {
	isolatedHome(t)
	clearProviderEnv(t)
	var code int
	out, _ := capture(t, func() { code = runDoctor([]string{"--strict"}) })
	if code != 0 {
		t.Fatalf("exit %d on a default config, want 0; output:\n%s", code, out)
	}
	for _, want := range []string{
		"frugal doctor",
		"search   youcom-free    ready",
		"search   serper         missing_key",
		"set SERPER_API_KEY to enable",
		"extract  goreadability  ready",
		"frugal__search",
		"probes: none",
		"verdict: healthy",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}

	// JSON shape.
	out, _ = capture(t, func() { code = runDoctor([]string{"--json"}) })
	if code != 0 {
		t.Fatalf("--json exit %d", code)
	}
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("decode --json: %v\n%s", err, out)
	}
	if rep.Summary.Fail != 0 || rep.Probed || len(rep.Providers) == 0 {
		t.Errorf("unexpected report: fail=%d probed=%v providers=%d", rep.Summary.Fail, rep.Probed, len(rep.Providers))
	}
	p := findProvider(t, rep, "search", "serper")
	if p.Status != "missing_key" || p.KeyEnv != "SERPER_API_KEY" || !p.Paid {
		t.Errorf("serper row = %+v", p)
	}
	if p := findProvider(t, rep, "search", "wikipedia"); p.Status != "ready" || p.Level != levelOK {
		t.Errorf("wikipedia row = %+v", p)
	}
}

func TestDoctor_KeyHygieneWarnings(t *testing.T) {
	isolatedHome(t)
	clearProviderEnv(t)
	t.Setenv("SERPER_API_KEY", " abcdefghijklmnop ")
	t.Setenv("BRAVE_API_KEY", "your-key-here")
	t.Setenv("YDC_API_KEY", "Bearer abcdefghijklmnop")
	t.Setenv("FIRECRAWL_API_KEY", "")

	rep := diagnose(doctorOptions{})
	cases := map[string]string{
		"serper": "whitespace",
		"brave":  "placeholder",
		"youcom": "Bearer",
	}
	for name, want := range cases {
		p := findProvider(t, rep, "search", name)
		if p.Status != "ready" {
			t.Errorf("%s: status %q, want ready (key is set)", name, p.Status)
		}
		if p.Level != levelWarn || !strings.Contains(p.Detail, want) {
			t.Errorf("%s: level %q detail %q, want warn mentioning %q", name, p.Level, p.Detail, want)
		}
	}
	// Set-but-empty is called out distinctly from unset.
	fc := findProvider(t, rep, "extract", "firecrawl")
	if fc.Status != "missing_key" || !strings.Contains(fc.Detail, "set but empty") {
		t.Errorf("firecrawl row = %+v", fc)
	}
	// Warnings alone pass, --strict fails.
	var code int
	_, _ = capture(t, func() { code = runDoctor(nil) })
	if code != 0 {
		t.Errorf("warnings exit %d, want 0", code)
	}
	_, _ = capture(t, func() { code = runDoctor([]string{"--strict"}) })
	if code != 1 {
		t.Errorf("--strict with warnings exit %d, want 1", code)
	}
}

func TestDoctor_ConfigFindings(t *testing.T) {
	isolatedHome(t)
	clearProviderEnv(t)
	writeDoctorConfig(t, `
search_providers:
  wikipedia:
    cost_per_call: 0
  serper:
    api_key_env: SERPER_API_KEY
    cost_per_call: 0.001
    daily_budget_usd: 0.005
  mystery:
    api_key_env: MYSTERY_KEY
    cost_per_call: 0
extract_providers:
  goreadability:
    cost_per_call: 0
  jina:
    enabled: false
    cost_per_call: 0
routing:
  cooldown: soon
  loop_window: later
  search:
    order: [serper]
    deny: [youcom]
  domains:
    deny_private: true
cache:
  enabled: true
  search_ttl: forever
limits:
  call_timeout: 0s
  max_chars: 40000
`)
	rep := diagnose(doctorOptions{})

	if p := findProvider(t, rep, "search", "mystery"); p.Status != "unknown_driver" || p.Level != levelWarn {
		t.Errorf("mystery row = %+v", p)
	}
	if p := findProvider(t, rep, "extract", "jina"); p.Status != "disabled" || p.Level != levelSkip {
		t.Errorf("jina row = %+v", p)
	}
	wantWarn := map[[2]string]string{
		{"routing", "cooldown"}:     "not a duration",
		{"routing", "loop_window"}:  "not a positive duration",
		{"routing", "search.order"}: `"serper"`,
		{"routing", "search.deny"}:  `"youcom"`,
		{"cache", "search_ttl"}:     "not a duration",
		{"limits", "call_timeout"}:  "disabled",
	}
	for key, want := range wantWarn {
		c, ok := findCheck(rep, key[0], key[1])
		if !ok {
			t.Errorf("check %v missing", key)
			continue
		}
		if c.Level != levelWarn || !strings.Contains(c.Detail, want) {
			t.Errorf("check %v = %+v, want warn containing %q", key, c, want)
		}
	}
	if c, _ := findCheck(rep, "routing", "domains"); c.Level != levelOK || !strings.Contains(c.Detail, "private") {
		t.Errorf("domains check = %+v", c)
	}
	if c, _ := findCheck(rep, "limits", "max_chars"); c.Level != levelOK || !strings.Contains(c.Detail, "40000") {
		t.Errorf("max_chars check = %+v", c)
	}
	if c, _ := findCheck(rep, "tools", "frugal__browse"); c.Level != levelSkip {
		t.Errorf("browse coverage = %+v", c)
	}
}

func TestDoctor_NoProvidersIsFailure(t *testing.T) {
	isolatedHome(t)
	clearProviderEnv(t)
	writeDoctorConfig(t, `
search_providers:
  wikipedia:
    enabled: false
    cost_per_call: 0
  marginalia:
    enabled: false
    cost_per_call: 0
  youcom-free:
    enabled: false
    cost_per_call: 0
extract_providers:
  goreadability:
    enabled: false
    cost_per_call: 0
  jina:
    enabled: false
    cost_per_call: 0
`)
	var code int
	out, _ := capture(t, func() { code = runDoctor(nil) })
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "no provider is ready in any table") || !strings.Contains(out, "verdict: not healthy") {
		t.Errorf("output:\n%s", out)
	}
}

func TestDoctor_BrokenConfigFails(t *testing.T) {
	isolatedHome(t)
	clearProviderEnv(t)
	writeDoctorConfig(t, "search_providers: [not, a, map]\n")
	var code int
	out, _ := capture(t, func() { code = runDoctor([]string{"--json"}) })
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if c, ok := findCheck(rep, "config", "load"); !ok || c.Level != levelFail {
		t.Errorf("config load check = %+v (found %v)", c, ok)
	}
}

func TestDoctor_LedgerBudgetColumn(t *testing.T) {
	home := isolatedHome(t)
	clearProviderEnv(t)
	t.Setenv("SERPER_API_KEY", "abcdefghijklmnop")
	writeDoctorConfig(t, `
search_providers:
  serper:
    api_key_env: SERPER_API_KEY
    cost_per_call: 0.001
    daily_budget_usd: 0.002
  wikipedia:
    cost_per_call: 0
extract_providers:
  goreadability:
    cost_per_call: 0
`)
	now := time.Date(2026, 10, 29, 15, 0, 0, 0, time.UTC)
	dir := filepath.Join(home, ".frugal", "usage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	recs := []ledger.Record{
		{TS: now.Add(-2 * time.Hour), Tool: "search", Provider: "serper", CostUSD: 0.001, Won: true, OK: true},
		{TS: now.Add(-1 * time.Hour), Tool: "search", Provider: "serper", CostUSD: 0.001, Won: true, OK: true},
		{TS: now.Add(-30 * time.Hour), Tool: "search", Provider: "serper", CostUSD: 0.001, Won: true, OK: true}, // yesterday
		{TS: now.Add(-1 * time.Hour), Tool: "search", Provider: "wikipedia", CostUSD: 0, Won: true, OK: true},
	}
	var lines []string
	for _, r := range recs {
		b, _ := json.Marshal(r)
		lines = append(lines, string(b))
	}
	if err := os.WriteFile(filepath.Join(dir, ledger.MonthFile(now)), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rep := diagnose(doctorOptions{now: func() time.Time { return now }})
	p := findProvider(t, rep, "search", "serper")
	if p.SpentTodayUSD < 0.0019 || p.SpentTodayUSD > 0.0021 {
		t.Errorf("spent today = %v, want 0.002 (yesterday excluded)", p.SpentTodayUSD)
	}
	if p.BudgetLeftUSD == nil || *p.BudgetLeftUSD != 0 {
		t.Errorf("budget left = %v, want 0", p.BudgetLeftUSD)
	}
	if p.Level != levelWarn || !strings.Contains(p.Detail, "exhausted") {
		t.Errorf("serper row = %+v, want exhausted warning", p)
	}
	if c, _ := findCheck(rep, "ledger", "usage"); c.Level != levelOK || !strings.Contains(c.Detail, "4 call(s) this month, 3 today") {
		t.Errorf("ledger check = %+v", c)
	}

	// Rendered budget column.
	var out string
	out, _ = capture(t, func() { renderDoctor(os.Stdout, rep, doctorOptions{}) })
	if !strings.Contains(out, "$0.0020/$0.0020") {
		t.Errorf("budget column missing:\n%s", out)
	}

	// FRUGAL_STATS=off is a warning: budgets have no memory across restarts.
	t.Setenv("FRUGAL_STATS", "off")
	rep = diagnose(doctorOptions{now: func() time.Time { return now }})
	if c, _ := findCheck(rep, "ledger", "usage"); c.Level != levelWarn {
		t.Errorf("ledger check with FRUGAL_STATS=off = %+v", c)
	}
}

func TestDoctor_Probes(t *testing.T) {
	isolatedHome(t)
	clearProviderEnv(t)

	// Serper (paid): healthy, answers one organic hit, counts the request.
	serperHits := 0
	serperSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serperHits++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"organic": []map[string]string{{"title": "frugal", "link": "https://frugal.sh", "snippet": "routing layer"}},
		})
	}))
	defer serperSrv.Close()
	// Brave (paid): the key is rejected.
	braveSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid key"}`, http.StatusUnauthorized)
	}))
	defer braveSrv.Close()
	// Wikipedia (free): provider-side outage.
	wikiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer wikiSrv.Close()
	// Jina (free extract): healthy render of the probe URL.
	jinaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200, "status": 20000,
			"data": map[string]any{"title": "Example", "url": r.URL.Path, "content": "# Example\n\nhello", "httpStatus": 200},
		})
	}))
	defer jinaSrv.Close()
	// The probe URL itself, for go-readability's local fetch.
	pageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><title>Probe</title></head><body><article><p>" + strings.Repeat("Readable paragraph text. ", 40) + "</p></article></body></html>"))
	}))
	defer pageSrv.Close()

	t.Setenv("SERPER_API_KEY", "abcdefghijklmnop")
	t.Setenv("BRAVE_API_KEY", "abcdefghijklmnop")
	t.Setenv("SERPER_URL", serperSrv.URL)
	t.Setenv("BRAVE_URL", braveSrv.URL)
	t.Setenv("WIKI_URL", wikiSrv.URL)
	t.Setenv("JINA_URL", jinaSrv.URL)
	writeDoctorConfig(t, `
search_providers:
  serper:
    api_key_env: SERPER_API_KEY
    base_url_env: SERPER_URL
    cost_per_call: 0.001
  brave:
    api_key_env: BRAVE_API_KEY
    base_url_env: BRAVE_URL
    cost_per_call: 0.005
  wikipedia:
    base_url_env: WIKI_URL
    cost_per_call: 0
  marginalia:
    enabled: false
    cost_per_call: 0
  youcom-free:
    enabled: false
    cost_per_call: 0
extract_providers:
  goreadability:
    cost_per_call: 0
  jina:
    base_url_env: JINA_URL
    cost_per_call: 0
`)
	// Retries would make the 503 probe wait through the backoff schedule.
	prev := routing.Retries()
	routing.SetRetries(0)
	defer routing.SetRetries(prev)

	// --probe alone: free providers only, paid rows marked unprobed.
	rep := diagnose(doctorOptions{Probe: true, ProbeURL: pageSrv.URL, Timeout: 5 * time.Second})
	if !rep.Probed || serperHits != 0 {
		t.Fatalf("probe touched a paid provider without --probe-paid (serper hits=%d)", serperHits)
	}
	if p := findProvider(t, rep, "search", "serper"); p.Probe == nil || p.Probe.Status != "skipped" || p.Level != levelOK {
		t.Errorf("serper row = %+v probe=%+v", p, p.Probe)
	}
	if p := findProvider(t, rep, "search", "wikipedia"); p.Probe == nil || p.Probe.Status != "error" || p.Probe.HTTPCode != 503 || p.Level != levelWarn {
		t.Errorf("wikipedia row = %+v probe=%+v (503 should warn, not fail)", p, p.Probe)
	}
	if p := findProvider(t, rep, "extract", "jina"); p.Probe == nil || p.Probe.Status != "ok" || p.Level != levelOK {
		t.Errorf("jina row = %+v probe=%+v", p, p.Probe)
	}
	if p := findProvider(t, rep, "extract", "goreadability"); p.Probe == nil || p.Probe.Status != "ok" {
		t.Errorf("goreadability row = %+v probe=%+v", p, p.Probe)
	}
	if rep.Summary.Fail != 0 {
		t.Errorf("fail count %d, want 0 (only transient trouble so far)", rep.Summary.Fail)
	}

	// --probe-paid: serper is billed once and passes, brave's 401 fails.
	rep = diagnose(doctorOptions{Probe: true, ProbePaid: true, ProbeURL: pageSrv.URL, Timeout: 5 * time.Second})
	if serperHits != 1 {
		t.Errorf("serper hits = %d, want exactly 1", serperHits)
	}
	if p := findProvider(t, rep, "search", "serper"); p.Probe == nil || p.Probe.Status != "ok" || p.Probe.CostUSD != 0.001 {
		t.Errorf("serper row = %+v probe=%+v", p, p.Probe)
	}
	if p := findProvider(t, rep, "search", "brave"); p.Probe == nil || p.Probe.Status != "error" || p.Probe.HTTPCode != 401 || p.Level != levelFail || !strings.Contains(p.Probe.Detail, "auth rejected") {
		t.Errorf("brave row = %+v probe=%+v", p, p.Probe)
	}
	if rep.ProbeCostUSD != 0.001 {
		t.Errorf("probe cost = %v, want 0.001", rep.ProbeCostUSD)
	}
	if rep.Summary.Fail == 0 {
		t.Error("a 401 on a configured key should be a failure")
	}
	out, _ := capture(t, func() { renderDoctor(os.Stdout, rep, doctorOptions{Probe: true, ProbePaid: true}) })
	for _, want := range []string{"probe failed", "auth rejected (HTTP 401)", "spent $0.0010", "verdict: not healthy"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n%s", want, out)
		}
	}

	// The CLI wires the flags and exit code through.
	var code int
	_, _ = capture(t, func() {
		code = runDoctor([]string{"--probe-paid", "--probe-url", pageSrv.URL, "--timeout", "5s", "--json"})
	})
	if code != 1 {
		t.Errorf("runDoctor --probe-paid exit %d, want 1 (brave 401)", code)
	}
}

func TestClassifyProbe(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus string
		wantKind   string
		wantSubstr string
	}{
		{"ok", nil, "ok", "", ""},
		{"empty", errEmpty, "empty", "", "no content"},
		{"401", routing.Permanent("x", 401, errors.New("nope")), "error", "permanent", "auth rejected"},
		{"402", routing.Permanent("x", 402, errors.New("pay")), "error", "permanent", "billing"},
		{"429", routing.Transient("x", 429, errors.New("slow down")), "error", "transient", "rate limited"},
		{"503", routing.Transient("x", 503, errors.New("down")), "error", "transient", "provider error"},
		{"timeout", routing.Transient("x", 0, context.DeadlineExceeded), "error", "transient", "probe timeout"},
		{"plain timeout", context.DeadlineExceeded, "error", "", "probe timeout"},
		{"other", errors.New("boom"), "error", "", "boom"},
	}
	for _, c := range cases {
		pr := classifyProbe(c.err, 0, "1 hit(s)", 5*time.Millisecond)
		if pr.Status != c.wantStatus || pr.Kind != c.wantKind || !strings.Contains(pr.Detail, c.wantSubstr) {
			t.Errorf("%s: got %+v", c.name, pr)
		}
	}
}

func TestKeyWarnings(t *testing.T) {
	if w := keyWarnings("K", "sk-abcdefghijklmnop"); len(w) != 0 {
		t.Errorf("clean key warned: %v", w)
	}
	for _, c := range []struct{ v, want string }{
		{" sk-abcdefghijklmnop", "whitespace"},
		{`"sk-abcdefghijklmnop"`, "quotes"},
		{"Bearer sk-abcdefghijklmnop", "Bearer"},
		{"<paste-your-key>", "placeholder"},
		{"abc", "shorter"},
	} {
		w := keyWarnings("K", c.v)
		if len(w) == 0 || !strings.Contains(strings.Join(w, " "), c.want) {
			t.Errorf("%q: got %v, want %q", c.v, w, c.want)
		}
	}
}

func TestClientCheck(t *testing.T) {
	dir := t.TempDir()
	configured := filepath.Join(dir, "a.json")
	_ = os.WriteFile(configured, []byte(`{"mcpServers":{"frugal":{"command":"frugal"}}}`), 0o600)
	bare := filepath.Join(dir, "b.json")
	_ = os.WriteFile(bare, []byte(`{"mcpServers":{}}`), 0o600)
	broken := filepath.Join(dir, "c.json")
	_ = os.WriteFile(broken, []byte(`{`), 0o600)

	cases := []struct {
		c         install.Client
		wantLevel string
		wantSub   string
	}{
		{install.Client{ID: "cursor", Kind: install.KindJSONFile, Detected: false, DetectionReason: "no dir"}, levelSkip, "not detected"},
		{install.Client{ID: "cursor", Kind: install.KindJSONFile, Detected: true, ConfigPath: configured}, levelOK, "frugal configured"},
		{install.Client{ID: "cursor", Kind: install.KindJSONFile, Detected: true, ConfigPath: bare}, levelWarn, "frugal mcp install --client cursor"},
		{install.Client{ID: "cursor", Kind: install.KindJSONFile, Detected: true, ConfigPath: broken}, levelWarn, "not valid JSON"},
		{install.Client{ID: "cursor", Kind: install.KindJSONFile, Detected: true, ConfigPath: filepath.Join(dir, "missing.json")}, levelWarn, "not readable"},
		{install.Client{ID: "claude-code", Kind: install.KindCLI, Detected: true, DetectionReason: "claude CLI at /usr/bin/claude"}, levelOK, "claude mcp list"},
	}
	for i, c := range cases {
		_, _, level, detail := clientCheck(c.c)
		if level != c.wantLevel || !strings.Contains(detail, c.wantSub) {
			t.Errorf("case %d: level %q detail %q, want %q containing %q", i, level, detail, c.wantLevel, c.wantSub)
		}
	}
}
