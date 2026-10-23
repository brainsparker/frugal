package main

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/frugalsh/frugal/internal/config"
)

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

// providerStatuses builds the dry-run status for every provider across all
// capabilities, mirroring the same gating logic the real serve path uses.
// No drivers are instantiated; no network is reached.
func providerStatuses(cfg *config.Config) (search, extract, browse []providerStatus) {
	search = buildSearchStatuses(cfg)
	extract = buildExtractStatuses(cfg)
	browse = buildBrowseStatuses(cfg)
	return
}

// buildSearchStatuses mirrors buildSearchers' gating logic for the dry-run.
func buildSearchStatuses(cfg *config.Config) []providerStatus {
	var out []providerStatus
	for name, sp := range cfg.SearchProviders {
		st := providerStatus{Name: name, Cost: sp.CostPerCall}
		switch name {
		case "searxng":
			base := os.Getenv(sp.BaseURLEnv)
			if base == "" {
				base = sp.BaseURL
			}
			if base == "" {
				st.Reason = "missing $SEARXNG_URL (or searxng.base_url)"
			} else {
				st.Active = true
				st.Reason = fmt.Sprintf("self-hosted at %s", base)
			}
		case "marginalia":
			st.Active = true
			st.Reason = "public endpoint (no key required)"
		case "youcom-free":
			st.Active = true
			st.Reason = "free tier (no key required, ~100 queries/day)"
		case "wikipedia":
			st.Active = true
			st.Reason = "free Wikimedia REST search (no key required)"
		case "serper":
			if key := os.Getenv(sp.APIKeyEnv); key != "" {
				st.Active = true
				st.Reason = "$SERPER_API_KEY set"
			} else {
				st.Reason = "missing $SERPER_API_KEY"
			}
		case "youcom":
			if key := os.Getenv(sp.APIKeyEnv); key != "" {
				st.Active = true
				st.Reason = "$YDC_API_KEY set"
			} else {
				st.Reason = "missing $YDC_API_KEY"
			}
		case "brave":
			if key := os.Getenv(sp.APIKeyEnv); key != "" {
				st.Active = true
				st.Reason = "$BRAVE_API_KEY set"
			} else {
				st.Reason = "missing $BRAVE_API_KEY"
			}
		default:
			st.Unknown = true
			st.Reason = "unknown provider — not wired in code"
		}
		if sp.Disabled() {
			st.Active = false
			if st.Reason == "" {
				st.Reason = "enabled: false in config"
			} else {
				st.Reason += " (but enabled: false in config)"
			}
		}
		out = append(out, st)
	}
	return out
}

// buildExtractStatuses mirrors buildExtractors' gating logic for the dry-run.
func buildExtractStatuses(cfg *config.Config) []providerStatus {
	var out []providerStatus
	for name, sp := range cfg.ExtractProviders {
		st := providerStatus{Name: name, Cost: sp.CostPerCall}
		switch name {
		case "goreadability":
			st.Active = true
			st.Reason = "in-process (no key required)"
		case "jina":
			key := os.Getenv(sp.APIKeyEnv)
			if key != "" {
				st.Active = true
				st.Reason = "$JINA_API_KEY set (500 req/min)"
			} else {
				st.Active = true
				st.Reason = "public endpoint (20 req/min without key)"
			}
		case "firecrawl":
			if key := os.Getenv(sp.APIKeyEnv); key != "" {
				st.Active = true
				st.Reason = "$FIRECRAWL_API_KEY set"
			} else {
				st.Reason = "missing $FIRECRAWL_API_KEY"
			}
		default:
			st.Unknown = true
			st.Reason = "unknown provider — not wired in code"
		}
		if sp.Disabled() {
			st.Active = false
			if st.Reason == "" {
				st.Reason = "enabled: false in config"
			} else {
				st.Reason += " (but enabled: false in config)"
			}
		}
		out = append(out, st)
	}
	return out
}

// buildBrowseStatuses mirrors buildBrowsers' gating logic for the dry-run.
func buildBrowseStatuses(cfg *config.Config) []providerStatus {
	var out []providerStatus
	for name, sp := range cfg.BrowseProviders {
		st := providerStatus{Name: name, Cost: sp.CostPerCall}
		switch name {
		case "browserless":
			if key := os.Getenv(sp.APIKeyEnv); key != "" {
				st.Active = true
				st.Reason = "$BROWSERLESS_TOKEN set"
			} else {
				st.Reason = "missing $BROWSERLESS_TOKEN"
			}
		default:
			st.Unknown = true
			st.Reason = "unknown provider — not wired in code"
		}
		if sp.Disabled() {
			st.Active = false
			if st.Reason == "" {
				st.Reason = "enabled: false in config"
			} else {
				st.Reason += " (but enabled: false in config)"
			}
		}
		out = append(out, st)
	}
	return out
}

// printRoutePlan renders the dry-run audit view: for each capability,
// every configured provider listed in cost-ascending order with the
// gating decision and the reason. Active providers come first within
// each cost bucket; the cheapest active provider in a bucket is the
// auto-route winner the real `serve` path would pick.
//
// Output goes to w (stdout in normal use) so the audit can be piped or
// diffed across environments. No drivers are constructed beyond
// examining what env vars are set, so no network is reached.
func printRoutePlan(w io.Writer, cfg *config.Config, ver, httpAddr string, authTokenSet, allowAnon bool) {
	search, extract, browse := providerStatuses(cfg)

	fmt.Fprintf(w, "frugal mcp serve --dry-run  (frugal %s)\n", ver)
	fmt.Fprintln(w, "no requests will be served; no network calls were made.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "route plan (ascending cost — the first ACTIVE row wins by default):")
	fmt.Fprintln(w)

	printCapabilityPlan(w, "frugal__search", search)
	printCapabilityPlan(w, "frugal__extract", extract)
	printCapabilityPlan(w, "frugal__browse", browse)

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