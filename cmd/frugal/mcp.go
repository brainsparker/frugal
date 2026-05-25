package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/frugalsh/frugal/internal/config"
	"github.com/frugalsh/frugal/internal/install"
	"github.com/frugalsh/frugal/internal/mcp"
	"github.com/frugalsh/frugal/internal/mcp/tools"
	"github.com/frugalsh/frugal/internal/obs"
	"github.com/frugalsh/frugal/internal/browse"
	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/provider/browserless"
	"github.com/frugalsh/frugal/internal/provider/firecrawl"
	"github.com/frugalsh/frugal/internal/provider/goreadability"
	"github.com/frugalsh/frugal/internal/provider/marginalia"
	"github.com/frugalsh/frugal/internal/provider/searxng"
	"github.com/frugalsh/frugal/internal/provider/serper"
	"github.com/frugalsh/frugal/internal/provider/youcom"
	"github.com/frugalsh/frugal/internal/search"
)

// runMCP dispatches the `frugal mcp <subcommand>` family. Returns the
// process exit code.
//
// Subcommands:
//   - serve:   run Frugal as an MCP server (stdio default; --http :PORT for Streamable HTTP)
//   - install: write MCP server config into Claude Desktop / Cursor / Claude Code (Phase 1 PR 6)
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
// --http :PORT switches to Streamable HTTP for remote deployments and HTTP
// clients.
//
// Both transports honor SIGINT / SIGTERM with a graceful shutdown.
func runMCPServe(args []string) int {
	fs := flag.NewFlagSet("mcp serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	httpAddr := fs.String("http", "", "if set, serve over Streamable HTTP on this address (e.g. :8765) instead of stdio")
	allowAnon := fs.Bool("allow-anon", false, "permit --http to run without FRUGAL_AUTH_TOKEN (foot-gun: only for localhost or behind a trusted proxy)")
	rateLimit := fs.Int("rate-limit-rpm", 600, "per-IP request budget per minute when serving --http (0 disables)")
	reqTimeout := fs.Duration("request-timeout", 30*time.Second, "per-request timeout when serving --http (0 disables)")
	dryRun := fs.Bool("dry-run", false, "print the route plan (which provider would win each capability, and why) and exit without serving anything")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: frugal mcp serve [--http ADDR] [--dry-run]")
		fmt.Fprintln(os.Stderr, "Run Frugal as an MCP server. Default transport is stdio")
		fmt.Fprintln(os.Stderr, "(what Claude Desktop, Claude Code, and Cursor consume).")
		fmt.Fprintln(os.Stderr, "Pass --http :PORT for Streamable HTTP (remote / HTTP clients).")
		fmt.Fprintln(os.Stderr, "Set FRUGAL_AUTH_TOKEN to enable bearer-token auth, or pass --allow-anon")
		fmt.Fprintln(os.Stderr, "to expose the server unauthenticated (localhost / trusted-proxy only).")
		fmt.Fprintln(os.Stderr, "Pass --dry-run to print the routing decisions and exit without")
		fmt.Fprintln(os.Stderr, "serving anything — no network calls are made; nothing is registered.")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	configPath := "config/models.yaml"
	if p := os.Getenv("FRUGAL_CONFIG"); p != "" {
		configPath = p
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		// stdio mode keeps stdout free of non-JSON bytes — failure logs go to
		// stderr, which is the contract every MCP client respects.
		fmt.Fprintf(os.Stderr, "frugal mcp serve: load config: %v\n", err)
		return 1
	}

	if *dryRun {
		// Audit mode: walk the same gating logic the real serve path uses,
		// print which provider would win each capability and why, then exit.
		// No drivers are instantiated; no network is reached. Output goes
		// to stdout so it's pipe-able (`frugal mcp serve --dry-run | diff`)
		// even when the real serve path would have constrained stdout.
		printRoutePlan(os.Stdout, cfg, version(), *httpAddr, os.Getenv("FRUGAL_AUTH_TOKEN") != "", *allowAnon)
		return 0
	}

	// stdio transport is single-session; logging to stderr is safe and
	// preserves the MCP newline-delimited JSON-RPC contract on stdout.
	srv := mcp.New("frugal", version(), slog.Default())

	metrics := obs.NewMetrics()
	searchers, _ := buildSearchers(cfg)
	tools.RegisterSearch(srv.Inner, searchers, metrics)
	if len(searchers) == 0 {
		slog.Warn("mcp serve: no search providers configured — frugal__search will not be advertised. " +
			"Set SEARXNG_URL (free, self-hosted), SERPER_API_KEY, or YDC_API_KEY to enable.")
	} else {
		names := make([]string, 0, len(searchers))
		for _, s := range searchers {
			names = append(names, s.Name())
		}
		slog.Info("mcp serve: frugal__search registered", "providers", names)
	}

	extractors, _ := buildExtractors(cfg)
	tools.RegisterExtract(srv.Inner, extractors, metrics)
	if len(extractors) > 0 {
		names := make([]string, 0, len(extractors))
		for _, e := range extractors {
			names = append(names, e.Name())
		}
		slog.Info("mcp serve: frugal__extract registered", "providers", names)
	}

	browsers, _ := buildBrowsers(cfg)
	tools.RegisterBrowse(srv.Inner, browsers, metrics)
	if len(browsers) > 0 {
		names := make([]string, 0, len(browsers))
		for _, b := range browsers {
			names = append(names, b.Name())
		}
		slog.Info("mcp serve: frugal__browse registered", "providers", names)
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
// agent client's config — Claude Desktop and Cursor merge into a JSON
// file; Claude Code gets a printed `claude mcp add` command (the
// `claude` CLI manages its own config).
//
// Flags:
//   - --client <id|all>  install only into the named client (default: all detected)
//   - --print            print the plan without writing (dry-run)
//   - --yes              skip the confirmation prompt
func runMCPInstall(args []string) int {
	fs := flag.NewFlagSet("mcp install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	clientID := fs.String("client", "all", "install into a specific client (claude-desktop | cursor | claude-code | all)")
	printOnly := fs.Bool("print", false, "print the plan without writing")
	assumeYes := fs.Bool("yes", false, "skip the confirmation prompt")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: frugal mcp install [flags]")
		fmt.Fprintln(os.Stderr, "Wire 'frugal' as an MCP server in agent clients (Claude Desktop, Cursor, Claude Code).")
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
			fmt.Fprintln(os.Stderr, "(no detected clients — install Claude Desktop, Cursor, or Claude Code first,")
			fmt.Fprintln(os.Stderr, " or pass --client X to force install into one anyway.)")
		}
		return 1
	}

	fmt.Fprintf(os.Stderr, "frugal binary: %s\n", binPath)
	fmt.Fprintln(os.Stderr, "planned changes:")
	for _, c := range targets {
		fmt.Fprintf(os.Stderr, "  - %s: %s\n", c.Title, install.PlanFor(c, binPath))
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
	for _, c := range targets {
		suggestion, err := install.Apply(c, binPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s: %v\n", c.Title, err)
			hadErr = true
			continue
		}
		switch c.Kind {
		case install.KindJSONFile:
			fmt.Fprintf(os.Stderr, "✓ %s: wrote %s\n", c.Title, c.ConfigPath)
		case install.KindCLI:
			fmt.Fprintf(os.Stderr, "→ %s: run this command yourself (the `claude` CLI manages its own config)\n", c.Title)
			fmt.Fprintf(os.Stderr, "    %s\n", suggestion)
		}
	}
	if hadErr {
		return 1
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "next steps:")
	fmt.Fprintln(os.Stderr, "  1. set SERPER_API_KEY and/or YDC_API_KEY in the env where the agent runs")
	fmt.Fprintln(os.Stderr, "     (for Claude Desktop: add env vars to the same config; for Cursor: same)")
	fmt.Fprintln(os.Stderr, "  2. restart the agent client to pick up the new MCP server")
	fmt.Fprintln(os.Stderr, "  3. look for the 'frugal__search' tool in the agent's tool picker")
	return 0
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

// providerStatus is one row in the dry-run plan: what `frugal mcp serve`
// would do with this provider at startup. Returned alongside the live
// driver slices from build*ers so the audit output (`--dry-run`) can't
// drift from the actual gating decisions — same code path, same answer.
type providerStatus struct {
	// Name is the YAML key (and the driver name in metrics).
	Name string
	// Cost is the configured per-call price in USD. Free / self-hosted is 0.
	Cost float64
	// Active is true when the provider would be registered at serve time.
	Active bool
	// Reason explains the gating decision in one line. For active providers
	// it names the env var / mode that gated it in; for skipped providers
	// it names the missing env var.
	Reason string
	// Unknown is set when the provider name isn't wired in code. Surfaced
	// so the dry-run can warn loudly instead of silently dropping the entry.
	Unknown bool
}

// buildSearchers instantiates one search.Searcher per search_providers
// entry whose credentials/endpoint are present at startup. Hosted APIs
// (You.com, Serper) gate on their api_key_env; self-hosted backends
// (SearXNG) gate on a non-empty base URL — resolved from base_url_env
// first, falling back to the static base_url. Unknown provider names log
// a warning and are skipped — operators can edit ~/.frugal/config/models.yaml
// to add new providers, but driver wiring lives here in code.
//
// The second return value mirrors every entry in cfg.SearchProviders
// (active or not) so the dry-run / audit surface stays in lockstep with
// the real gating logic.
func buildSearchers(cfg *config.Config) ([]search.Searcher, []providerStatus) {
	var out []search.Searcher
	statuses := make([]providerStatus, 0, len(cfg.SearchProviders))
	for name, sp := range cfg.SearchProviders {
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
		st := providerStatus{Name: name, Cost: sp.CostPerCall}
		switch name {
		case "youcom":
			if key == "" {
				st.Reason = fmt.Sprintf("skipped: $%s not set", sp.APIKeyEnv)
			} else {
				st.Active = true
				st.Reason = fmt.Sprintf("$%s set", sp.APIKeyEnv)
				out = append(out, youcom.New(key, base, sp.CostPerCall))
			}
		case "serper":
			if key == "" {
				st.Reason = fmt.Sprintf("skipped: $%s not set", sp.APIKeyEnv)
			} else {
				st.Active = true
				st.Reason = fmt.Sprintf("$%s set", sp.APIKeyEnv)
				out = append(out, serper.New(key, base, sp.CostPerCall))
			}
		case "searxng":
			// Self-hosted; gate on base URL (no API key).
			if c := searxng.New(base); c != nil {
				st.Active = true
				st.Reason = fmt.Sprintf("$%s set", sp.BaseURLEnv)
				out = append(out, c)
			} else {
				st.Reason = fmt.Sprintf("skipped: $%s not set", sp.BaseURLEnv)
			}
		case "marginalia":
			// Public, donation-funded; no API key, no required URL —
			// driver defaults to the public endpoint. Always registers
			// if the YAML entry exists.
			st.Active = true
			st.Reason = "public endpoint (no key required)"
			out = append(out, marginalia.New(base))
		default:
			st.Unknown = true
			st.Reason = "unknown provider — add a driver in internal/provider/<name> and a switch case in cmd/frugal/mcp.go"
			slog.Warn("mcp serve: unknown search provider in config; ignoring",
				"name", name, "hint", "add a driver in internal/provider/<name> and a switch case here")
		}
		statuses = append(statuses, st)
	}
	return out, statuses
}

// buildExtractors instantiates one extract.Extractor per extract_providers
// entry whose credentials/endpoint are present at startup. goreadability
// is in-process and always available when listed in the YAML; firecrawl
// gates on FIRECRAWL_API_KEY. Unknown names log a warning and are skipped.
func buildExtractors(cfg *config.Config) ([]extract.Extractor, []providerStatus) {
	var out []extract.Extractor
	statuses := make([]providerStatus, 0, len(cfg.ExtractProviders))
	for name, sp := range cfg.ExtractProviders {
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
		st := providerStatus{Name: name, Cost: sp.CostPerCall}
		switch name {
		case "goreadability":
			// Pure-in-process — no key, no URL, always available.
			st.Active = true
			st.Reason = "in-process (no key required)"
			out = append(out, goreadability.New())
		case "firecrawl":
			if key == "" {
				st.Reason = fmt.Sprintf("skipped: $%s not set", sp.APIKeyEnv)
			} else {
				st.Active = true
				st.Reason = fmt.Sprintf("$%s set", sp.APIKeyEnv)
				out = append(out, firecrawl.New(key, base, sp.CostPerCall))
			}
		default:
			st.Unknown = true
			st.Reason = "unknown provider — add a driver in internal/provider/<name> and a switch case in cmd/frugal/mcp.go"
			slog.Warn("mcp serve: unknown extract provider in config; ignoring",
				"name", name, "hint", "add a driver in internal/provider/<name> and a switch case here")
		}
		statuses = append(statuses, st)
	}
	return out, statuses
}

// buildBrowsers instantiates one browse.Browser per browse_providers
// entry whose credentials/endpoint are present at startup. Currently
// only Browserless is supported; local Playwright is deferred.
func buildBrowsers(cfg *config.Config) ([]browse.Browser, []providerStatus) {
	var out []browse.Browser
	statuses := make([]providerStatus, 0, len(cfg.BrowseProviders))
	for name, sp := range cfg.BrowseProviders {
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
		st := providerStatus{Name: name, Cost: sp.CostPerCall}
		switch name {
		case "browserless":
			if key == "" {
				st.Reason = fmt.Sprintf("skipped: $%s not set", sp.APIKeyEnv)
			} else {
				st.Active = true
				st.Reason = fmt.Sprintf("$%s set", sp.APIKeyEnv)
				out = append(out, browserless.New(key, base, sp.CostPerCall))
			}
		default:
			st.Unknown = true
			st.Reason = "unknown provider — add a driver in internal/provider/<name> and a switch case in cmd/frugal/mcp.go"
			slog.Warn("mcp serve: unknown browse provider in config; ignoring",
				"name", name, "hint", "add a driver in internal/provider/<name> and a switch case here")
		}
		statuses = append(statuses, st)
	}
	return out, statuses
}

// printRoutePlan renders the dry-run audit view: for each capability,
// every configured provider listed in cost-ascending order with the
// gating decision and the reason. Active providers come first within
// each cost bucket; the cheapest active provider in a bucket is the
// auto-route winner the real `serve` path would pick. No drivers are
// constructed beyond what the build*ers already build (which themselves
// honour the gating predicate, so no network is reached for skipped
// providers).
//
// Output goes to w (stdout in normal use) so the audit can be piped or
// diffed across environments.
func printRoutePlan(w io.Writer, cfg *config.Config, ver, httpAddr string, authTokenSet, allowAnon bool) {
	_, searchStatuses := buildSearchers(cfg)
	_, extractStatuses := buildExtractors(cfg)
	_, browseStatuses := buildBrowsers(cfg)

	fmt.Fprintf(w, "frugal mcp serve --dry-run  (frugal %s)\n", ver)
	fmt.Fprintln(w, "no requests will be served; no network calls were made.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "route plan (ascending cost — the first ACTIVE row wins by default):")
	fmt.Fprintln(w)

	printCapabilityPlan(w, "frugal__search", searchStatuses)
	printCapabilityPlan(w, "frugal__extract", extractStatuses)
	printCapabilityPlan(w, "frugal__browse", browseStatuses)

	fmt.Fprintln(w, "transport:")
	if httpAddr == "" {
		fmt.Fprintln(w, "  stdio (default — what Claude Desktop, Claude Code, and Cursor consume)")
	} else {
		fmt.Fprintf(w, "  streamable HTTP on %s\n", httpAddr)
		switch {
		case authTokenSet:
			fmt.Fprintln(w, "  auth: bearer token ($FRUGAL_AUTH_TOKEN set)")
		case allowAnon:
			fmt.Fprintln(w, "  auth: NONE (--allow-anon — only safe on localhost / behind a trusted proxy)")
		default:
			fmt.Fprintln(w, "  auth: would refuse to start ($FRUGAL_AUTH_TOKEN unset and --allow-anon not passed)")
		}
	}
	fmt.Fprintln(w)
}

// printCapabilityPlan formats one capability's plan rows. Sort key:
// cost ascending, then active-before-skipped at the same cost (the
// auto-route winner is what the operator wants to see first), then by
// name for determinism. Skipped tool blocks still print so reviewers
// see what the YAML wired and what a missing env var would unlock.
func printCapabilityPlan(w io.Writer, tool string, statuses []providerStatus) {
	sort.SliceStable(statuses, func(i, j int) bool {
		if statuses[i].Cost != statuses[j].Cost {
			return statuses[i].Cost < statuses[j].Cost
		}
		if statuses[i].Active != statuses[j].Active {
			return statuses[i].Active
		}
		return statuses[i].Name < statuses[j].Name
	})

	if len(statuses) == 0 {
		fmt.Fprintf(w, "  %s: (none configured)\n\n", tool)
		return
	}
	activeCount := 0
	for _, st := range statuses {
		if st.Active {
			activeCount++
		}
	}
	if activeCount == 0 {
		fmt.Fprintf(w, "  %s: NOT REGISTERED — no providers gated in. Configured but skipped:\n", tool)
	} else {
		fmt.Fprintf(w, "  %s: registered with %d provider(s):\n", tool, activeCount)
	}
	for _, st := range statuses {
		state := "SKIP  "
		if st.Active {
			state = "ACTIVE"
		}
		if st.Unknown {
			state = "UNKNWN"
		}
		fmt.Fprintf(w, "    %s  %-13s  $%.4f/call  %s\n", state, st.Name, st.Cost, st.Reason)
	}
	fmt.Fprintln(w)
}
