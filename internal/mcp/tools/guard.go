package tools

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
)

// guardChain filters an already-ordered provider chain down to the
// providers the guard currently allows for this capability, dropping the
// over-budget / cooling-down ones. It logs each skip at Warn with the
// reason and appends a "; budget: skipped X (reason)" note to the routing
// reason string so the decision stays visible in the trace the tools
// already log. A nil guard leaves the chain and reason untouched.
//
// Returns the surviving chain and the augmented reason. An empty result
// means every provider is fenced off; callers turn that into an error via
// guardEmptyError so the failure names why.
func guardChain[T routing.Costed](g *routing.Guard, capability string, ordered []T, reason string, logger *slog.Logger) ([]T, string) {
	if g == nil {
		return ordered, reason
	}
	kept := make([]T, 0, len(ordered))
	skips := make([]string, 0)
	for _, it := range ordered {
		ok, why := g.Allow(capability, it.Name())
		if ok {
			kept = append(kept, it)
			continue
		}
		logger.Warn("routing guard: provider skipped",
			"capability", capability, "provider", it.Name(), "reason", why)
		skips = append(skips, fmt.Sprintf("skipped %s (%s)", it.Name(), why))
	}
	if len(skips) > 0 {
		reason += "; budget: " + strings.Join(skips, "; ")
	}
	return kept, reason
}

// guardEmptyError is the error returned when guardChain fenced off every
// provider. It names the capability and the ways a provider comes back
// (cooldown or breaker expiry, the next UTC midnight budget reset), so
// an agent knows whether to back off or give up.
func guardEmptyError(capability string) error {
	return fmt.Errorf("every configured %s provider is over its daily budget, cooling down after a rate limit, or fenced off after consecutive failures; retry after a budget resets (UTC midnight) or the cooldown elapses", capability)
}

// guardRecord is the guard side of a tool's AttemptHook: it always books
// the attempt's cost against the provider's daily budget, opens the
// cooldown window when the attempt failed with a 429, and feeds the
// outcome to the failure breaker (which opens on a run of timeouts or
// 5xx and logs the transition once). Composed alongside the metrics
// hook so both observers see every attempt. A nil guard makes this a
// no-op (the guard methods are nil-safe).
func guardRecord(g *routing.Guard, capability, provider string, costUSD float64, err error, logger *slog.Logger) {
	g.RecordCost(capability, provider, costUSD)
	var re *routing.Error
	if errors.As(err, &re) && re.Status == 429 {
		g.RecordRateLimitFor(capability, provider, re.RetryAfter)
	}
	if g.RecordOutcome(capability, provider, err) {
		threshold, cooldown := g.FailureBreakerSettings()
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("routing breaker: provider fenced off after consecutive failures",
			"capability", capability, "provider", provider,
			"threshold", threshold, "cooldown", cooldown, "err", err)
	}
}

// composeHook wraps an existing AttemptHook so guardRecord runs on every
// attempt too. The capability string is fixed per tool (search / extract /
// browse); frugal__execute passes the per-attempt capability instead.
func composeHook(g *routing.Guard, capability string, inner routing.AttemptHook) routing.AttemptHook {
	return func(provider string, latency time.Duration, costUSD float64, won bool, err error) {
		if inner != nil {
			inner(provider, latency, costUSD, won, err)
		}
		guardRecord(g, capability, provider, costUSD, err, nil)
	}
}
