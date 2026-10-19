// Per-call cost ceilings: the caller's own cap on what one tool call may
// spend, declared on the call itself (max_cost_usd) rather than in the
// operator's config. Daily budgets (guard.go) protect the operator's
// wallet over a day; the ceiling lets the agent say "this question is
// not worth more than a tenth of a cent" on a per-question basis. Both
// rails run after the policy ordering and before the fallback walk, so
// a provider the ceiling fences off is never called, not even as a
// fallback rung.
package routing

import (
	"fmt"
	"strings"
	"time"
)

// Ceiling is a caller-declared per-call spend cap. The zero value means
// "no ceiling" and leaves every chain untouched, which is what keeps
// every pre-ceiling call site behaving identically. A set ceiling of
// exactly zero is meaningful: free rungs only.
type Ceiling struct {
	MaxUSD float64
	Set    bool
}

// NoCeiling is the explicit "caller declared nothing" value.
var NoCeiling = Ceiling{}

// CeilingFrom builds a Ceiling from an optional tool-input field. A nil
// pointer means the caller declared nothing. A negative value is a
// caller error, surfaced as such rather than silently clamped: a
// negative ceiling has no sensible reading and most likely means the
// agent mis-filled the argument.
func CeilingFrom(maxUSD *float64) (Ceiling, error) {
	if maxUSD == nil {
		return NoCeiling, nil
	}
	if *maxUSD < 0 {
		return NoCeiling, fmt.Errorf("max_cost_usd must be zero or positive, got %g", *maxUSD)
	}
	return Ceiling{MaxUSD: *maxUSD, Set: true}, nil
}

// Allows reports whether a provider's effective per-call cost fits under
// the ceiling, with a reason naming both numbers when it does not.
// Effective cost is quota-aware (EffectiveCostFor), so a paid provider
// with free quota remaining passes a zero ceiling: the call really is
// free this month, and refusing it would contradict the receipt.
func (c Ceiling) Allows(p Costed, now time.Time) (bool, string) {
	if !c.Set {
		return true, ""
	}
	cost := EffectiveCostFor(p, now)
	if cost <= c.MaxUSD {
		return true, ""
	}
	return false, fmt.Sprintf("%s costs %s per call, above max_cost_usd %s", p.Name(), USD(cost), USD(c.MaxUSD))
}

// ApplyCeiling filters an already-ordered chain down to the providers
// whose effective cost fits under the ceiling, preserving order. It
// appends a "; ceiling: skipped X (...)" note to the routing reason so
// the decision stays visible in the trace, mirroring the guard's note.
// An unset ceiling returns the chain and reason untouched. An empty
// result means every provider is over the ceiling; callers turn that
// into CeilingEmptyError so the failure tells the agent what the
// cheapest option actually costs.
func ApplyCeiling[T Costed](ordered []T, c Ceiling, reason string, now time.Time) ([]T, string) {
	if !c.Set {
		return ordered, reason
	}
	kept := make([]T, 0, len(ordered))
	skips := make([]string, 0)
	for _, it := range ordered {
		ok, why := c.Allows(it, now)
		if ok {
			kept = append(kept, it)
			continue
		}
		skips = append(skips, fmt.Sprintf("skipped %s (%s)", it.Name(), why))
	}
	if len(skips) > 0 {
		reason += "; ceiling: " + strings.Join(skips, "; ")
	}
	return kept, reason
}

// CeilingEmptyError is the error for a chain the ceiling fenced off
// entirely. It names the capability, the ceiling, and the cheapest
// configured provider with its price, so an agent can decide whether to
// raise the ceiling or drop the question. configured is the pre-ceiling
// chain (already policy-filtered), used only to find the cheapest.
func CeilingEmptyError[T Costed](capability string, c Ceiling, configured []T, now time.Time) error {
	if len(configured) == 0 {
		return fmt.Errorf("no %s provider fits under max_cost_usd %s", capability, USD(c.MaxUSD))
	}
	cheapest := configured[0]
	cheapestCost := EffectiveCostFor(cheapest, now)
	for _, it := range configured[1:] {
		if cost := EffectiveCostFor(it, now); cost < cheapestCost {
			cheapest, cheapestCost = it, cost
		}
	}
	return fmt.Errorf("no %s provider fits under max_cost_usd %s; the cheapest available is %s at %s per call",
		capability, USD(c.MaxUSD), cheapest.Name(), USD(cheapestCost))
}

// USD renders a per-call price for traces and error messages. Four
// decimals cover every list price in the routing table ($0.0010,
// $0.0050) without turning a free rung into "$0".
func USD(v float64) string {
	return fmt.Sprintf("$%.4f", v)
}
