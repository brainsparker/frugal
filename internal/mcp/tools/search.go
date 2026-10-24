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

	"github.com/frugalsh/frugal/internal/cache"
	"github.com/frugalsh/frugal/internal/limit"
	"github.com/frugalsh/frugal/internal/obs"
	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// cachedSearch is the cache entry the search-capable tools share:
// frugal__search and frugal__execute write and read the same shape, so
// a repeat of the same query through either tool is a hit.
type cachedSearch struct {
	Items    []search.Item
	Warnings []string
	Provider string
	CostUSD  float64
}

// SearchInput is the JSON-schema-generating shape for frugal__search.
// Field tags drive the schema the MCP client sees — keep names + descriptions
// stable across releases, since agents pattern-match on them.
type SearchInput struct {
	Query      string `json:"query" jsonschema:"the search query"`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"max results to return (default 5, clamped to 20)"`
	Freshness  string `json:"freshness,omitempty" jsonschema:"optional time window: day | week | month"`
	// Provider pins the search provider for this call ("searxng", "serper",
	// "youcom", "brave", …). When empty or "auto", the configured routing
	// policy picks (cheapest-first by default). Use a pin when a specific
	// provider is known to have materially better recall for this call.
	Provider string `json:"provider,omitempty" jsonschema:"optional provider override: searxng | marginalia | youcom-free | wikipedia | serper | youcom | auto"`
	// MaxCostUSD is the caller's per-call spend ceiling. Providers whose
	// effective per-call cost exceeds it are removed from the chain before
	// any call is made; 0 means free rungs only. A pointer so that an
	// explicit 0 is distinguishable from "not declared".
	MaxCostUSD *float64 `json:"max_cost_usd,omitempty" jsonschema:"optional per-call spend ceiling in USD: providers priced above it are never called for this request; 0 means free providers only"`
	// ReadTop asks for the top N hits to be opened through the extract
	// chain in this same call, so the agent reads the pages instead of
	// answering from snippets. 0 (the default) keeps the snippets-only
	// behavior byte-for-byte. See readtop.go.
	ReadTop int `json:"read_top,omitempty" jsonschema:"optional: also read the top N result pages (1 to 5) through the extract chain in this call and return their content under pages[], each with a fetch receipt (provider, cost, chars, truncated, age_days or undated); the pages share this call's deadline and max_cost_usd; 0 (default) returns snippets only"`
	// PageMaxChars caps each page's returned markdown when ReadTop is
	// set. Zero resolves to the operator's limits.max_chars, then to
	// DefaultPageMaxChars; a page read is never unlimited.
	PageMaxChars int `json:"page_max_chars,omitempty" jsonschema:"optional per-page cap on returned markdown characters when read_top is set (default 4000, or the server's limits.max_chars when configured); each page reports truncated, chars_returned, and chars_total so you can re-read one URL with frugal__extract and a larger max_chars"`
}

// SearchOutput is the structured-content payload returned to the MCP
// client. The result list is what the agent reads to compose its answer;
// CostUSD + ProviderUsed + LatencyMS are the observability footer that
// makes the routing decision auditable in agent traces.
type SearchOutput struct {
	Results []search.Item `json:"results"`
	CostUSD float64       `json:"cost_usd"`
	// TotalCostUSD sums every attempt's cost_usd; Attempts lists each
	// provider call in order (see Attempt).
	TotalCostUSD float64   `json:"total_cost_usd"`
	Attempts     []Attempt `json:"attempts,omitempty"`
	ProviderUsed string    `json:"provider_used"`
	LatencyMS    int64     `json:"latency_ms"`
	// Warnings carries degraded-service notes from the winning provider —
	// e.g. "freshness window ignored" from drivers without a time filter —
	// so the agent can react (re-query pinned to serper/youcom) instead of
	// mistaking best-effort results for exact ones.
	Warnings []string `json:"warnings,omitempty" jsonschema:"degraded-service notes, e.g. a provider that ignored the freshness window"`
	// Cached / CacheAgeMS report when the response came from the local
	// result cache instead of a provider call. cost_usd is 0 on a hit:
	// nothing was paid for this call. provider_used still names the
	// provider that produced the original result.
	Cached     bool  `json:"cached,omitempty" jsonschema:"true when served from the local result cache without a provider call"`
	CacheAgeMS int64 `json:"cache_age_ms,omitempty" jsonschema:"age of the cached result in milliseconds (cache hits only)"`
	// EstTokens is the approximate context cost of the result list
	// (titles, URLs, snippets) plus any pages read, so the agent can see
	// what a wide max_results or read_top actually buys. Search results
	// are never truncated; max_results is the size knob for the list and
	// page_max_chars for the pages.
	EstTokens int `json:"est_tokens"`
	// Pages holds the top hits read through the extract chain when the
	// caller set read_top, in result order, one entry per hit attempted
	// (including the ones that failed, with fetch_status "error").
	// PagesRead / PagesFailed summarize them. cost_usd above stays the
	// search provider's price; page costs are on each Page and in
	// total_cost_usd and attempts[] (capability "extract").
	Pages       []Page `json:"pages,omitempty" jsonschema:"top results read through the extract chain (read_top only), each with content and a fetch receipt; a page that could not be fetched is listed with fetch_status error rather than dropped"`
	PagesRead   int    `json:"pages_read,omitempty" jsonschema:"number of pages whose content was fetched (read_top only)"`
	PagesFailed int    `json:"pages_failed,omitempty" jsonschema:"number of pages that could not be fetched (read_top only)"`
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
	o := buildToolOptions(opts)
	if metrics != nil {
		for _, s := range searchers {
			metrics.EnsureProvider(s.Name(), "search")
		}
		for _, e := range o.readers {
			metrics.EnsureProvider(e.Name(), "extract")
		}
	}
	desc := fmt.Sprintf(
		"Run a web search routed across %s. Returns a list of {title, url, snippet} hits "+
			"plus the actual provider used and cost paid. Provider choice follows the "+
			"configured routing policy (cheapest-first by default, with automatic failover); "+
			"pin via the `provider` argument, or cap this call's spend with `max_cost_usd` "+
			"(0 = free providers only).",
		joinNames(searchers),
	)
	if len(o.readers) > 0 {
		desc += fmt.Sprintf(
			" Set `read_top` (1 to %d) to also open the top hits through the extract chain (%s) "+
				"in the same call: `pages[]` returns each page's markdown (capped by `page_max_chars`, "+
				"default %d) with a fetch receipt, so you can answer from the page rather than the "+
				"snippet; dead links are reported, not dropped.",
			MaxReadTop, joinExtractorNames(o.readers), DefaultPageMaxChars,
		)
	}
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
	}, withAttempts(makeSearchHandler(searchers, metrics, o), func(o *SearchOutput, a []Attempt, t float64) { o.Attempts, o.TotalCostUSD = a, t }))
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
	// Page reads book under the extract capability: metrics, budgets,
	// and the cooldown all see them as what they are.
	var pageHook routing.AttemptHook
	if metrics != nil {
		pageHook = func(provider string, latency time.Duration, costUSD float64, won bool, err error) {
			metrics.RecordCall(provider, latency, costUSD, won, err)
		}
	}
	pageHook = composeHook(o.guard, "extract", pageHook)

	return func(ctx context.Context, _ *sdkmcp.CallToolRequest, in SearchInput) (*sdkmcp.CallToolResult, SearchOutput, error) {
		ctx, cancelCall := routing.WithCallDeadline(ctx, o.callTimeout)
		defer cancelCall()
		if in.Query == "" {
			return nil, SearchOutput{}, fmt.Errorf("frugal__search: query is required")
		}
		freshness, err := normalizeFreshness(in.Freshness)
		if err != nil {
			return nil, SearchOutput{}, fmt.Errorf("frugal__search: %w", err)
		}
		ceiling, err := routing.CeilingFrom(in.MaxCostUSD)
		if err != nil {
			return nil, SearchOutput{}, fmt.Errorf("frugal__search: %w", err)
		}
		readTop, err := normalizeReadTop(in.ReadTop)
		if err != nil {
			return nil, SearchOutput{}, fmt.Errorf("frugal__search: %w", err)
		}
		if readTop > 0 && len(o.readers) == 0 {
			return nil, SearchOutput{}, fmt.Errorf("frugal__search: read_top needs an extract provider and none is configured; go-readability is built in, so this server was started without extract support")
		}
		if in.PageMaxChars < 0 {
			return nil, SearchOutput{}, fmt.Errorf("frugal__search: page_max_chars must be zero (default) or positive")
		}
		// With pages in the receipt, label the search attempts too, so
		// attempts[] reads unambiguously. Snippets-only calls keep the
		// historical unlabeled entries.
		attemptCapability := ""
		if readTop > 0 {
			attemptCapability = "search"
		}
		hook := attemptLogFrom(ctx).wrap(attemptCapability, hook)
		pageHook := attemptLogFrom(ctx).wrap("extract", pageHook)
		q := search.Query{
			Text:       in.Query,
			MaxResults: in.MaxResults,
			Freshness:  freshness,
		}
		logger := slog.Default()

		provider := normalizeProvider(in.Provider)
		// Loop rail: refuse the one-too-many identical call before any
		// provider is paid for it. Keyed on the normalized input, so
		// whitespace and provider spelling don't split a loop.
		if err := admitCall(o.guard, "frugal__search", SearchInput{
			Query:        strings.TrimSpace(in.Query),
			MaxResults:   in.MaxResults,
			Freshness:    freshness,
			Provider:     provider,
			ReadTop:      readTop,
			PageMaxChars: in.PageMaxChars,
		}, logger); err != nil {
			return nil, SearchOutput{}, err
		}
		start := time.Now()

		// Result cache: an identical call inside the TTL is answered
		// from memory, costing nothing. Key covers every argument that
		// changes what a provider would return. Nil cache is a no-op.
		// read_top is not part of the key: the hit list is the same
		// either way, and the pages are read (or served from the extract
		// cache) below.
		key := cache.SearchKey(provider, in.Query, in.MaxResults, freshness)
		if v, age, ok := o.resultCache.Get(key); ok {
			if hit, isSearch := v.(cachedSearch); isSearch {
				out := SearchOutput{
					Results:      hit.Items,
					CostUSD:      0,
					ProviderUsed: hit.Provider,
					Warnings:     hit.Warnings,
					Cached:       true,
					CacheAgeMS:   age.Milliseconds(),
				}
				if readTop > 0 {
					attachPages(ctx, &out, o, readTop, in.PageMaxChars, ceiling, pageHook, logger)
				}
				out.LatencyMS = time.Since(start).Milliseconds()
				return nil, out, nil
			}
		}

		var (
			used      search.Searcher
			res       search.Results
			searchErr error
		)
		if isAuto(provider) {
			now := time.Now()
			ordered, reason := routing.Apply(searchers, o.policy, o.lat, now)
			if len(ordered) == 0 {
				return nil, SearchOutput{}, fmt.Errorf("frugal__search: every configured provider is denied by the routing policy")
			}
			ordered, reason = guardChain(o.guard, "search", ordered, reason, logger)
			if len(ordered) == 0 {
				return nil, SearchOutput{}, fmt.Errorf("frugal__search: %w", guardEmptyError("search"))
			}
			affordable := ordered
			ordered, reason = routing.ApplyCeiling(ordered, ceiling, reason, now)
			if len(ordered) == 0 {
				return nil, SearchOutput{}, fmt.Errorf("frugal__search: %w", routing.CeilingEmptyError("search", ceiling, affordable, now))
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
		} else if ok, why := pinUnderCeiling(searchers, provider, ceiling); !ok {
			// A pin above the caller's own ceiling is a contradiction in
			// the request; refuse before spending.
			return nil, SearchOutput{}, fmt.Errorf("frugal__search: %s", why)
		} else {
			used, res, searchErr = search.CallPinned(ctx, searchers, provider, q, logger, hook)
		}
		latency := time.Since(start).Milliseconds()
		if searchErr != nil {
			return nil, SearchOutput{}, fmt.Errorf("frugal__search: %w", searchErr)
		}

		o.resultCache.Put(key, cachedSearch{
			Items:    res.Items,
			Warnings: res.Warnings,
			Provider: used.Name(),
			CostUSD:  res.CostUSD,
		}, res.CostUSD, o.searchTTL)

		items, dropped := filterItems(o.domains, res.Items)
		warnings := res.Warnings
		if dropped > 0 {
			logger.Warn("domain policy: search results dropped", "tool", "frugal__search", "dropped", dropped)
			warnings = append(append([]string(nil), warnings...), domainDropWarning(dropped))
		}
		out := SearchOutput{
			Results:      items,
			CostUSD:      res.CostUSD,
			ProviderUsed: used.Name(),
			LatencyMS:    latency,
			Warnings:     warnings,
			EstTokens:    limit.EstTokens(itemChars(items)),
		}
		if readTop > 0 {
			attachPages(ctx, &out, o, readTop, in.PageMaxChars, ceiling, pageHook, logger)
			out.LatencyMS = time.Since(start).Milliseconds()
		}
		return nil, out, nil
	}
}

// attachPages runs the read_top step on a finished search output: it
// opens the top n hits, fills pages[] and the page counters, and folds
// the page content into est_tokens. The ceiling passed in is the
// caller's for the whole call; what the search spent comes off first.
func attachPages(ctx context.Context, out *SearchOutput, o toolOptions, n int, pageMaxChars int, ceiling routing.Ceiling, hook routing.AttemptHook, logger *slog.Logger) {
	if n > len(out.Results) {
		n = len(out.Results)
	}
	pages := readTopPages(ctx, o, out.Results, n, o.effectivePageMaxChars(pageMaxChars), pageCeiling(ceiling, out.CostUSD, n), hook, logger)
	out.Pages = pages
	out.PagesRead, out.PagesFailed = countPages(pages)
	out.EstTokens = limit.EstTokens(itemChars(out.Results) + pageChars(pages))
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

// itemChars counts the characters a result list puts into the agent's
// context: title, URL, and snippet per hit.
func itemChars(items []search.Item) int {
	total := 0
	for _, it := range items {
		total += limit.Count(it.Title, it.URL, it.Snippet)
	}
	return total
}

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
