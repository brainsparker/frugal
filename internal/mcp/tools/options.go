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

func buildToolOptions(opts []ToolOption) toolOptions {
	var o toolOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
