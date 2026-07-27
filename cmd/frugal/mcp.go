package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/frugalsh/frugal/internal/browse"
	"github.com/frugalsh/frugal/internal/config"
	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/install"
	"github.com/frugalsh/frugal/internal/ledger"
	"github.com/frugalsh/frugal/internal/mcp"
	"github.com/frugalsh/frugal/internal/mcp/tools"
	"github.com/frugalsh/frugal/internal/obs"
	"github.com/frugalsh/frugal/internal/provider/browserless"
	"github.com/frugalsh/frugal/internal/provider/firecrawl"
	"github.com/frugalsh/frugal/internal/provider/goreadability"
	"github.com/frugalsh/frugal/internal/provider/marginalia"
	"github.com/frugalsh/frugal/internal/provider/searxng"
	"github.com/frugalsh/frugal/internal/provider/serper"
	"github.com/frugalsh/frugal/internal/provider/wikipedia"
	"github.com/frugalsh/frugal/internal/provider/youcom"
	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// runMCP dispatches the `frugal mcp <subcommand>` family. Returns the
// process exit code.
//
// Subcommands:
//   - serve:   run Frugal as an MCP server (stdio default; --http ADDR for Streamable HTTP)
//   - install: write MCP server config into Claude Desktop / Cursor / AnythingLLM / Claude Code
//
// Anything else falls through to a usage error.
func runMCP(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: frugal mcp <serve|install> [flags]")
		return 2
	}
	switch args[0] {
	case "serve":
		return runMCPServe(args[1:])
	case "install":
		return runMCPInstall(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown mcp subcommand %q (want serve | install)\n", args[0])
		return 2
	}
}

// runMCPServe runs the MCP server. stdio is the default — what Claude
// Desktop, Claude Code, and Cursor consume for locally-installed servers.
// --http ADDR switches to Streamable HTTP for remote deployments and HTTP
// clients; --allow-anon binds must be loopback (e.g. 127.0.0.1:8765),
// anything wider needs FRUGAL_AUTH_TOKEN.
//
// Both transports honor SIGINT / SIGTERM with a graceful shutdown.
func runMCPServe(args []string) int {
	fs := flag.NewFlagSet("mcp serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	httpAddr := fs.String("http", "", "if set, serve over Streamable HTTP on this address instead of stdio (e.g. 127.0.0.1:8765 with --allow-anon; non-local binds require FRUGAL_AUTH_TOKEN)")
	allowAnon := fs.Bool("allow-anon", false, "permit --http to run without FRUGAL_AUTH_TOKEN (foot-gun: only for localhost or behind a trusted proxy)")
	rateLimit := fs.Int("rate-limit-rpm", 600, "per-IP request budget per minute when serving --http (0 disables)")
	reqTimeout := fs.Duration("request-timeout", 30*time.Second, "per-request timeout when serving --http (0 disables)")
	maxRequestBytes := fs.Int64("max-request-bytes", 1<<20, "reject HTTP requests above this Content-Length when serving --http (0 disables)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: frugal mcp serve [--http ADDR]")
		fmt.Fprintln(os.Stderr, "Run Frugal as an MCP server. Default transport is stdio")
		fmt.Fprintln(os.Stderr, "(what Claude Desktop, Claude Code, and Cursor consume).")
		fmt.Fprintln(os.Stderr, "Pass --http ADDR for Streamable HTTP (remote / HTTP clients).")
		fmt.Fprintln(os.Stderr, "Set FRUGAL_AUTH_TOKEN to enable bearer-token auth — required for any")
		fmt.Fprintln(os.Stderr, "non-local bind. Or pass --allow-anon to serve unauthenticated, which")
		fmt.Fprintln(os.Stderr, "additionally requires a loopback address (e.g. --http 127.0.0.1:8765).")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, cfgSrc, err := config.LoadAuto()
	if err != nil {
		// stdio mode keeps stdout free of non-JSON bytes — failure logs go to
		// stderr, which is the contract every MCP client respects.
		fmt.Fprintf(os.Stderr, "frugal mcp serve: load config: %v\n", err)
		return 1
	}
	slog.Info("mcp serve: config loaded", "source", cfgSrc)

	// stdio transport is single-session; logging to stderr is safe and
	// preserves the MCP newline-delimited JSON-RPC contract on stdout.
	srv := mcp.New("frugal", version(), slog.Default())

	metrics := obs.NewMetrics()
	if ledger.Enabled() {
		if dir, lerr := ledger.Dir(); lerr == nil {
			w := ledger.NewWriter(dir, rackRates(cfg), func(err error) {
				slog.Warn("usage ledger write failed; frugal stats will undercount", "err", err)
			})
			metrics.SetSink(func(tool, provider string, latency time.Duration, costUSD float64, won bool, err error) {
				w.Record(tool, provider, latency, costUSD, won, err == nil)
			})
		} else {
			slog.Warn("mcp serve: usage ledger disabled — cannot resolve home dir", "err", lerr)
		}
	}
	// Routing policies: absent config keeps the cheap default per
	// capability. The latency lookup feeds the fast strategy from the
	// local ledger; when the ledger is off, fast degrades to cost order.
	latFor := func(string) routing.LatencyLookup { return nil }
	if ledger.Enabled() {
		if dir, lerr := ledger.Dir(); lerr == nil {
			lc := newLatencyCache(dir, 5*time.Minute)
			latFor = lc.lookup
		}
	}
	policies := map[string]routing.Policy{
		"search":  policyFor(cfg.Routing, "search"),
		"extract": policyFor(cfg.Routing, "extract"),
		"browse":  policyFor(cfg.Routing, "browse"),
	}
	if cfg.Routing != nil {
		for _, capability := range []string{"search", "extract", "browse"} {
			p := policies[capability]
			if p.Strategy == routing.StrategyCheap && len(p.Order) == 0 && len(p.Deny) == 0 {
				continue
			}
			slog.Info("mcp serve: routing policy",
				"capability", capability,
				"strategy", p.Strategy.String(),
				"order", p.Order,
				"deny", denyNames(p.Deny))
		}
	}

	searchers := buildSearchers(cfg)
	warnPolicyStrangers("search", policies["search"], searcherNames(searchers))
	tools.RegisterSearch(srv.Inner, searchers, metrics,
		tools.WithPolicy(policies["search"]), tools.WithLatencyLookup(latFor("search")))
	if len(searchers) == 0 {
		slog.Warn("mcp serve: no search providers configured — frugal__search will not be advertised. " +
			"Set SEARXNG_URL (free, self-hosted), SERPER_API_KEY, or YDC_API_KEY to enable.")
	} else {
		slog.Info("mcp serve: frugal__search registered", "providers", searcherNames(searchers))
	}

	extractors := buildExtractors(cfg)
	warnPolicyStrangers("extract", policies["extract"], extractorNames(extractors))
	tools.RegisterExtract(srv.Inner, extractors, metrics,
		tools.WithPolicy(policies["extract"]), tools.WithLatencyLookup(latFor("extract")))
	if len(extractors) > 0 {
		slog.Info("mcp serve: frugal__extract registered", "providers", extractorNames(extractors))
	}

	browsers := buildBrowsers(cfg)
	warnPolicyStrangers("browse", policies["browse"], browserNames(browsers))
	tools.RegisterBrowse(srv.Inner, browsers, metrics,
		tools.WithPolicy(policies["browse"]), tools.WithLatencyLookup(latFor("browse")))
	if len(browsers) > 0 {
		slog.Info("mcp serve: frugal__browse registered", "providers", browserNames(browsers))
	}

	tools.RegisterExecute(srv.Inner, searchers, extractors, browsers, metrics,
		tools.WithPolicies(policies), tools.WithLatencyLookupFor(latFor))
	if len(searchers) > 0 {
		slog.Info("mcp serve: frugal__execute registered",
			"search", len(searchers), "extract", len(extractors), "browse", len(browsers))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Periodic INFO dump of accumulated cost / latency / errors. Skipped
	// on idle intervals so quiet sessions stay quiet.
	go logMetricsPeriodically(ctx, metrics, 60*time.Second)

	if *httpAddr != "" {
		opts := mcp.HTTPOptions{
			AuthToken:          os.Getenv("FRUGAL_AUTH_TOKEN"),
			AllowAnon:          *allowAnon,
			RateLimitPerMinute: *rateLimit,
			Metrics:            metrics,
			RequestTimeout:     *reqTimeout,
			MaxRequestBytes:    *maxRequestBytes,
		}
		if err := srv.ServeHTTP(ctx, *httpAddr, opts); err != nil {
			fmt.Fprintf(os.Stderr, "frugal mcp serve: %v\n", err)
			return 1
		}
		return 0
	}
	if err := srv.ServeStdio(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "frugal mcp serve: %v\n", err)
		return 1
	}
	return 0
}

// runMCPInstall writes the `frugal` MCP server entry into each detected
// agent client's config — Claude Desktop, Cursor, and AnythingLLM merge
// into a JSON file; Claude Code is registered by shelling out to `claude
// mcp add` (the `claude` CLI manages its own config), falling back to
// printing the command.
//
// Flags:
//   - --client <id|all>  install only into the named client (default: all detected)
//   - --print            print the plan without writing (dry-run)
//   - --yes              skip the confirmation prompt
func runMCPInstall(args []string) int {
	fs := flag.NewFlagSet("mcp install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	clientID := fs.String("client", "all", "install into a specific client (claude-desktop | cursor | anythingllm | claude-code | all)")
	printOnly := fs.Bool("print", false, "print the plan without writing")
	assumeYes := fs.Bool("yes", false, "skip the confirmation prompt")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: frugal mcp install [flags]")
		fmt.Fprintln(os.Stderr, "Wire 'frugal' as an MCP server in agent clients (Claude Desktop, Cursor,")
		fmt.Fprintln(os.Stderr, "AnythingLLM, Claude Code). For a self-hosted or Docker AnythingLLM, set")
		fmt.Fprintln(os.Stderr, "ANYTHINGLLM_STORAGE_DIR to its storage directory first.")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	binPath, err := install.FrugalBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal mcp install: %v\n", err)
		return 1
	}

	env := providerEnvVars()

	clients := install.DetectClients()
	targets, err := filterClients(clients, *clientID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal mcp install: %v\n", err)
		return 2
	}

	fmt.Fprintln(os.Stderr, "detected agent clients:")
	for _, c := range clients {
		mark := "✗"
		if c.Detected {
			mark = "✓"
		}
		fmt.Fprintf(os.Stderr, "  %s %-15s %s\n", mark, c.Title, c.DetectionReason)
	}
	fmt.Fprintln(os.Stderr)

	if len(targets) == 0 {
		fmt.Fprintln(os.Stderr, "no clients selected for install.")
		if *clientID == "all" {
			fmt.Fprintln(os.Stderr, "(no detected clients — install Claude Desktop, Cursor, AnythingLLM, or")
			fmt.Fprintln(os.Stderr, " Claude Code first, or pass --client X to force install into one anyway.)")
		}
		return 1
	}

	fmt.Fprintf(os.Stderr, "frugal binary: %s\n", binPath)
	fmt.Fprintln(os.Stderr, "planned changes:")
	for _, c := range targets {
		fmt.Fprintf(os.Stderr, "  - %s: %s\n", c.Title, install.PlanFor(c, binPath, env))
	}
	fmt.Fprintln(os.Stderr)

	if *printOnly {
		fmt.Fprintln(os.Stderr, "(--print set; no changes written.)")
		return 0
	}

	if !*assumeYes && !confirm("apply the changes above?") {
		fmt.Fprintln(os.Stderr, "aborted.")
		return 1
	}

	var hadErr bool
	var wroteJSON bool
	var wroteAnythingLLM bool
	for _, c := range targets {
		suggestion, err := install.Apply(c, binPath, env)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s: %v\n", c.Title, err)
			if suggestion != "" {
				fmt.Fprintf(os.Stderr, "  run this yourself:\n    %s\n", suggestion)
			}
			hadErr = true
			continue
		}
		switch c.Kind {
		case install.KindJSONFile:
			wroteJSON = true
			if c.ID == "anythingllm" {
				wroteAnythingLLM = true
			}
			fmt.Fprintf(os.Stderr, "✓ %s: wrote %s\n", c.Title, c.ConfigPath)
		case install.KindCLI:
			fmt.Fprintf(os.Stderr, "✓ %s: registered via `claude mcp add`\n", c.Title)
		}
	}
	if hadErr {
		return 1
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "next steps:")
	switch {
	case len(env) > 0 && wroteJSON:
		fmt.Fprintln(os.Stderr, "  1. provider settings found in this shell (keys / URLs) were copied into")
		fmt.Fprintln(os.Stderr, "     the GUI client configs (GUI apps don't read your shell rc); after")
		fmt.Fprintln(os.Stderr, "     rotating a key, re-run `frugal mcp install` to refresh the values")
	case len(env) > 0:
		fmt.Fprintln(os.Stderr, "  1. Claude Code spawns frugal from your shell, so it inherits your")
		fmt.Fprintln(os.Stderr, "     exported keys live — nothing needed baking into a config file")
	default:
		fmt.Fprintln(os.Stderr, "  1. optional: export SERPER_API_KEY and/or YDC_API_KEY, then re-run")
		fmt.Fprintln(os.Stderr, "     `frugal mcp install` so GUI clients get the keys too — zero-key")
		fmt.Fprintln(os.Stderr, "     search via Marginalia works without this step")
	}
	fmt.Fprintln(os.Stderr, "  2. restart the agent client to pick up the new MCP server")
	fmt.Fprintln(os.Stderr, "  3. look for the 'frugal__search' tool in the agent's tool picker")
	if wroteAnythingLLM {
		fmt.Fprintln(os.Stderr, "     (AnythingLLM: Agent Skills → MCP Servers, then call it from an")
		fmt.Fprintln(os.Stderr, "      @agent chat — MCP tools only load for agent sessions there)")
	}
	return 0
}

// policyFor maps one capability's YAML routing policy onto the routing
// package's Policy. A nil section (or nil Routing entirely) yields the
// zero value — cheap, nothing denied — the historical default.
func policyFor(rc *config.RoutingConfig, capability string) routing.Policy {
	var rp *config.RoutePolicy
	if rc != nil {
		switch capability {
		case "search":
			rp = rc.Search
		case "extract":
			rp = rc.Extract
		case "browse":
			rp = rc.Browse
		}
	}
	if rp == nil {
		return routing.Policy{}
	}
	p := routing.Policy{Order: append([]string(nil), rp.Order...)}
	switch strings.TrimSpace(rp.Strategy) {
	case "fast":
		p.Strategy = routing.StrategyFast
	case "premium":
		p.Strategy = routing.StrategyPremium
	}
	if len(rp.Deny) > 0 {
		p.Deny = make(map[string]bool, len(rp.Deny))
		for _, n := range rp.Deny {
			p.Deny[n] = true
		}
	}
	return p
}

// warnPolicyStrangers flags policy order/deny entries that name no
// registered provider. Config validation already rejected names foreign
// to the capability; this catches the softer case — a provider that's
// valid but didn't register (key unset, disabled) — which is worth a
// startup note, not an error.
func warnPolicyStrangers(capability string, p routing.Policy, registered []string) {
	if len(p.Order) == 0 && len(p.Deny) == 0 {
		return
	}
	have := make(map[string]bool, len(registered))
	for _, n := range registered {
		have[n] = true
	}
	for _, n := range p.Order {
		if !have[n] {
			slog.Warn("mcp serve: routing policy orders a provider that is not registered",
				"capability", capability, "provider", n)
		}
	}
	for n := range p.Deny {
		if !have[n] {
			slog.Warn("mcp serve: routing policy denies a provider that is not registered anyway",
				"capability", capability, "provider", n)
		}
	}
}

// denyNames renders a deny set as a sorted slice for logging.
func denyNames(deny map[string]bool) []string {
	out := make([]string, 0, len(deny))
	for n := range deny {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func searcherNames(searchers []search.Searcher) []string {
	out := make([]string, 0, len(searchers))
	for _, s := range searchers {
		out = append(out, s.Name())
	}
	return out
}

func extractorNames(extractors []extract.Extractor) []string {
	out := make([]string, 0, len(extractors))
	for _, e := range extractors {
		out = append(out, e.Name())
	}
	return out
}

func browserNames(browsers []browse.Browser) []string {
	out := make([]string, 0, len(browsers))
	for _, b := range browsers {
		out = append(out, b.Name())
	}
	return out
}

// latencyCache wraps ledger.LatencySnapshot behind a TTL so the fast
// strategy doesn't re-read the ledger files on every tool call. Safe
// for concurrent use; a snapshot failure is treated as "no data" and
// retried after the TTL.
type latencyCache struct {
	dir string
	ttl time.Duration

	mu   sync.Mutex
	at   time.Time
	snap map[string]map[string]routing.LatencyStat
}

func newLatencyCache(dir string, ttl time.Duration) *latencyCache {
	return &latencyCache{dir: dir, ttl: ttl}
}

// lookup returns the LatencyLookup for one capability's tool name.
func (c *latencyCache) lookup(tool string) routing.LatencyLookup {
	return func(provider string) (routing.LatencyStat, bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		now := time.Now()
		if c.at.IsZero() || now.Sub(c.at) > c.ttl {
			snap, err := ledger.LatencySnapshot(c.dir, now)
			if err != nil {
				snap = nil
			}
			c.snap = snap
			c.at = now
		}
		st, ok := c.snap[tool][provider]
		return st, ok
	}
}

// canonicalProviderOrder fixes the tie-break between same-cost providers:
// self-hosted first (the operator stood that instance up deliberately),
// then public-free, then paid by ascending list price. YAML maps don't
// preserve file order, so this list — not the config file — is what makes
// registration (and therefore OrderByCost's stable ties) deterministic.
// Providers not listed here sort last, by name.
var canonicalProviderOrder = []string{
	"searxng", "marginalia", "wikipedia", "serper", "youcom", // search
	"goreadability", "firecrawl", // extract
	"browserless", // browse
}

// wireableProviders names the providers each buildX switch can actually
// construct a driver for, per capability. Keep in sync with the switch
// cases in buildSearchers / buildExtractors / buildBrowsers: rackRates
// must not benchmark savings against a config entry the binary can never
// dispatch to.
var wireableProviders = map[string]map[string]bool{
	"search":  {"searxng": true, "marginalia": true, "wikipedia": true, "serper": true, "youcom": true},
	"extract": {"goreadability": true, "firecrawl": true},
	"browse":  {"browserless": true},
}

// rackRates derives each capability's premium rack rate — the max
// cost_per_call across the capability's configured providers — for the
// usage ledger's savings counterfactual. LoadAuto returns every YAML
// entry whether or not its API key is set, so rack rates work on a
// keyless install too. Entries the operator disabled with
// `enabled: false`, and entries naming providers the binary has no
// driver for, don't count: a savings number benchmarked against a
// provider that can never serve a call would be fiction.
func rackRates(cfg *config.Config) map[string]float64 {
	out := make(map[string]float64, 3)
	add := func(tool string, providers map[string]config.SearchProviderConfig) {
		for name, sp := range providers {
			if sp.Disabled() || !wireableProviders[tool][name] {
				continue
			}
			if sp.CostPerCall > out[tool] {
				out[tool] = sp.CostPerCall
			}
		}
	}
	add("search", cfg.SearchProviders)
	add("extract", cfg.ExtractProviders)
	add("browse", cfg.BrowseProviders)
	return out
}

// sortedProviderNames returns the provider map's keys in canonical order.
func sortedProviderNames(providers map[string]config.SearchProviderConfig) []string {
	rank := make(map[string]int, len(canonicalProviderOrder))
	for i, n := range canonicalProviderOrder {
		rank[n] = i
	}
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		ri, iKnown := rank[names[i]]
		rj, jKnown := rank[names[j]]
		switch {
		case iKnown && jKnown:
			return ri < rj
		case iKnown:
			return true
		case jKnown:
			return false
		default:
			return names[i] < names[j]
		}
	})
	return names
}

// providerEnvVars collects the env vars the active config can consume —
// every api_key_env / base_url_env on every provider — that are set in
// the installer's environment. Claude Desktop and Cursor spawn MCP
// servers without a login shell, so rc-file exports never reach them;
// copying the values into each client's env block is the only way paid
// fallback works there.
//
// The names come from config.LoadTrusted, NOT LoadAuto: a crafted
// config/models.yaml in an untrusted cwd must not get to name arbitrary
// secrets (AWS keys, GitHub tokens) for harvesting into client configs.
//
// FRUGAL_CONFIG itself is copied only when it demonstrably loads — and
// as an absolute path, because GUI clients spawn the server from `/`,
// where a checkout-relative value would silently recreate the
// crash-on-launch that the embedded default exists to fix.
func providerEnvVars() map[string]string {
	env := map[string]string{}
	cfg, _, err := config.LoadTrusted()
	if err == nil {
		for _, providers := range []map[string]config.SearchProviderConfig{
			cfg.SearchProviders, cfg.ExtractProviders, cfg.BrowseProviders,
		} {
			for _, p := range providers {
				// A provider tombstoned with `enabled: false` can never
				// register — baking its secret into client configs would
				// persist a credential for nothing.
				if p.Disabled() {
					continue
				}
				for _, name := range []string{p.APIKeyEnv, p.BaseURLEnv} {
					if name == "" {
						continue
					}
					if v := os.Getenv(name); v != "" {
						env[name] = v
					}
				}
			}
		}
	}
	if p := strings.TrimSpace(os.Getenv("FRUGAL_CONFIG")); p != "" {
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "warning: FRUGAL_CONFIG is set but fails to load (%v); not copying it into client configs\n", err)
		default:
			if abs, absErr := filepath.Abs(p); absErr == nil {
				env["FRUGAL_CONFIG"] = abs
			}
		}
	}
	return env
}

// filterClients narrows the catalog down to the install targets per the
// --client flag. "all" → every detected client. Specific ID → that one
// client whether detected or not (operator override). Unknown ID → error.
func filterClients(all []install.Client, want string) ([]install.Client, error) {
	if want == "" || want == "all" {
		var out []install.Client
		for _, c := range all {
			if c.Detected {
				out = append(out, c)
			}
		}
		return out, nil
	}
	for _, c := range all {
		if c.ID == want {
			return []install.Client{c}, nil
		}
	}
	known := make([]string, 0, len(all))
	for _, c := range all {
		known = append(known, c.ID)
	}
	return nil, fmt.Errorf("unknown client %q (known: %s | all)", want, strings.Join(known, " | "))
}

// confirm prompts on stdin for a Y/n answer. Returns true on Y / y /
// empty (default Yes); false on n / N. Other input re-prompts.
func confirm(question string) bool {
	r := bufio.NewReader(os.Stdin)
	for {
		fmt.Fprintf(os.Stderr, "%s [Y/n] ", question)
		line, err := r.ReadString('\n')
		if err != nil {
			return false
		}
		line = strings.TrimSpace(strings.ToLower(line))
		switch line {
		case "", "y", "yes":
			return true
		case "n", "no":
			return false
		}
	}
}

// logMetricsPeriodically dumps a Snapshot to slog every interval, but
// skips intervals with no activity so a quiet stdio session doesn't spam
// the log every minute. Stops when ctx is canceled.
func logMetricsPeriodically(ctx context.Context, m *obs.Metrics, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !m.HasActivity() {
				continue
			}
			snap := m.Snapshot()
			for _, p := range snap.Providers {
				slog.Info("metrics",
					"provider", p.Name,
					"calls", p.Calls,
					"errors", p.Errors,
					"cost_usd", p.CostUSD,
					"avg_latency_ms", p.AvgLatencyMS)
			}
			slog.Info("metrics_total", "cost_usd", snap.TotalCost)
		}
	}
}

// buildSearchers instantiates one search.Searcher per search_providers
// entry whose credentials/endpoint are present at startup. Hosted APIs
// (You.com, Serper) gate on their api_key_env; self-hosted backends
// (SearXNG) gate on a non-empty base URL — resolved from base_url_env
// first, falling back to the static base_url. Entries marked
// `enabled: false` are skipped — the operator's opt-out from the
// config-load default overlay. Unknown provider names log a warning and
// are skipped — operators can edit ~/.frugal/config/models.yaml to add
// new providers, but driver wiring lives here in code.
func buildSearchers(cfg *config.Config) []search.Searcher {
	var out []search.Searcher
	for _, name := range sortedProviderNames(cfg.SearchProviders) {
		sp := cfg.SearchProviders[name]
		if sp.Disabled() {
			continue
		}
		key := ""
		if sp.APIKeyEnv != "" {
			key = os.Getenv(sp.APIKeyEnv)
		}
		base := sp.BaseURL
		if sp.BaseURLEnv != "" {
			if envBase := os.Getenv(sp.BaseURLEnv); envBase != "" {
				base = envBase
			}
		}
		switch name {
		case "youcom":
			if key == "" {
				continue
			}
			out = append(out, youcom.New(key, base, sp.CostPerCall))
		case "serper":
			if key == "" {
				continue
			}
			out = append(out, serper.New(key, base, sp.CostPerCall))
		case "searxng":
			// Self-hosted; gate on base URL (no API key).
			if c := searxng.New(base); c != nil {
				out = append(out, c)
			}
		case "marginalia":
			// Public, donation-funded; no API key, no required URL —
			// driver defaults to the public endpoint. Always registers
			// if the YAML entry exists.
			out = append(out, marginalia.New(base))
		case "wikipedia":
			// Public Wikimedia REST search; no API key, no required
			// URL. Always registers if the YAML entry exists.
			out = append(out, wikipedia.New(base))
		default:
			slog.Warn("mcp serve: unknown search provider in config; ignoring",
				"name", name, "hint", "add a driver in internal/provider/<name> and a switch case here")
		}
	}
	return out
}

// buildExtractors instantiates one extract.Extractor per extract_providers
// entry whose credentials/endpoint are present at startup. goreadability
// is in-process and always available when listed in the YAML; firecrawl
// gates on FIRECRAWL_API_KEY. Entries marked `enabled: false` are
// skipped. Unknown names log a warning and are skipped.
func buildExtractors(cfg *config.Config) []extract.Extractor {
	var out []extract.Extractor
	for _, name := range sortedProviderNames(cfg.ExtractProviders) {
		sp := cfg.ExtractProviders[name]
		if sp.Disabled() {
			continue
		}
		key := ""
		if sp.APIKeyEnv != "" {
			key = os.Getenv(sp.APIKeyEnv)
		}
		base := sp.BaseURL
		if sp.BaseURLEnv != "" {
			if envBase := os.Getenv(sp.BaseURLEnv); envBase != "" {
				base = envBase
			}
		}
		switch name {
		case "goreadability":
			// Pure-in-process — no key, no URL, always available.
			out = append(out, goreadability.New())
		case "firecrawl":
			if key == "" {
				continue
			}
			out = append(out, firecrawl.New(key, base, sp.CostPerCall))
		default:
			slog.Warn("mcp serve: unknown extract provider in config; ignoring",
				"name", name, "hint", "add a driver in internal/provider/<name> and a switch case here")
		}
	}
	return out
}

// buildBrowsers instantiates one browse.Browser per browse_providers
// entry whose credentials/endpoint are present at startup. Entries
// marked `enabled: false` are skipped. Currently only Browserless is
// supported; local Playwright is deferred.
func buildBrowsers(cfg *config.Config) []browse.Browser {
	var out []browse.Browser
	for _, name := range sortedProviderNames(cfg.BrowseProviders) {
		sp := cfg.BrowseProviders[name]
		if sp.Disabled() {
			continue
		}
		key := ""
		if sp.APIKeyEnv != "" {
			key = os.Getenv(sp.APIKeyEnv)
		}
		base := sp.BaseURL
		if sp.BaseURLEnv != "" {
			if envBase := os.Getenv(sp.BaseURLEnv); envBase != "" {
				base = envBase
			}
		}
		switch name {
		case "browserless":
			if key == "" {
				continue
			}
			out = append(out, browserless.New(key, base, sp.CostPerCall))
		default:
			slog.Warn("mcp serve: unknown browse provider in config; ignoring",
				"name", name, "hint", "add a driver in internal/provider/<name> and a switch case here")
		}
	}
	return out
}
