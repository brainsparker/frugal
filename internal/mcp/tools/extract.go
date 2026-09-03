package tools

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/limit"
	"github.com/frugalsh/frugal/internal/obs"
	"github.com/frugalsh/frugal/internal/routing"
)

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
	// MaxChars caps the content returned (markdown + text + html,
	// shared). Zero falls back to the server's configured default, which
	// is unlimited unless the operator set limits.max_chars.
	MaxChars int `json:"max_chars,omitempty" jsonschema:"optional cap on returned content characters (markdown, text, and html combined); the response reports truncated, chars_returned, and chars_total so you can re-call with a larger value; 0 = server default"`
}

// ExtractOutput is the structured-content payload returned to the MCP
// client. Markdown is the primary read; HTML / Text / Title / Byline /
// Links are populated when the driver supplies them. CostUSD +
// ProviderUsed + LatencyMS make the routing decision auditable, and
// the size footer (CharsReturned / CharsTotal / Truncated / EstTokens)
// makes the context cost auditable in the same breath.
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
	// CharsReturned / CharsTotal / Truncated report what the max_chars
	// budget did: how much content is in this response, how much the
	// provider produced, and whether the two differ. EstTokens is the
	// approximate context cost of the returned content
	// (limit.CharsPerToken characters per token).
	CharsReturned int  `json:"chars_returned"`
	CharsTotal    int  `json:"chars_total"`
	Truncated     bool `json:"truncated,omitempty"`
	EstTokens     int  `json:"est_tokens"`
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
		if in.MaxChars < 0 {
			return nil, ExtractOutput{}, fmt.Errorf("frugal__extract: max_chars must be zero (server default) or positive")
		}
		q := extract.Query{URL: in.URL, Formats: in.Formats}
		logger := slog.Default()

		start := time.Now()
		var (
			used extract.Extractor
			res  extract.Result
			err  error
		)
		provider := normalizeProvider(in.Provider)
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

		out := ExtractOutput{
			Markdown:     res.Markdown,
			HTML:         res.HTML,
			Text:         res.Text,
			Title:        res.Title,
			Byline:       res.Byline,
			Links:        res.Links,
			CostUSD:      res.CostUSD,
			ProviderUsed: used.Name(),
			LatencyMS:    latency,
		}
		// Markdown first: it is the rendering agents actually read. Raw
		// HTML is the bulkiest and least useful, so it is the first to
		// be dropped when the budget is tight.
		rep := limit.Cap(o.effectiveMaxChars(in.MaxChars), &out.Markdown, &out.Text, &out.HTML)
		out.CharsReturned, out.CharsTotal, out.Truncated = rep.CharsReturned, rep.CharsTotal, rep.Truncated
		out.EstTokens = limit.EstTokens(rep.CharsReturned)
		return nil, out, nil
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
