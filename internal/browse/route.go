package browse

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
)

// AttemptHook re-exports the capability-neutral routing hook.
type AttemptHook = routing.AttemptHook

// OrderByCost returns browsers sorted by effective cost ascending.
// Stable on ties.
func OrderByCost(browsers []Browser) []Browser {
	out := make([]Browser, len(browsers))
	copy(out, browsers)
	now := time.Now()
	sort.SliceStable(out, func(i, j int) bool {
		return EffectiveCostOf(out[i], now) < EffectiveCostOf(out[j], now)
	})
	return out
}

// CallWithFallback walks browsers in cost order, returning the first
// success. Every per-provider failure — transient or permanent — logs
// and falls through to the next provider; the chain stops early when
// ctx is done or a driver reports a fatal (request-scoped) error.
// Hook (may be nil) fires once per attempt.
func CallWithFallback(ctx context.Context, browsers []Browser, q Query, logger *slog.Logger, hook AttemptHook) (Browser, Result, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if len(browsers) == 0 {
		return nil, Result{}, errors.New("frugal: no browse providers configured")
	}
	ordered := OrderByCost(browsers)
	var errs []error
	for i, b := range ordered {
		start := time.Now()
		res, err := b.Render(ctx, q)
		latency := time.Since(start)
		if hook != nil {
			hook(b.Name(), latency, res.CostUSD, err)
		}
		if err == nil {
			logger.Debug("browse ok",
				"provider", b.Name(),
				"cost_usd", res.CostUSD,
				"latency_ms", latency.Milliseconds(),
				"attempt", i+1,
				"chars", len(res.HTML)+len(res.Text))
			return b, res, nil
		}
		errs = append(errs, err)
		if routing.IsFatal(err) {
			logger.Warn("browse fatal error; request can't succeed anywhere — aborting chain",
				"provider", b.Name(),
				"latency_ms", latency.Milliseconds(),
				"err", err)
			return b, Result{}, err
		}
		if ctx.Err() != nil {
			logger.Warn("browse aborted; context done",
				"provider", b.Name(),
				"latency_ms", latency.Milliseconds(),
				"err", err)
			return b, Result{}, routing.JoinAttempts(errs)
		}
		logger.Warn("browse error; falling back",
			"provider", b.Name(),
			"permanent", routing.IsPermanent(err),
			"attempt", i+1,
			"remaining", len(ordered)-i-1,
			"latency_ms", latency.Milliseconds(),
			"err", err)
	}
	return ordered[len(ordered)-1], Result{}, routing.JoinAttempts(errs)
}

// CallPinned dispatches one render against the named provider only.
func CallPinned(ctx context.Context, browsers []Browser, name string, q Query, logger *slog.Logger, hook AttemptHook) (Browser, Result, error) {
	b := Find(browsers, name)
	if b == nil {
		return nil, Result{}, &ErrProviderNotConfigured{Name: name, Known: namesOf(browsers)}
	}
	if logger == nil {
		logger = slog.Default()
	}
	start := time.Now()
	res, err := b.Render(ctx, q)
	latency := time.Since(start)
	if hook != nil {
		hook(b.Name(), latency, res.CostUSD, err)
	}
	if err != nil {
		logger.Warn("browse pinned error",
			"provider", b.Name(),
			"latency_ms", latency.Milliseconds(),
			"permanent", routing.IsPermanent(err),
			"err", err)
	}
	return b, res, err
}

// ErrProviderNotConfigured is returned by CallPinned when the requested
// browser isn't in the configured set.
type ErrProviderNotConfigured struct {
	Name  string
	Known []string
}

func (e *ErrProviderNotConfigured) Error() string {
	if len(e.Known) == 0 {
		return "browser " + e.Name + " not configured (no browsers configured)"
	}
	return "browser " + e.Name + " not configured (known: " + joinNames(e.Known) + ")"
}

func namesOf(browsers []Browser) []string {
	out := make([]string, 0, len(browsers))
	for _, b := range browsers {
		out = append(out, b.Name())
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

// StripHTML returns a best-effort plain-text view of raw HTML. Used by
// drivers that get HTML back from the upstream API but the caller asked
// for `format: "text"`. Implementation is intentionally minimal — drop
// script/style blocks, drop tags, collapse whitespace, treat every tag
// boundary as a soft space so adjacent text from different elements
// doesn't smash together. Not a substitute for a real Readability pass.
func StripHTML(html string) string {
	out := make([]byte, 0, len(html))
	inTag := false
	inScript := false
	inStyle := false
	// pendingSpace = true means "the next emitted non-space char should
	// be preceded by a single space." Set when a tag closes or when we
	// see whitespace in content; consumed when we emit content.
	pendingSpace := false

	lower := func(b byte) byte {
		if b >= 'A' && b <= 'Z' {
			return b + 32
		}
		return b
	}
	emit := func(c byte) {
		if pendingSpace && len(out) > 0 {
			out = append(out, ' ')
		}
		pendingSpace = false
		out = append(out, c)
	}

	i := 0
	for i < len(html) {
		if !inScript && !inStyle && i+6 < len(html) && html[i] == '<' {
			// Sniff <script ... > and <style ... > openings (case-insensitive).
			if lower(html[i+1]) == 's' && lower(html[i+2]) == 'c' && lower(html[i+3]) == 'r' && lower(html[i+4]) == 'i' && lower(html[i+5]) == 'p' && lower(html[i+6]) == 't' {
				inScript = true
				inTag = true
				pendingSpace = true
				i++
				continue
			}
			if lower(html[i+1]) == 's' && lower(html[i+2]) == 't' && lower(html[i+3]) == 'y' && lower(html[i+4]) == 'l' && lower(html[i+5]) == 'e' {
				inStyle = true
				inTag = true
				pendingSpace = true
				i++
				continue
			}
		}
		if inScript && i+7 < len(html) && html[i] == '<' && html[i+1] == '/' &&
			lower(html[i+2]) == 's' && lower(html[i+3]) == 'c' && lower(html[i+4]) == 'r' && lower(html[i+5]) == 'i' && lower(html[i+6]) == 'p' && lower(html[i+7]) == 't' {
			inScript = false
			inTag = true
			i++
			continue
		}
		if inStyle && i+6 < len(html) && html[i] == '<' && html[i+1] == '/' &&
			lower(html[i+2]) == 's' && lower(html[i+3]) == 't' && lower(html[i+4]) == 'y' && lower(html[i+5]) == 'l' && lower(html[i+6]) == 'e' {
			inStyle = false
			inTag = true
			i++
			continue
		}
		c := html[i]
		if inScript || inStyle {
			i++
			continue
		}
		if c == '<' {
			inTag = true
			pendingSpace = true
			i++
			continue
		}
		if c == '>' {
			inTag = false
			i++
			continue
		}
		if inTag {
			i++
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			pendingSpace = true
			i++
			continue
		}
		emit(c)
		i++
	}
	// Trim trailing space.
	for len(out) > 0 && out[len(out)-1] == ' ' {
		out = out[:len(out)-1]
	}
	return string(out)
}
