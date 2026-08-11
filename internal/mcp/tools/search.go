// Package tools registers Frugal's routed MCP tools against an
// *mcp.Server. Each tool here delegates to the relevant internal/provider
// driver(s) via a small interface defined in internal/search (and, in
// later PRs, internal/extract, internal/cache, …).
//
// The tool surface is intentionally narrow: one tool per capability
// (frugal__search, frugal__extract, frugal__browse) plus the
// intent-level frugal__execute, with the provider choice happening
// inside the handler. Agents see one stable tool name; the routing
// decision is reported, not delegated.
package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/obs"
	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// SearchInput is the JSON-schema-generating shape for frugal__search.
// Field tags drive the schema the MCP client sees — keep names + descriptions
// stable across releases, since agents pattern-match on them.
type SearchInput struct {
	Query      string `json:"query" jsonschema:"the search query"`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"max results to return (default 5, clamped to 20)"`
	Freshness  string `json:"freshness,omitempty" jsonschema:"optional time window: day | week | month"`
	// Provider pins the search provider for this call ("searxng", "serper",
	// "youcom", …). When empty or "auto", the configured routing policy
	// picks (cheapest-first by default). Use a pin when a specific
	// provider is known to have materially better recall for this call.
	Provider string `json:"provider,omitempty" jsonschema:"optional provider override: searxng | marginalia | wikipedia | serper | youcom | auto"`
}

// SearchOutput is the structured-content payload returned to the MCP
// client. The result list is what the agent reads to compose its answer;
// CostUSD + ProviderUsed + LatencyMS are the observability footer that
// makes the routing decision auditable in agent traces.
type SearchOutput struct {
	Results      []search.Item `json:"results"`
	CostUSD      float64       `json:"cost_usd"`
	ProviderUsed string        `json:"provider_used"`
	LatencyMS    int64         `json:"latency_ms"`
	// Warnings carries degraded-service notes from the winning provider —
	// e.g. "freshness window ignored" from drivers without a time filter —
	// so the agent can react (re-query pinned to serper/youcom) instead of
	// mistaking best-effort results for exact ones.
	Warnings []string `json:"warnings,omitempty" jsonschema:"degraded-service notes, e.g. a provider that ignored the freshness window"`
}

// RegisterSearch wires frugal__search onto the given MCP server. searchers
// is the operator-configured list (one entry per configured provider key).
// A no-op when searchers is empty — the tool is not registered, so
// tools/list won't advertise something the server can't fulfill. That
// distinction matters: agents query tools/list at session start and
// shouldn't see ghost tools that always error.
//
// Pass metrics (non-nil) to record per-provider call counts, error counts,
// latency, and cost as each call lands. Nil metrics disables observability
// but keeps the routing semantics identical.
func RegisterSearch(server *sdkmcp.Server, searchers []search.Searcher, metrics *obs.Metrics, opts ...ToolOption) {
	if len(searchers) == 0 {
		return
	}
	if metrics != nil {
		for _, s := range searchers {
			metrics.EnsureProvider(s.Name(), "search")
		}
	}
	desc := fmt.Sprintf(
		"Run a web search routed across %s. Returns a list of {title, url, snippet} hits "+
			"plus the actual provider used and cost paid. Provider choice follows the "+
			"configured routing policy (cheapest-first by default, with automatic failover); "+
			"pin via the `provider` argument.",
		joinNames(searchers),
	)
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        "frugal__search",
		Title:       "Web search (routed)",
		Description: desc,
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  false, // search results can shift between calls
			OpenWorldHint:   boolPtr(true),
		},
	}, makeSearchHandler(searchers, metrics, buildToolOptions(opts)))
}

func makeSearchHandler(searchers []search.Searcher, metrics *obs.Metrics, o toolOptions) func(context.Context, *sdkmcp.CallToolRequest, SearchInput) (*sdkmcp.CallToolResult, SearchOutput, error) {
	// Hook closes over metrics so every fallback attempt is recorded —
	// not just the winner. Nil metrics skips recording, costing a comparison
	// per call.
	var hook search.AttemptHook
	if metrics != nil {
		hook = func(provider string, latency time.Duration, costUSD float64, won bool, err error) {
			metrics.RecordCall(provider, latency, costUSD, won, err)
		}
	}
	// Compose the guard's cost / rate-limit recording onto the metrics
	// hook so every attempt books spend and trips the cooldown. Nil guard
	// makes this a pass-through (the guard methods are nil-safe).
	hook = composeHook(o.guard, "search", hook)

	return func(ctx context.Context, _ *sdkmcp.CallToolRequest, in SearchInput) (*sdkmcp.CallToolResult, SearchOutput, error) {
		if in.Query == "" {
			return nil, SearchOutput{}, fmt.Errorf("frugal__search: query is required")
		}
		freshness, err := normalizeFreshness(in.Freshness)
		if err != nil {
			return nil, SearchOutput{}, fmt.Errorf("frugal__search: %w", err)
		}
		q := search.Query{
			Text:       in.Query,
			MaxResults: in.MaxResults,
			Freshness:  freshness,
		}
		logger := slog.Default()

		provider := normalizeProvider(in.Provider)
		start := time.Now()
		var (
			used      search.Searcher
			res       search.Results
			searchErr error
		)
		if isAuto(provider) {
			ordered, reason := routing.Apply(searchers, o.policy, o.lat, time.Now())
			if len(ordered) == 0 {
				return nil, SearchOutput{}, fmt.Errorf("frugal__search: every configured provider is denied by the routing policy")
			}
			ordered, reason = guardChain(o.guard, "search", ordered, reason, logger)
			if len(ordered) == 0 {
				return nil, SearchOutput{}, fmt.Errorf("frugal__search: %w", guardEmptyError("search"))
			}
			logger.Debug("search routing", "reason", reason)
			used, res, searchErr = search.CallInOrder(ctx, ordered, q, logger, hook)
		} else if o.policy.Deny[provider] {
			// Deny means "never call" — a pin doesn't override it.
			return nil, SearchOutput{}, fmt.Errorf("frugal__search: provider %q is denied by the routing policy", provider)
		} else if ok, why := o.guard.Allow("search", provider); !ok {
			// Budget / cooldown blocks a pin the same way deny does:
			// blocked means blocked.
			return nil, SearchOutput{}, fmt.Errorf("frugal__search: provider %q unavailable: %s", provider, why)
		} else {
			used, res, searchErr = search.CallPinned(ctx, searchers, provider, q, logger, hook)
		}
		latency := time.Since(start).Milliseconds()
		if searchErr != nil {
			return nil, SearchOutput{}, fmt.Errorf("frugal__search: %w", searchErr)
		}

		out := SearchOutput{
			Results:      res.Items,
			CostUSD:      res.CostUSD,
			ProviderUsed: used.Name(),
			LatencyMS:    latency,
			Warnings:     res.Warnings,
		}
		return nil, out, nil
	}
}

// isAuto reports whether the caller wants auto-routing (the default).
// Empty string or the explicit sentinel "auto" both mean "pick for me."
func isAuto(requested string) bool { return requested == "" || requested == "auto" }

func normalizeProvider(in string) string {
	return strings.ToLower(strings.TrimSpace(in))
}

func joinNames(searchers []search.Searcher) string {
	if len(searchers) == 0 {
		return "(none)"
	}
	out := searchers[0].Name()
	for _, s := range searchers[1:] {
		out += ", " + s.Name()
	}
	return out
}

func boolPtr(b bool) *bool { return &b }

func normalizeFreshness(in string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(in))
	if v == "" {
		return "", nil
	}
	switch v {
	case "day", "week", "month":
		return v, nil
	default:
		return "", fmt.Errorf("freshness must be one of: day, week, month")
	}
}
