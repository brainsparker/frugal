package tools

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/cache"
	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/obs"
	"github.com/frugalsh/frugal/internal/routing"
)

// cachedExtract is the cache entry the extract-capable tools share.
// It carries the full extract.Result so a frugal__extract hit loses no
// fields (byline, links) regardless of which tool stored the entry.
type cachedExtract struct {
	Res      extract.Result
	Provider string
}

// ExtractInput is the JSON-schema-generating shape for frugal__extract.
// Field tags drive the schema the MCP client sees — keep names + descriptions
// stable across releases, since agents pattern-match on them.
type ExtractInput struct {
	URL string `json:"url" jsonschema:"the URL to extract"`
	// Formats picks which output fields the caller wants. Recognized:
	// "markdown" (default), "html", "text". Drivers may opportunistically
	// populate others.
	Formats []string `json:"formats,omitempty" jsonschema:"output formats: markdown | html | text"`
	// Provider pins the extract provider for this call ("goreadability",
	// "firecrawl", …). Empty / "auto" → the routing policy decides.
	Provider string `json:"provider,omitempty" jsonschema:"optional provider override: goreadability | firecrawl | auto"`
}

// ExtractOutput is the structured-content payload returned to the MCP
// client. Markdown is the primary read; HTML / Text / Title / Byline /
// Links are populated when the driver supplies them. CostUSD +
// ProviderUsed + LatencyMS make the routing decision auditable.
type ExtractOutput struct {
	Markdown     string   `json:"markdown,omitempty"`
	HTML         string   `json:"html,omitempty"`
	Text         string   `json:"text,omitempty"`
	Title        string   `json:"title,omitempty"`
	Byline       string   `json:"byline,omitempty"`
	Links        []string `json:"links,omitempty"`
	CostUSD      float64  `json:"cost_usd"`
	ProviderUsed string   `json:"provider_used"`
	LatencyMS    int64    `json:"latency_ms"`
	// Cached / CacheAgeMS report when the response came from the local
	// result cache instead of a provider call. cost_usd is 0 on a hit.
	Cached     bool  `json:"cached,omitempty" jsonschema:"true when served from the local result cache without a provider call"`
	CacheAgeMS int64 `json:"cache_age_ms,omitempty" jsonschema:"age of the cached result in milliseconds (cache hits only)"`
}

// RegisterExtract wires frugal__extract onto the given MCP server.
// extractors is the operator-configured list. A no-op when empty —
// no ghost tools in tools/list. Pass metrics (non-nil) to record
// per-attempt call counts, errors, latency, and cost.
func RegisterExtract(server *sdkmcp.Server, extractors []extract.Extractor, metrics *obs.Metrics, opts ...ToolOption) {
	if len(extractors) == 0 {
		return
	}
	if metrics != nil {
		for _, e := range extractors {
			metrics.EnsureProvider(e.Name(), "extract")
		}
	}
	desc := fmt.Sprintf(
		"Extract the main article content from a URL, routed across %s. Returns "+
			"markdown / html / text + metadata (title, byline). Provider choice "+
			"follows the configured routing policy (cheapest-first by default: "+
			"typically a local Readability pass, falling back to a paid scraper "+
			"when the page needs JS).",
		joinExtractorNames(extractors),
	)
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        "frugal__extract",
		Title:       "Page extract (routed)",
		Description: desc,
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  true, // extracting the same URL twice should yield the same content
			OpenWorldHint:   boolPtr(true),
		},
	}, makeExtractHandler(extractors, metrics, buildToolOptions(opts)))
}

func makeExtractHandler(extractors []extract.Extractor, metrics *obs.Metrics, o toolOptions) func(context.Context, *sdkmcp.CallToolRequest, ExtractInput) (*sdkmcp.CallToolResult, ExtractOutput, error) {
	var hook extract.AttemptHook
	if metrics != nil {
		hook = func(provider string, latency time.Duration, costUSD float64, won bool, err error) {
			metrics.RecordCall(provider, latency, costUSD, won, err)
		}
	}
	hook = composeHook(o.guard, "extract", hook)

	return func(ctx context.Context, _ *sdkmcp.CallToolRequest, in ExtractInput) (*sdkmcp.CallToolResult, ExtractOutput, error) {
		if in.URL == "" {
			return nil, ExtractOutput{}, fmt.Errorf("frugal__extract: url is required")
		}
		q := extract.Query{URL: in.URL, Formats: in.Formats}
		logger := slog.Default()
		provider := normalizeProvider(in.Provider)

		start := time.Now()

		// Result cache: same URL, formats, and pin inside the TTL is
		// answered from memory. Nil cache is a no-op.
		key := cache.ExtractKey(provider, in.URL, in.Formats)
		if v, age, ok := o.resultCache.Get(key); ok {
			if hit, isExtract := v.(cachedExtract); isExtract {
				return nil, ExtractOutput{
					Markdown:     hit.Res.Markdown,
					HTML:         hit.Res.HTML,
					Text:         hit.Res.Text,
					Title:        hit.Res.Title,
					Byline:       hit.Res.Byline,
					Links:        hit.Res.Links,
					CostUSD:      0,
					ProviderUsed: hit.Provider,
					LatencyMS:    time.Since(start).Milliseconds(),
					Cached:       true,
					CacheAgeMS:   age.Milliseconds(),
				}, nil
			}
		}

		var (
			used extract.Extractor
			res  extract.Result
			err  error
		)
		if isAuto(provider) {
			ordered, reason := routing.Apply(extractors, o.policy, o.lat, time.Now())
			if len(ordered) == 0 {
				return nil, ExtractOutput{}, fmt.Errorf("frugal__extract: every configured provider is denied by the routing policy")
			}
			ordered, reason = guardChain(o.guard, "extract", ordered, reason, logger)
			if len(ordered) == 0 {
				return nil, ExtractOutput{}, fmt.Errorf("frugal__extract: %w", guardEmptyError("extract"))
			}
			logger.Debug("extract routing", "reason", reason)
			used, res, err = extract.CallInOrder(ctx, ordered, q, logger, hook)
		} else if o.policy.Deny[provider] {
			// Deny means "never call" — a pin doesn't override it.
			return nil, ExtractOutput{}, fmt.Errorf("frugal__extract: provider %q is denied by the routing policy", provider)
		} else if ok, why := o.guard.Allow("extract", provider); !ok {
			// Budget / cooldown blocks a pin the same way deny does.
			return nil, ExtractOutput{}, fmt.Errorf("frugal__extract: provider %q unavailable: %s", provider, why)
		} else {
			used, res, err = extract.CallPinned(ctx, extractors, provider, q, logger, hook)
		}
		latency := time.Since(start).Milliseconds()
		if err != nil {
			return nil, ExtractOutput{}, fmt.Errorf("frugal__extract: %w", err)
		}

		o.resultCache.Put(key, cachedExtract{Res: res, Provider: used.Name()}, res.CostUSD, o.extractTTL)

		return nil, ExtractOutput{
			Markdown:     res.Markdown,
			HTML:         res.HTML,
			Text:         res.Text,
			Title:        res.Title,
			Byline:       res.Byline,
			Links:        res.Links,
			CostUSD:      res.CostUSD,
			ProviderUsed: used.Name(),
			LatencyMS:    latency,
		}, nil
	}
}

func joinExtractorNames(extractors []extract.Extractor) string {
	if len(extractors) == 0 {
		return "(none)"
	}
	out := extractors[0].Name()
	for _, e := range extractors[1:] {
		out += ", " + e.Name()
	}
	return out
}
