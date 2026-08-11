package tools

import (
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

func buildToolOptions(opts []ToolOption) toolOptions {
	var o toolOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
