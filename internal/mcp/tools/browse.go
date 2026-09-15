package tools

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/browse"
	"github.com/frugalsh/frugal/internal/limit"
	"github.com/frugalsh/frugal/internal/obs"
	"github.com/frugalsh/frugal/internal/routing"
)

// BrowseInput is the JSON-schema-generating shape for frugal__browse.
type BrowseInput struct {
	URL string `json:"url" jsonschema:"the URL to render"`
	// WaitMs is the optional millisecond pause after DOM-ready, giving
	// the page time to finish XHR / hydration.
	WaitMs int `json:"wait_ms,omitempty" jsonschema:"optional wait after initial DOM-ready, in milliseconds"`
	// Format picks the result shape: "html" (default) or "text".
	Format string `json:"format,omitempty" jsonschema:"return format: html | text"`
	// Provider pins the browse provider for this call. Empty / "auto"
	// → the routing policy decides.
	Provider string `json:"provider,omitempty" jsonschema:"optional provider override: browserless | auto"`
	// MaxChars caps the rendered content returned (text + html, shared).
	// Zero falls back to the server's configured default.
	MaxChars int `json:"max_chars,omitempty" jsonschema:"optional cap on returned content characters (text and html combined); the response reports truncated, chars_returned, and chars_total; 0 = server default"`
}

// BrowseOutput is the structured-content payload returned to the MCP
// client. HTML is the primary read; Text is populated when Format ==
// "text". CostUSD + ProviderUsed + LatencyMS make the routing decision
// auditable; the size footer makes the context cost auditable.
type BrowseOutput struct {
	HTML         string  `json:"html,omitempty"`
	Text         string  `json:"text,omitempty"`
	CostUSD      float64 `json:"cost_usd"`
	ProviderUsed string  `json:"provider_used"`
	LatencyMS    int64   `json:"latency_ms"`
	// See ExtractOutput for the meaning of the size footer.
	CharsReturned int  `json:"chars_returned"`
	CharsTotal    int  `json:"chars_total"`
	Truncated     bool `json:"truncated,omitempty"`
	EstTokens     int  `json:"est_tokens"`
}

// RegisterBrowse wires frugal__browse onto the given MCP server.
// browsers is the operator-configured list. A no-op when empty —
// tools/list won't advertise a tool we can't fulfill.
func RegisterBrowse(server *sdkmcp.Server, browsers []browse.Browser, metrics *obs.Metrics, opts ...ToolOption) {
	if len(browsers) == 0 {
		return
	}
	if metrics != nil {
		for _, b := range browsers {
			metrics.EnsureProvider(b.Name(), "browse")
		}
	}
	desc := fmt.Sprintf(
		"Render a URL with a real JS-capable headless browser, routed across %s. "+
			"Use when frugal__extract returns empty content (page requires JS) or "+
			"when the agent specifically needs the post-render DOM. Returns HTML "+
			"(default) or stripped plain text (format=\"text\").",
		joinBrowserNames(browsers),
	)
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        "frugal__browse",
		Title:       "Headless render (routed)",
		Description: desc,
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  false, // JS execution is not deterministic
			OpenWorldHint:   boolPtr(true),
		},
	}, makeBrowseHandler(browsers, metrics, buildToolOptions(opts)))
}

func makeBrowseHandler(browsers []browse.Browser, metrics *obs.Metrics, o toolOptions) func(context.Context, *sdkmcp.CallToolRequest, BrowseInput) (*sdkmcp.CallToolResult, BrowseOutput, error) {
	var hook browse.AttemptHook
	if metrics != nil {
		hook = func(provider string, latency time.Duration, costUSD float64, won bool, err error) {
			metrics.RecordCall(provider, latency, costUSD, won, err)
		}
	}
	hook = composeHook(o.guard, "browse", hook)

	return func(ctx context.Context, _ *sdkmcp.CallToolRequest, in BrowseInput) (*sdkmcp.CallToolResult, BrowseOutput, error) {
		if in.URL == "" {
			return nil, BrowseOutput{}, fmt.Errorf("frugal__browse: url is required")
		}
		if in.MaxChars < 0 {
			return nil, BrowseOutput{}, fmt.Errorf("frugal__browse: max_chars must be zero (server default) or positive")
		}
		q := browse.Query{URL: in.URL, WaitForMS: in.WaitMs, ReturnFormat: in.Format}
		logger := slog.Default()

		start := time.Now()
		var (
			used browse.Browser
			res  browse.Result
			err  error
		)
		provider := normalizeProvider(in.Provider)
		if isAuto(provider) {
			ordered, reason := routing.Apply(browsers, o.policy, o.lat, time.Now())
			if len(ordered) == 0 {
				return nil, BrowseOutput{}, fmt.Errorf("frugal__browse: every configured provider is denied by the routing policy")
			}
			ordered, reason = guardChain(o.guard, "browse", ordered, reason, logger)
			if len(ordered) == 0 {
				return nil, BrowseOutput{}, fmt.Errorf("frugal__browse: %w", guardEmptyError("browse"))
			}
			logger.Debug("browse routing", "reason", reason)
			used, res, err = browse.CallInOrder(ctx, ordered, q, logger, hook)
		} else if o.policy.Deny[provider] {
			// Deny means "never call" — a pin doesn't override it.
			return nil, BrowseOutput{}, fmt.Errorf("frugal__browse: provider %q is denied by the routing policy", provider)
		} else if ok, why := o.guard.Allow("browse", provider); !ok {
			// Budget / cooldown blocks a pin the same way deny does.
			return nil, BrowseOutput{}, fmt.Errorf("frugal__browse: provider %q unavailable: %s", provider, why)
		} else {
			used, res, err = browse.CallPinned(ctx, browsers, provider, q, logger, hook)
		}
		latency := time.Since(start).Milliseconds()
		if err != nil {
			return nil, BrowseOutput{}, fmt.Errorf("frugal__browse: %w", err)
		}

		out := BrowseOutput{
			HTML:         res.HTML,
			Text:         res.Text,
			CostUSD:      res.CostUSD,
			ProviderUsed: used.Name(),
			LatencyMS:    latency,
		}
		// Text first when both are present: the stripped rendering is
		// what a budget-conscious caller asked for, the DOM is the bulk.
		rep := limit.Cap(o.effectiveMaxChars(in.MaxChars), &out.Text, &out.HTML)
		out.CharsReturned, out.CharsTotal, out.Truncated = rep.CharsReturned, rep.CharsTotal, rep.Truncated
		out.EstTokens = limit.EstTokens(rep.CharsReturned)
		return nil, out, nil
	}
}

func joinBrowserNames(browsers []browse.Browser) string {
	if len(browsers) == 0 {
		return "(none)"
	}
	out := browsers[0].Name()
	for _, b := range browsers[1:] {
		out += ", " + b.Name()
	}
	return out
}
