package routing

import (
	"errors"
	"fmt"
	"time"
)

// Failure breaker: the fourth guard rail, alongside daily budgets, the
// 429 cooldown, and the loop guard. It fences off a provider that is
// down (timing out, returning 5xx, refusing connections) so the chain
// stops paying the wait on every call.
//
// Why the 429 cooldown is not enough: a rate limit is the one failure a
// provider announces. An outage is silent. Each driver has its own
// request timeout (15 to 90s), so a provider that has stopped answering
// sits at the front of the chain and burns that timeout on every single
// call before failover even starts, for as long as the outage lasts.
// Under the default 60s call deadline that is a quarter to all of the
// budget spent on a rung that cannot win, and the agent sees every tool
// call take fifteen-plus seconds for no reason it can act on.
//
// The rail counts consecutive transient failures per capability and
// provider. At the threshold the breaker opens and Allow refuses the
// provider for the failure cooldown, so the chain skips straight to the
// next rung at zero cost. When the window elapses the breaker is
// half-open: the provider is admitted again, a success closes the
// breaker and resets the count, and one more failure reopens it for a
// full window without waiting for the threshold again. A single blip
// never trips it: the count resets on any success, including a zero-hit
// one, because an answer of "nothing" still proves the provider is up.
//
// What counts as a failure is deliberately narrow: transient errors that
// are not a 429 (that one has its own rail). Permanent errors (401, 402,
// 403, 4xx) do not count: they fail in milliseconds, cost no deadline,
// and name a key or plan the operator has to fix, which fencing would
// only hide. Fatal errors (the target itself is gone) are about the
// request, not the provider, and are ignored too. A cancelled caller is
// classified permanent by ClassifyNetwork and so never counts either.

// DefaultFailureThreshold is the number of consecutive transient
// failures that opens a provider's breaker when the config does not say
// otherwise. Three in a row is past what a flaky network produces on a
// healthy provider (each driver already retried a free provider twice
// inside one attempt) and short enough that an outage costs the agent
// three slow calls, not thirty.
const DefaultFailureThreshold = 3

// breakerEntry is one capability/provider pair's failure state.
type breakerEntry struct {
	// consecutive is the run of transient failures since the last
	// success. It is not cleared when the breaker opens, so a failure in
	// the half-open state reopens the breaker immediately.
	consecutive int
	// openUntil is when the breaker admits the provider again. Zero
	// while the breaker is closed.
	openUntil time.Time
	// last is the error that opened the breaker, kept for the refusal
	// reason so the agent and the log name the actual symptom.
	last string
}

// SetFailureBreaker turns the failure rail on. threshold is the number
// of consecutive transient failures that opens the breaker; zero or
// less turns the rail off. cooldown is how long the breaker stays open;
// zero or less falls back to the guard's rate-limit cooldown so one
// `routing.cooldown` setting governs both windows unless the operator
// separates them. Safe on a nil Guard (no-op) and meant to be called
// before serving starts, like SetLoopGuard.
func (g *Guard) SetFailureBreaker(threshold int, cooldown time.Duration) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if threshold <= 0 {
		g.failThreshold = 0
		g.failCooldown = 0
		g.breakers = nil
		return
	}
	if cooldown <= 0 {
		cooldown = g.cooldown
	}
	g.failThreshold = threshold
	g.failCooldown = cooldown
	if g.breakers == nil {
		g.breakers = make(map[string]*breakerEntry)
	}
}

// FailureBreakerEnabled reports whether the failure rail is on.
func (g *Guard) FailureBreakerEnabled() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.failThreshold > 0
}

// FailureBreakerSettings returns the active threshold and cooldown
// (zeros when the rail is off), for startup logging.
func (g *Guard) FailureBreakerSettings() (int, time.Duration) {
	if g == nil {
		return 0, 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.failThreshold, g.failCooldown
}

// RecordOutcome feeds one provider attempt's result to the failure rail.
// A nil err (any success, including zero hits) closes the breaker and
// clears the failure run. A transient non-429 error extends the run and
// opens the breaker at the threshold, or reopens it at once when the
// breaker was half-open. Other errors leave the state untouched. The
// return value reports whether this call opened (or reopened) the
// breaker, so the caller can log the transition once rather than on
// every refused Allow. A nil Guard, or one with the rail off, does
// nothing and returns false.
func (g *Guard) RecordOutcome(capability, provider string, err error) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failThreshold <= 0 {
		return false
	}
	k := key(capability, provider)
	if err == nil {
		delete(g.breakers, k)
		return false
	}
	if !countsAsFailure(err) {
		return false
	}
	e := g.breakers[k]
	if e == nil {
		e = &breakerEntry{}
		g.breakers[k] = e
	}
	e.consecutive++
	if e.consecutive < g.failThreshold {
		return false
	}
	now := g.now()
	wasOpen := now.Before(e.openUntil)
	e.openUntil = now.Add(g.failCooldown)
	e.last = shortError(err)
	// Opening from closed or from half-open is a transition worth a log
	// line; extending an already-open breaker (a pinned call that was
	// refused never reaches here, but a concurrent in-flight attempt can)
	// is not.
	return !wasOpen
}

// breakerAllowLocked is the failure rail's half of Allow. Caller holds
// g.mu. Returns false with a reason while the breaker is open; once the
// window has elapsed the provider is admitted (half-open) and the entry
// is kept so a further failure reopens the breaker without a fresh run.
func (g *Guard) breakerAllowLocked(k string, now time.Time) (bool, string) {
	if g.failThreshold <= 0 {
		return true, ""
	}
	e, ok := g.breakers[k]
	if !ok || !now.Before(e.openUntil) {
		return true, ""
	}
	return false, fmt.Sprintf("circuit open after %d consecutive failures (last: %s) until %s (%s left)",
		e.consecutive, e.last,
		e.openUntil.UTC().Format(time.RFC3339), e.openUntil.Sub(now).Round(time.Second))
}

// countsAsFailure decides whether an attempt error moves the failure
// rail. See the package comment at the top of this file for the policy.
func countsAsFailure(err error) bool {
	if err == nil || !IsTransient(err) || IsFatal(err) {
		return false
	}
	var re *Error
	if errors.As(err, &re) && re.Status == 429 {
		// The rate-limit rail owns 429s: it already fences the provider
		// for the cooldown or the Retry-After hint, whichever is longer.
		return false
	}
	return true
}

// shortError renders an attempt error for the refusal reason: the
// provider prefix is redundant there, and the detail is for the log.
func shortError(err error) string {
	var re *Error
	if errors.As(err, &re) {
		switch {
		case re.Status > 0:
			return fmt.Sprintf("HTTP %d", re.Status)
		case re.Err != nil:
			return trimErr(re.Err.Error())
		}
	}
	return trimErr(err.Error())
}

// trimErr keeps an error message to one line of reasonable length so a
// routing trace stays readable.
func trimErr(s string) string {
	const max = 80
	for i, r := range s {
		if r == '\n' || i >= max {
			return s[:i] + "..."
		}
	}
	return s
}
