package search

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
)

// AttemptHook is the per-attempt observability hook used by
// CallWithFallback / CallPinned. Re-exported from internal/routing so
// the existing search.AttemptHook call sites keep working — the
// underlying type is identical.
type AttemptHook = routing.AttemptHook

// OrderByCost returns searchers sorted by effective cost ascending — the
// quota-aware price reported by Quoted.EffectiveCostPerCall when the
// driver implements it, falling back to the static CostPerCall otherwise.
// Stable — ties preserve input order, so callers can break ties by listing
// a preferred provider first. Returns a fresh slice; the input isn't
// mutated.
//
// All comparisons use a single `time.Now()` snapshot so a month-boundary
// rollover that lands mid-sort can't produce an inconsistent ordering.
func OrderByCost(searchers []Searcher) []Searcher {
	out := make([]Searcher, len(searchers))
	copy(out, searchers)
	now := time.Now()
	sort.SliceStable(out, func(i, j int) bool {
		return EffectiveCostOf(out[i], now) < EffectiveCostOf(out[j], now)
	})
	return out
}

// CallWithFallback walks searchers in cost order, calling Search on each
// until one succeeds. Every per-provider failure — transient (network
// blip, 5xx) or permanent (auth, quota, provider-side 4xx) — logs and
// falls through to the next provider: a 401 or 404 from one provider says
// nothing about the next one's credentials or endpoints, and the product
// promise is "the cheapest provider that returns a result". Permanent
// still matters inside the driver (no retry there); out here it only
// changes the log line. The chain stops early in two cases: ctx is done
// (the caller went away), or a driver reports a fatal error — the
// request itself can't succeed anywhere, so the rest of the chain would
// only rediscover that for money.
//
// A zero-hit success from a FREE provider falls through: an empty result
// set isn't "a result" in the sense the promise means, and free-tier
// index coverage differs wildly between providers (Marginalia's
// indie-web index whiffs on mainstream queries Serper nails). A zero-hit
// success from a PAID provider (CostPerCall() > 0) instead stops the
// chain and is returned as-is — a major-index paid provider returning
// nothing is strong evidence the query truly has no hits, and spending
// 5x more to confirm emptiness again would contradict the product
// promise. If the whole chain comes up empty, the first zero-hit success
// is returned as-is — empty-but-succeeded beats surfacing a later
// provider's error for a query that simply has no hits.
//
// Returns the searcher that produced the result, its Results, and nil
// error on success. When every provider fails, the returned error
// classifies as the last attempt but its message names every failed
// provider (routing.JoinAttempts). On no searchers configured returns a
// non-nil error with no provider.
//
// logger is used for per-attempt logging at debug level (success) and
// warn level (fallback). Pass slog.Default() if no logger context is
// available. hook may be nil; when set, it's called after each attempt
// regardless of outcome.
func CallWithFallback(ctx context.Context, searchers []Searcher, q Query, logger *slog.Logger, hook AttemptHook) (Searcher, Results, error) {
	return CallInOrder(ctx, OrderByCost(searchers), q, logger, hook)
}

// CallInOrder is CallWithFallback minus the sort: it walks searchers
// exactly as given. Callers that ordered the chain themselves — via
// routing.Apply under an operator policy — use this; CallWithFallback
// delegates here after the default cost sort. All fallback semantics
// (paid-zero-hit stop, fatal short-circuit, context cancel, zero-hit
// fall-through) are identical.
func CallInOrder(ctx context.Context, ordered []Searcher, q Query, logger *slog.Logger, hook AttemptHook) (Searcher, Results, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if len(ordered) == 0 {
		return nil, Results{}, errors.New("frugal: no search providers configured")
	}
	var errs []error
	var emptyOK Searcher // first provider that succeeded with zero hits
	var emptyRes Results
	for i, s := range ordered {
		start := time.Now()
		res, err := s.Search(ctx, q)
		latency := time.Since(start)
		if hook != nil {
			// won: a non-empty success is the result the caller gets. A
			// zero-hit success that ends up returned — whether a paid
			// provider stopped the chain or the whole chain came up
			// empty — stays won=false: a query with no hits earned no
			// savings.
			hook(s.Name(), latency, res.CostUSD, err == nil && len(res.Items) > 0, err)
		}
		if err == nil {
			if len(res.Items) == 0 {
				if s.CostPerCall() > 0 {
					// A paid provider's empty answer is evidence, not a
					// coverage gap: stop here rather than pay a pricier
					// provider to rediscover emptiness. The hook already
					// saw this attempt with won=false — no hits, no
					// savings credit.
					logger.Debug("search zero hits from paid provider; accepting empty result",
						"provider", s.Name(),
						"cost_usd", res.CostUSD,
						"latency_ms", latency.Milliseconds(),
						"attempt", i+1)
					return s, res, nil
				}
				if emptyOK == nil {
					emptyOK, emptyRes = s, res
				}
				logger.Warn("search zero hits; falling back",
					"provider", s.Name(),
					"latency_ms", latency.Milliseconds(),
					"attempt", i+1,
					"remaining", len(ordered)-i-1)
				continue
			}
			logger.Debug("search ok",
				"provider", s.Name(),
				"cost_usd", res.CostUSD,
				"latency_ms", latency.Milliseconds(),
				"attempt", i+1,
				"results", len(res.Items))
			return s, res, nil
		}
		errs = append(errs, err)
		if routing.IsFatal(err) {
			logger.Warn("search fatal error; request can't succeed anywhere — aborting chain",
				"provider", s.Name(),
				"latency_ms", latency.Milliseconds(),
				"err", err)
			if emptyOK != nil {
				return emptyOK, emptyRes, nil
			}
			return s, Results{}, err
		}
		if ctx.Err() != nil {
			logger.Warn("search aborted; context done",
				"provider", s.Name(),
				"latency_ms", latency.Milliseconds(),
				"err", err)
			if emptyOK != nil {
				return emptyOK, emptyRes, nil
			}
			return s, Results{}, routing.JoinAttempts(errs)
		}
		logger.Warn("search error; falling back",
			"provider", s.Name(),
			"permanent", routing.IsPermanent(err),
			"attempt", i+1,
			"remaining", len(ordered)-i-1,
			"latency_ms", latency.Milliseconds(),
			"err", err)
	}
	if emptyOK != nil {
		logger.Debug("search exhausted chain; returning zero-hit success",
			"provider", emptyOK.Name())
		return emptyOK, emptyRes, nil
	}
	return ordered[len(ordered)-1], Results{}, routing.JoinAttempts(errs)
}

// CallPinned dispatches one call against the named searcher only — no
// fallback. Used when the tool-call argument pins a specific provider.
// Returns ErrProviderNotConfigured if name doesn't match any known
// searcher. hook may be nil.
func CallPinned(ctx context.Context, searchers []Searcher, name string, q Query, logger *slog.Logger, hook AttemptHook) (Searcher, Results, error) {
	s := Find(searchers, name)
	if s == nil {
		return nil, Results{}, &ErrProviderNotConfigured{Name: name, Known: namesOf(searchers)}
	}
	if logger == nil {
		logger = slog.Default()
	}
	start := time.Now()
	res, err := s.Search(ctx, q)
	latency := time.Since(start)
	if hook != nil {
		// Pinned calls have no fallback: whatever came back is the result.
		// won still requires hits — a zero-hit success earns no savings
		// credit, same as the fallback path.
		hook(s.Name(), latency, res.CostUSD, err == nil && len(res.Items) > 0, err)
	}
	if err != nil {
		logger.Warn("search pinned error",
			"provider", s.Name(),
			"latency_ms", latency.Milliseconds(),
			"permanent", routing.IsPermanent(err),
			"err", err)
	}
	return s, res, err
}

// ErrProviderNotConfigured is returned by CallPinned when the requested
// provider isn't in the configured set. Holds the list of valid names
// so callers can produce a helpful error message.
type ErrProviderNotConfigured struct {
	Name  string
	Known []string
}

func (e *ErrProviderNotConfigured) Error() string {
	if len(e.Known) == 0 {
		return "provider " + e.Name + " not configured (no providers configured)"
	}
	return "provider " + e.Name + " not configured (known: " + joinNames(e.Known) + ")"
}

func namesOf(searchers []Searcher) []string {
	out := make([]string, 0, len(searchers))
	for _, s := range searchers {
		out = append(out, s.Name())
	}
	return out
}

func joinNames(names []string) string {
	if len(names) == 0 {
		return ""
	}
	out := names[0]
	for _, n := range names[1:] {
		out += ", " + n
	}
	return out
}
