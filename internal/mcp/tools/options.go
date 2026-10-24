package tools

import (
	"time"

	"github.com/frugalsh/frugal/internal/cache"
	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/routing"
)

// toolOptions carries the optional routing knobs a RegisterX call can
// set. The zero value — cheap strategy, nothing denied, no latency
// source — reproduces the historical routing exactly, which is what
// keeps every pre-policy call site and test behaving identically.
type toolOptions struct {
	policy routing.Policy
	lat    routing.LatencyLookup
	// policies / latFor are the per-capability variants frugal__execute
	// consumes — it routes across all three capabilities, so a single
	// policy/lookup pair isn't enough.
	policies map[string]routing.Policy
	latFor   func(tool string) routing.LatencyLookup
	// guard enforces per-provider daily spend caps and the rate-limit
	// cooldown. A nil guard is a safe no-op on every method, so the
	// enforcement call sites need no conditionals and the zero value keeps
	// the historical behavior exactly.
	guard *routing.Guard
	// maxChars is the operator's default character budget for the
	// content fields of extract / browse / execute results (see
	// internal/limit). Zero, the default, means unlimited, which keeps
	// the historical payloads byte-for-byte. A per-call max_chars
	// argument overrides it in either direction.
	maxChars int
	// resultCache memoizes successful search / extract results so a
	// repeated call inside the TTL costs nothing. Nil (the default)
	// disables caching entirely; the cache methods are nil-safe, so the
	// handlers consult it unconditionally. Browse is never cached.
	resultCache *cache.Cache
	searchTTL   time.Duration
	extractTTL  time.Duration

	// domains is the operator's allow / deny rule for fetch targets.
	// Consulted before routing on every URL-taking call and applied to
	// search result lists. Nil allows everything.
	domains *routing.DomainPolicy

	// callTimeout is the overall wall-clock budget for one tool call,
	// across every failover attempt. Zero (the zero value) disables it;
	// mcp serve sets routing.DefaultCallTimeout unless configured.
	callTimeout time.Duration

	// readers is the extract chain frugal__search opens top hits with
	// when the caller sets read_top, under the extract capability's own
	// policy and latency source (see WithPageReaders). Nil (the default)
	// leaves read_top unavailable and the search tool unchanged.
	readers    []extract.Extractor
	readPolicy routing.Policy
	readLat    routing.LatencyLookup
}

// WithCallTimeout bounds each tool call — the whole failover chain — to d.
// Zero or negative disables the overall deadline.
func WithCallTimeout(d time.Duration) ToolOption {
	return func(o *toolOptions) {
		if d < 0 {
			d = 0
		}
		o.callTimeout = d
	}
}

// ToolOption configures a routed tool at registration time.
type ToolOption func(*toolOptions)

// WithPolicy sets the operator's routing policy for this capability.
func WithPolicy(p routing.Policy) ToolOption {
	return func(o *toolOptions) { o.policy = p }
}

// WithLatencyLookup supplies the ledger-backed latency source the fast
// strategy ranks on. Without one, fast degrades to cost order.
func WithLatencyLookup(l routing.LatencyLookup) ToolOption {
	return func(o *toolOptions) { o.lat = l }
}

// WithPolicies sets the per-capability policies ("search" / "extract" /
// "browse") frugal__execute routes under. Missing keys keep the cheap
// default.
func WithPolicies(m map[string]routing.Policy) ToolOption {
	return func(o *toolOptions) { o.policies = m }
}

// WithLatencyLookupFor supplies a per-capability latency source for
// frugal__execute.
func WithLatencyLookupFor(f func(tool string) routing.LatencyLookup) ToolOption {
	return func(o *toolOptions) { o.latFor = f }
}

// WithGuard supplies the spend-cap / cooldown guard the enforcement layer
// consults before each routed call. Nil (the default) disables both rails.
func WithGuard(g *routing.Guard) ToolOption {
	return func(o *toolOptions) { o.guard = g }
}

// WithMaxChars sets the default character budget applied to the content
// of extract, browse, and execute results when the caller does not pass
// max_chars. Zero or negative disables the default cap.
func WithMaxChars(n int) ToolOption {
	return func(o *toolOptions) {
		if n < 0 {
			n = 0
		}
		o.maxChars = n
	}
}

// effectiveMaxChars resolves the budget for one call: the caller's
// max_chars wins when set, otherwise the operator default, otherwise
// unlimited (0).
func (o toolOptions) effectiveMaxChars(requested int) int {
	if requested > 0 {
		return requested
	}
	return o.maxChars
}

// WithResultCache supplies the exact-match result cache and its
// per-capability TTLs. A nil cache or a TTL at or below zero disables
// caching for the affected capability. The same *cache.Cache instance
// should back every routed tool so frugal__execute and the direct
// tools share entries.
func WithResultCache(c *cache.Cache, searchTTL, extractTTL time.Duration) ToolOption {
	return func(o *toolOptions) {
		o.resultCache = c
		o.searchTTL = searchTTL
		o.extractTTL = extractTTL
	}
}

// WithDomainPolicy supplies the fetch-target allow / deny policy. Nil
// (the default) leaves every host fetchable.
func WithDomainPolicy(p *routing.DomainPolicy) ToolOption {
	return func(o *toolOptions) { o.domains = p }
}

func buildToolOptions(opts []ToolOption) toolOptions {
	var o toolOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
