package tools

import (
	"time"

	"github.com/frugalsh/frugal/internal/cache"
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
	// resultCache memoizes successful search / extract results so a
	// repeated call inside the TTL costs nothing. Nil (the default)
	// disables caching entirely; the cache methods are nil-safe, so the
	// handlers consult it unconditionally. Browse is never cached.
	resultCache *cache.Cache
	searchTTL   time.Duration
	extractTTL  time.Duration
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

func buildToolOptions(opts []ToolOption) toolOptions {
	var o toolOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
