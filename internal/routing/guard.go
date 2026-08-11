package routing

import (
	"fmt"
	"sync"
	"time"
)

// Guard enforces two operator-declared spend rails at the routing layer:
// a per-provider daily USD budget, and a rate-limit cooldown circuit
// breaker. Both are keyed by "capability/provider" (e.g. "search/youcom")
// so the same provider name registered under two capability tables gets
// independent counters, matching how the config splits its provider maps.
//
// A nil *Guard is a safe no-op on every method: Allow returns true,
// RecordCost / RecordRateLimit do nothing. That lets call sites drop the
// guard in unconditionally, and keeps behavior identical to the
// pre-guard product when no budgets are configured.
type Guard struct {
	// caps is the configured daily USD budget per key. A key absent here
	// (or with a non-positive value) has no budget and is always allowed
	// on the spend rail. Immutable after NewGuard, so no lock needed.
	caps     map[string]float64
	cooldown time.Duration

	// now is the clock, overridable in tests. Defaults to time.Now.
	now func() time.Time

	mu    sync.Mutex
	spend map[string]daySpend  // key → today's accumulated spend
	cool  map[string]time.Time // key → cooldown expiry (zero if not cooling)
}

// daySpend is one key's spend accumulator, tagged with the UTC day it
// belongs to so RecordCost can reset it on a day rollover without a
// background sweeper.
type daySpend struct {
	day   string // UTC date, "2006-01-02"
	total float64
}

// defaultCooldown is the rate-limit cooldown window used when NewGuard is
// given a non-positive duration. Long enough to let a provider's per-
// minute rate limit drain, short enough that a transient 429 doesn't
// fence the provider off for the rest of the session.
const defaultCooldown = 60 * time.Second

// NewGuard builds a Guard. caps maps "capability/provider" keys to a daily
// USD budget; only positive caps constrain a provider, and the map may be
// nil or empty (cooldown then still applies to every provider). A cooldown
// of zero or less defaults to defaultCooldown.
func NewGuard(caps map[string]float64, cooldown time.Duration) *Guard {
	if cooldown <= 0 {
		cooldown = defaultCooldown
	}
	// Copy caps so a later mutation of the caller's map can't change the
	// budgets out from under a running Guard.
	c := make(map[string]float64, len(caps))
	for k, v := range caps {
		c[k] = v
	}
	return &Guard{
		caps:     c,
		cooldown: cooldown,
		now:      time.Now,
		spend:    make(map[string]daySpend),
		cool:     make(map[string]time.Time),
	}
}

// key joins a capability and provider into the guard's map key. Kept in
// one place so Allow / RecordCost / RecordRateLimit can't drift.
func key(capability, provider string) string {
	return capability + "/" + provider
}

// Allow reports whether a provider may be called right now, and a human-
// readable reason when it may not. It returns false when the provider is
// inside its rate-limit cooldown, or when its accumulated spend for the
// current UTC day has reached its configured cap. A provider with no
// configured cap is always allowed on the spend rail; the cooldown rail
// applies to every provider. A nil Guard always allows.
func (g *Guard) Allow(capability, provider string) (bool, string) {
	if g == nil {
		return true, ""
	}
	k := key(capability, provider)
	now := g.now()

	g.mu.Lock()
	defer g.mu.Unlock()

	if until, ok := g.cool[k]; ok {
		if now.Before(until) {
			return false, fmt.Sprintf("cooling down after a rate limit until %s (%s left)",
				until.UTC().Format(time.RFC3339), until.Sub(now).Round(time.Second))
		}
		// Expired: drop it so the map doesn't grow without bound.
		delete(g.cool, k)
	}

	cap, capped := g.caps[k]
	if capped && cap > 0 {
		s := g.spend[k]
		if s.day == utcDay(now) && s.total >= cap {
			return false, fmt.Sprintf("daily budget reached: $%.4f of $%.4f spent (resets at UTC midnight)",
				s.total, cap)
		}
	}
	return true, ""
}

// RecordCost adds costUSD to a provider's spend for the current UTC day,
// resetting the accumulator first if the day has rolled over since the
// last record. Always recorded, even for uncapped providers, so a cap
// added mid-day (a config reload in a future revision) sees real spend.
// A nil Guard does nothing.
func (g *Guard) RecordCost(capability, provider string, costUSD float64) {
	if g == nil || costUSD == 0 {
		return
	}
	k := key(capability, provider)
	today := utcDay(g.now())

	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.spend[k]
	if s.day != today {
		s = daySpend{day: today}
	}
	s.total += costUSD
	g.spend[k] = s
}

// RecordRateLimit opens the cooldown window for a provider: subsequent
// Allow calls return false until cooldown elapses. Called when a provider
// returns a 429 (see the tool hook closures). A nil Guard does nothing.
func (g *Guard) RecordRateLimit(capability, provider string) {
	if g == nil {
		return
	}
	k := key(capability, provider)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cool[k] = g.now().Add(g.cooldown)
}

// utcDay renders t's UTC calendar date, the bucket key spend accumulates
// under. UTC (not local) so a machine's timezone can't shift when the
// budget resets.
func utcDay(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}
