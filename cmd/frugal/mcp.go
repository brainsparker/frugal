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
	"syscall"
	"time"

	"github.com/frugalsh/frugal/internal/browse"
	"github.com/frugalsh/frugal/internal/config"
	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/install"
	"github.com/frugalsh/frugal/internal/mcp"
	"github.com/frugalsh/frugal/internal/mcp/tools"
	"github.com/frugalsh/frugal/internal/obs"
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
	maxRequestBytes := fs.Int64("max-request-bytes", 1<<20, "reject HTTP requests above this Content-Length when serving --http (0 disables)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: frugal mcp serve [--http ADDR]")
		fmt.Fprintln(os.Stderr, "Run Frugal as an MCP server. Default transport is stdio")
		fmt.Fprintln(os.Stderr, "(what Claude Desktop, Claude Code, and Cursor consume).")
		fmt.Fprintln(os.Stderr, "Pass --http :PORT for Streamable HTTP (remote / HTTP clients).")
		fmt.Fprintln(os.Stderr, "Set FRUGAL_AUTH_TOKEN to enable bearer-token auth, or pass --allow-anon")
		fmt.Fprintln(os.Stderr, "to expose the server unauthenticated (localhost / trusted-proxy only).")
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
	searchers := buildSearchers(cfg)
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

	extractors := buildExtractors(cfg)
	tools.RegisterExtract(srv.Inner, extractors, metrics)
	if len(extractors) > 0 {
		names := make([]string, 0, len(extractors))
		for _, e := range extractors {
			names = append(names, e.Name())
		}
		slog.Info("mcp serve: frugal__extract registered", "providers", names)
	}

	browsers := buildBrowsers(cfg)
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
			fmt.Fprintln(os.Stderr, "(no detected clients — install Claude Desktop, Cursor, or Claude Code first,")
			fmt.Fprintln(os.Stderr, " or pass --client X to force install into one anyway.)")
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
	for _, c := range targets {
		suggestion, err := install.Apply(c, binPath, env)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s: %v\n", c.Title, err)
			hadErr = true
			continue
		}
		switch c.Kind {
		case install.KindJSONFile:
			wroteJSON = true
			fmt.Fprintf(os.Stderr, "✓ %s: wrote %s\n", c.Title, c.ConfigPath)
		case install.KindCLI:
			if suggestion == "" {
				fmt.Fprintf(os.Stderr, "✓ %s: registered via `claude mcp add`\n", c.Title)
			} else {
				fmt.Fprintf(os.Stderr, "→ %s: couldn't invoke the `claude` CLI; run this yourself:\n", c.Title)
				fmt.Fprintf(os.Stderr, "    %s\n", suggestion)
			}
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
	return 0
}

// canonicalProviderOrder fixes the tie-break between same-cost providers:
// self-hosted first (the operator stood that instance up deliberately),
// then public-free, then paid by ascending list price. YAML maps don't
// preserve file order, so this list — not the config file — is what makes
// registration (and therefore OrderByCost's stable ties) deterministic.
// Providers not listed here sort last, by name.
var canonicalProviderOrder = []string{
	"searxng", "marginalia", "serper", "youcom", // search
	"goreadability", "firecrawl", // extract
	"browserless", // browse
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
// first, falling back to the static base_url. Unknown provider names log
// a warning and are skipped — operators can edit ~/.frugal/config/models.yaml
// to add new providers, but driver wiring lives here in code.
func buildSearchers(cfg *config.Config) []search.Searcher {
	var out []search.Searcher
	for _, name := range sortedProviderNames(cfg.SearchProviders) {
		sp := cfg.SearchProviders[name]
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
// gates on FIRECRAWL_API_KEY. Unknown names log a warning and are skipped.
func buildExtractors(cfg *config.Config) []extract.Extractor {
	var out []extract.Extractor
	for _, name := range sortedProviderNames(cfg.ExtractProviders) {
		sp := cfg.ExtractProviders[name]
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
// entry whose credentials/endpoint are present at startup. Currently
// only Browserless is supported; local Playwright is deferred.
func buildBrowsers(cfg *config.Config) []browse.Browser {
	var out []browse.Browser
	for _, name := range sortedProviderNames(cfg.BrowseProviders) {
		sp := cfg.BrowseProviders[name]
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
