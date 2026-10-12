package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/browse"
	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/limit"
	"github.com/frugalsh/frugal/internal/obs"
	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// ExecuteInput is the JSON-schema-generating shape for frugal__execute:
// describe the job, optionally state a routing preference, and Frugal
// picks the capability and provider.
type ExecuteInput struct {
	Intent   string `json:"intent" jsonschema:"what you want done, in plain language; include the URL if you have one"`
	Priority string `json:"priority,omitempty" jsonschema:"routing preference: cheap | balanced | premium (default balanced — the server's configured policy)"`
	Provider string `json:"provider,omitempty" jsonschema:"optional provider pin within the chosen capability"`
	// MaxChars caps page content when the intent resolves to an extract
	// or a render. Search results are never truncated. Zero falls back
	// to the server's configured default.
	MaxChars int `json:"max_chars,omitempty" jsonschema:"optional cap on returned page content characters for extract and browse intents (markdown, text, and html combined); the response reports truncated, chars_returned, and chars_total; 0 = server default"`
}

// ExecuteOutput carries whichever capability's payload the intent
// resolved to, plus the routing trace: capability, provider, cost,
// latency, and a one-line reason explaining the classification and the
// policy that ordered the chain.
type ExecuteOutput struct {
	Capability   string        `json:"capability"`
	Results      []search.Item `json:"results,omitempty"`
	Markdown     string        `json:"markdown,omitempty"`
	Text         string        `json:"text,omitempty"`
	Title        string        `json:"title,omitempty"`
	HTML         string        `json:"html,omitempty"`
	CostUSD      float64       `json:"cost_usd"`
	ProviderUsed string        `json:"provider_used"`
	LatencyMS    int64         `json:"latency_ms"`
	Reason       string        `json:"reason"`
	Warnings     []string      `json:"warnings,omitempty"`
	// Size footer, see ExtractOutput. For search intents CharsTotal and
	// CharsReturned measure the result list and Truncated is never set.
	CharsReturned int  `json:"chars_returned"`
	CharsTotal    int  `json:"chars_total"`
	Truncated     bool `json:"truncated,omitempty"`
	EstTokens     int  `json:"est_tokens"`
}

// RegisterExecute wires frugal__execute onto the given MCP server. The
// tool classifies the intent onto a capability with deterministic URL /
// keyword heuristics (no model call) and routes within that capability
// under the configured policy. Registered only when search providers
// exist — search is the classification default, so without it the tool
// couldn't honor its contract. Attempts are metered under their real
// capability ("search" / "extract" / "browse"), never under an
// "execute" pseudo-tool, so the frugal stats receipt stays truthful.
func RegisterExecute(server *sdkmcp.Server, searchers []search.Searcher, extractors []extract.Extractor, browsers []browse.Browser, metrics *obs.Metrics, opts ...ToolOption) {
	if len(searchers) == 0 {
		return
	}
	if metrics != nil {
		for _, s := range searchers {
			metrics.EnsureProvider(s.Name(), "search")
		}
		for _, e := range extractors {
			metrics.EnsureProvider(e.Name(), "extract")
		}
		for _, b := range browsers {
			metrics.EnsureProvider(b.Name(), "browse")
		}
	}
	capabilities := []string{"search"}
	if len(extractors) > 0 {
		capabilities = append(capabilities, "extract")
	}
	if len(browsers) > 0 {
		capabilities = append(capabilities, "browse")
	}
	desc := fmt.Sprintf(
		"Describe the job in plain language and Frugal completes it: the intent is "+
			"classified onto a capability (%s) by URL and keyword cues, then routed to a "+
			"provider under the configured policy (cheapest by default; priority=premium "+
			"prefers premium-priced providers). The response reports the capability, the "+
			"provider used, the cost paid, and the reason for the decision.",
		strings.Join(capabilities, ", "),
	)
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        "frugal__execute",
		Title:       "Describe the job (routed)",
		Description: desc,
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  false, // search results and rendered pages shift between calls
			OpenWorldHint:   boolPtr(true),
		},
	}, makeExecuteHandler(searchers, extractors, browsers, metrics, buildToolOptions(opts)))
}

func makeExecuteHandler(searchers []search.Searcher, extractors []extract.Extractor, browsers []browse.Browser, metrics *obs.Metrics, o toolOptions) func(context.Context, *sdkmcp.CallToolRequest, ExecuteInput) (*sdkmcp.CallToolResult, ExecuteOutput, error) {
	policyOf := func(capability string) routing.Policy { return o.policies[capability] }
	latOf := func(capability string) routing.LatencyLookup {
		if o.latFor == nil {
			return nil
		}
		return o.latFor(capability)
	}

	return func(ctx context.Context, _ *sdkmcp.CallToolRequest, in ExecuteInput) (*sdkmcp.CallToolResult, ExecuteOutput, error) {
		if strings.TrimSpace(in.Intent) == "" {
			return nil, ExecuteOutput{}, fmt.Errorf("frugal__execute: intent is required")
		}
		priority := strings.ToLower(strings.TrimSpace(in.Priority))
		switch priority {
		case "", "cheap", "balanced", "premium":
		default:
			return nil, ExecuteOutput{}, fmt.Errorf("frugal__execute: priority must be one of: cheap, balanced, premium")
		}
		if in.MaxChars < 0 {
			return nil, ExecuteOutput{}, fmt.Errorf("frugal__execute: max_chars must be zero (server default) or positive")
		}

		it := ClassifyIntent(in.Intent)
		// Priority overrides the strategy only; the operator's order and
		// deny lists always apply. balanced (or empty) means "whatever
		// the config declares", which defaults to cheap.
		polOf := func(capability string) routing.Policy {
			pol := policyOf(capability)
			switch priority {
			case "cheap":
				pol.Strategy = routing.StrategyCheap
				pol.Order = nil
			case "premium":
				pol.Strategy = routing.StrategyPremium
				pol.Order = nil
			}
			return pol
		}

		provider := normalizeProvider(in.Provider)
		if !isAuto(provider) && polOf(it.Capability).Deny[provider] {
			// Deny means "never call" — a pin doesn't override it.
			return nil, ExecuteOutput{}, fmt.Errorf("frugal__execute: provider %q is denied by the routing policy", provider)
		}

		// Budget / cooldown blocks a pin the same way deny does: check
		// before dispatch so a blocked pin errors instead of calling.
		if !isAuto(provider) {
			if ok, why := o.guard.Allow(it.Capability, provider); !ok {
				return nil, ExecuteOutput{}, fmt.Errorf("frugal__execute: provider %q unavailable: %s", provider, why)
			}
		}

		logger := slog.Default()
		start := time.Now()
		out, err := dispatchIntent(ctx, it, polOf, latOf, provider, searchers, extractors, browsers, metrics, o.guard, logger)
		if err != nil {
			return nil, ExecuteOutput{}, fmt.Errorf("frugal__execute: %w", err)
		}
		out.LatencyMS = time.Since(start).Milliseconds()
		applySizeFooter(&out, o.effectiveMaxChars(in.MaxChars))
		return nil, out, nil
	}
}

// applySizeFooter caps page content and fills the size footer on an
// execute result. Search results are measured but never cut: the
// caller's size knob there is max_results, and dropping hits silently
// would misreport what the provider returned. The fall-forward render
// path lands here too, so a JS-rendered page gets the same budget as a
// plain extract.
func applySizeFooter(out *ExecuteOutput, maxChars int) {
	if out.Capability == "search" {
		n := itemChars(out.Results)
		out.CharsReturned, out.CharsTotal = n, n
		out.EstTokens = limit.EstTokens(n)
		return
	}
	rep := limit.Cap(maxChars, &out.Markdown, &out.Text, &out.HTML)
	out.CharsReturned, out.CharsTotal, out.Truncated = rep.CharsReturned, rep.CharsTotal, rep.Truncated
	out.EstTokens = limit.EstTokens(rep.CharsReturned)
}

// hookFor adapts the metrics sink into an AttemptHook while counting
// attempts, so the reason line can report which rung of the chain won.
// capability is the capability the attempt runs under: execute routes
// across all three, so the guard must book spend / trip the cooldown
// under the real capability, never an "execute" pseudo-key. The guard is
// nil-safe, so guardRecord is a no-op when no budgets are configured.
func hookFor(metrics *obs.Metrics, guard *routing.Guard, capability string, attempts *atomic.Int64) routing.AttemptHook {
	return func(provider string, latency time.Duration, costUSD float64, won bool, err error) {
		attempts.Add(1)
		if metrics != nil {
			metrics.RecordCall(provider, latency, costUSD, won, err)
		}
		guardRecord(guard, capability, provider, costUSD, err)
	}
}

func reasonLine(it Intent, pol routing.Policy, applyReason string, provider string, attempt int64) string {
	return fmt.Sprintf("%s; %s; provider=%s won on attempt %d", it.Note, applyReason, provider, attempt)
}

// dispatchIntent runs the classified intent against its capability's
// providers. Extract intents fall forward to browse when the whole
// extract chain produced no content and a non-fatal outcome — the
// JS-rendered-page case frugal__browse's own docs describe — with
// costs summed across both chains. The fall-forward browse runs under
// the browse capability's own policy.
func dispatchIntent(ctx context.Context, it Intent, polOf func(string) routing.Policy, latOf func(string) routing.LatencyLookup, provider string, searchers []search.Searcher, extractors []extract.Extractor, browsers []browse.Browser, metrics *obs.Metrics, guard *routing.Guard, logger *slog.Logger) (ExecuteOutput, error) {
	var attempts atomic.Int64
	hook := hookFor(metrics, guard, it.Capability, &attempts)
	now := time.Now()
	pol := polOf(it.Capability)
	lat := latOf(it.Capability)

	switch it.Capability {
	case "search":
		q := search.Query{Text: it.Query, Freshness: it.Freshness}
		var (
			used   search.Searcher
			res    search.Results
			err    error
			reason string
		)
		if isAuto(provider) {
			ordered, applyReason := routing.Apply(searchers, pol, lat, now)
			if len(ordered) == 0 {
				return ExecuteOutput{}, fmt.Errorf("every configured search provider is denied by the routing policy")
			}
			ordered, applyReason = guardChain(guard, "search", ordered, applyReason, logger)
			if len(ordered) == 0 {
				return ExecuteOutput{}, guardEmptyError("search")
			}
			reason = applyReason
			used, res, err = search.CallInOrder(ctx, ordered, q, logger, hook)
		} else {
			reason = "provider pinned by caller"
			used, res, err = search.CallPinned(ctx, searchers, provider, q, logger, hook)
		}
		if err != nil {
			return ExecuteOutput{}, err
		}
		return ExecuteOutput{
			Capability:   "search",
			Results:      res.Items,
			CostUSD:      res.CostUSD,
			ProviderUsed: used.Name(),
			Reason:       reasonLine(it, pol, reason, used.Name(), attempts.Load()),
			Warnings:     res.Warnings,
		}, nil

	case "extract":
		if len(extractors) == 0 {
			return ExecuteOutput{}, fmt.Errorf("the intent needs a page extract, but no extract providers are configured")
		}
		q := extract.Query{URL: it.URL}
		var (
			used   extract.Extractor
			res    extract.Result
			err    error
			reason string
		)
		if isAuto(provider) {
			ordered, applyReason := routing.Apply(extractors, pol, lat, now)
			if len(ordered) == 0 {
				return ExecuteOutput{}, fmt.Errorf("every configured extract provider is denied by the routing policy")
			}
			ordered, applyReason = guardChain(guard, "extract", ordered, applyReason, logger)
			if len(ordered) == 0 {
				return ExecuteOutput{}, guardEmptyError("extract")
			}
			reason = applyReason
			used, res, err = extract.CallInOrder(ctx, ordered, q, logger, hook)
		} else {
			reason = "provider pinned by caller"
			used, res, err = extract.CallPinned(ctx, extractors, provider, q, logger, hook)
		}

		empty := err == nil && res.Markdown == "" && res.HTML == "" && res.Text == ""
		failedSoft := err != nil && !routing.IsFatal(err) && ctx.Err() == nil
		if (empty || failedSoft) && len(browsers) > 0 && isAuto(provider) {
			// Fall forward: the page most likely needs JS. Fatal errors
			// (the URL itself is gone) never fall forward — a renderer
			// can't fix a 404.
			extractCost := res.CostUSD
			// The fall-forward runs under the browse capability, so its
			// hook must book spend / cooldown under "browse", not the
			// extract key the primary hook uses.
			browseHook := hookFor(metrics, guard, "browse", &attempts)
			out, berr := browseFor(ctx, it, polOf("browse"), latOf("browse"), browsers, guard, browseHook, logger, "text", now)
			if berr == nil {
				out.CostUSD += extractCost
				out.Reason = reasonLine(it, pol, "extract produced no content; fell forward to a headless render", out.ProviderUsed, attempts.Load())
				return out, nil
			}
			if err != nil {
				return ExecuteOutput{}, fmt.Errorf("extract failed (%w) and browse fall-forward failed (%v)", err, berr)
			}
		}
		if err != nil {
			return ExecuteOutput{}, err
		}
		return ExecuteOutput{
			Capability:   "extract",
			Markdown:     res.Markdown,
			HTML:         res.HTML,
			Text:         res.Text,
			Title:        res.Title,
			CostUSD:      res.CostUSD,
			ProviderUsed: used.Name(),
			Reason:       reasonLine(it, pol, reason, used.Name(), attempts.Load()),
		}, nil

	case "browse":
		if len(browsers) == 0 {
			return ExecuteOutput{}, fmt.Errorf("the intent needs a headless render, but no browse providers are configured")
		}
		if !isAuto(provider) {
			used, res, err := browse.CallPinned(ctx, browsers, provider, browse.Query{URL: it.URL}, logger, hook)
			if err != nil {
				return ExecuteOutput{}, err
			}
			return ExecuteOutput{
				Capability:   "browse",
				HTML:         res.HTML,
				Text:         res.Text,
				CostUSD:      res.CostUSD,
				ProviderUsed: used.Name(),
				Reason:       reasonLine(it, pol, "provider pinned by caller", used.Name(), attempts.Load()),
			}, nil
		}
		out, err := browseFor(ctx, it, pol, lat, browsers, guard, hook, logger, "", now)
		if err != nil {
			return ExecuteOutput{}, err
		}
		out.Reason = reasonLine(it, pol, out.Reason, out.ProviderUsed, attempts.Load())
		return out, nil
	}
	return ExecuteOutput{}, fmt.Errorf("unclassifiable intent") // unreachable: ClassifyIntent always returns a capability
}

// browseFor runs the browse chain under the given policy. The returned
// Reason is only the policy apply-reason; callers wrap it into the full
// trace line.
func browseFor(ctx context.Context, it Intent, pol routing.Policy, lat routing.LatencyLookup, browsers []browse.Browser, guard *routing.Guard, hook routing.AttemptHook, logger *slog.Logger, format string, now time.Time) (ExecuteOutput, error) {
	ordered, applyReason := routing.Apply(browsers, pol, lat, now)
	if len(ordered) == 0 {
		return ExecuteOutput{}, fmt.Errorf("every configured browse provider is denied by the routing policy")
	}
	ordered, applyReason = guardChain(guard, "browse", ordered, applyReason, logger)
	if len(ordered) == 0 {
		return ExecuteOutput{}, guardEmptyError("browse")
	}
	used, res, err := browse.CallInOrder(ctx, ordered, browse.Query{URL: it.URL, ReturnFormat: format}, logger, hook)
	if err != nil {
		return ExecuteOutput{}, err
	}
	return ExecuteOutput{
		Capability:   "browse",
		HTML:         res.HTML,
		Text:         res.Text,
		CostUSD:      res.CostUSD,
		ProviderUsed: used.Name(),
		Reason:       applyReason,
	}, nil
}
