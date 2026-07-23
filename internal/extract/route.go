package extract

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
)

// AttemptHook re-exports the capability-neutral routing hook so callers
// can pass an extract-flavored closure without dragging in the routing
// package.
type AttemptHook = routing.AttemptHook

// OrderByCost returns extractors sorted by effective cost ascending.
// Quoted providers contribute their EffectiveCostPerCall(now); others
// fall back to CostPerCall. Stable on ties — config order wins.
func OrderByCost(extractors []Extractor) []Extractor {
	out := make([]Extractor, len(extractors))
	copy(out, extractors)
	now := time.Now()
	sort.SliceStable(out, func(i, j int) bool {
		return EffectiveCostOf(out[i], now) < EffectiveCostOf(out[j], now)
	})
	return out
}

// CallWithFallback walks extractors in cost order, returning the first
// success. Every per-provider failure — transient or permanent — logs
// and falls through to the next provider. This is what makes the
// goreadability → Firecrawl handoff work: goreadability classifies an
// empty-content (JS-rendered) page as Permanent, and the next driver
// down the chain can render it. The chain stops early in two cases:
// ctx is done (the caller went away), or a driver reports a fatal
// error — a dead target URL (404/410) is dead for every provider, so
// falling through would spend a paid scrape rediscovering that.
//
// Hook (may be nil) fires once per attempt with provider name, latency,
// per-call cost, and the error (nil on success). Used by the metrics
// layer to record every attempt — not just the winner.
func CallWithFallback(ctx context.Context, extractors []Extractor, q Query, logger *slog.Logger, hook AttemptHook) (Extractor, Result, error) {
	return CallInOrder(ctx, OrderByCost(extractors), q, logger, hook)
}

// CallInOrder is CallWithFallback minus the sort: it walks extractors
// exactly as given. Callers that ordered the chain themselves — via
// routing.Apply under an operator policy — use this; CallWithFallback
// delegates here after the default cost sort.
func CallInOrder(ctx context.Context, ordered []Extractor, q Query, logger *slog.Logger, hook AttemptHook) (Extractor, Result, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if len(ordered) == 0 {
		return nil, Result{}, errors.New("frugal: no extract providers configured")
	}
	var errs []error
	for i, e := range ordered {
		start := time.Now()
		res, err := e.Extract(ctx, q)
		latency := time.Since(start)
		if hook != nil {
			// won: the first success is what the caller gets back.
			hook(e.Name(), latency, res.CostUSD, err == nil, err)
		}
		if err == nil {
			logger.Debug("extract ok",
				"provider", e.Name(),
				"cost_usd", res.CostUSD,
				"latency_ms", latency.Milliseconds(),
				"attempt", i+1,
				"chars", len(res.Markdown)+len(res.HTML)+len(res.Text))
			return e, res, nil
		}
		errs = append(errs, err)
		if routing.IsFatal(err) {
			logger.Warn("extract fatal error; request can't succeed anywhere — aborting chain",
				"provider", e.Name(),
				"latency_ms", latency.Milliseconds(),
				"err", err)
			return e, Result{}, err
		}
		if ctx.Err() != nil {
			logger.Warn("extract aborted; context done",
				"provider", e.Name(),
				"latency_ms", latency.Milliseconds(),
				"err", err)
			return e, Result{}, routing.JoinAttempts(errs)
		}
		logger.Warn("extract error; falling back",
			"provider", e.Name(),
			"permanent", routing.IsPermanent(err),
			"attempt", i+1,
			"remaining", len(ordered)-i-1,
			"latency_ms", latency.Milliseconds(),
			"err", err)
	}
	return ordered[len(ordered)-1], Result{}, routing.JoinAttempts(errs)
}

// CallPinned dispatches one extract against the named provider only —
// no fallback. Used when the caller pins a provider via the MCP tool
// argument.
func CallPinned(ctx context.Context, extractors []Extractor, name string, q Query, logger *slog.Logger, hook AttemptHook) (Extractor, Result, error) {
	e := Find(extractors, name)
	if e == nil {
		return nil, Result{}, &ErrProviderNotConfigured{Name: name, Known: namesOf(extractors)}
	}
	if logger == nil {
		logger = slog.Default()
	}
	start := time.Now()
	res, err := e.Extract(ctx, q)
	latency := time.Since(start)
	if hook != nil {
		// Pinned calls have no fallback: whatever came back is the result.
		hook(e.Name(), latency, res.CostUSD, err == nil, err)
	}
	if err != nil {
		logger.Warn("extract pinned error",
			"provider", e.Name(),
			"latency_ms", latency.Milliseconds(),
			"permanent", routing.IsPermanent(err),
			"err", err)
	}
	return e, res, err
}

// ErrProviderNotConfigured is returned by CallPinned when the requested
// extractor isn't in the configured set.
type ErrProviderNotConfigured struct {
	Name  string
	Known []string
}

func (e *ErrProviderNotConfigured) Error() string {
	if len(e.Known) == 0 {
		return "extractor " + e.Name + " not configured (no extractors configured)"
	}
	return "extractor " + e.Name + " not configured (known: " + joinNames(e.Known) + ")"
}

func namesOf(extractors []Extractor) []string {
	out := make([]string, 0, len(extractors))
	for _, e := range extractors {
		out = append(out, e.Name())
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
