package routing

import (
	"sync"
	"testing"
	"time"
)

// newTestGuard builds a Guard whose clock is driven by *nowp, so tests
// can advance time deterministically across day rollovers and cooldown
// windows without sleeping.
func newTestGuard(caps map[string]float64, cooldown time.Duration, nowp *time.Time) *Guard {
	g := NewGuard(caps, cooldown)
	g.now = func() time.Time { return *nowp }
	return g
}

func TestGuard_BudgetAccumulationAndExhaustion(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	g := newTestGuard(map[string]float64{"search/youcom": 0.01}, time.Minute, &now)

	if ok, why := g.Allow("search", "youcom"); !ok {
		t.Fatalf("fresh provider should be allowed, got %q", why)
	}
	// Spend under the cap: 0.006 < 0.01, still allowed.
	g.RecordCost("search", "youcom", 0.006)
	if ok, _ := g.Allow("search", "youcom"); !ok {
		t.Fatalf("under-cap provider should still be allowed")
	}
	// A second charge crosses the cap (0.012 >= 0.01).
	g.RecordCost("search", "youcom", 0.006)
	ok, why := g.Allow("search", "youcom")
	if ok {
		t.Fatalf("provider at cap should be blocked")
	}
	if why == "" {
		t.Fatalf("blocked Allow must give a reason")
	}
}

func TestGuard_UncappedProviderAlwaysAllowedUntilRateLimited(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	// No cap for this provider; only a cooldown.
	g := newTestGuard(nil, time.Minute, &now)

	// Huge spend never blocks an uncapped provider.
	g.RecordCost("search", "marginalia", 999.0)
	if ok, _ := g.Allow("search", "marginalia"); !ok {
		t.Fatalf("uncapped provider must stay allowed regardless of spend")
	}
	// But a rate limit still fences it off.
	g.RecordRateLimit("search", "marginalia")
	if ok, _ := g.Allow("search", "marginalia"); ok {
		t.Fatalf("cooldown must apply to uncapped providers too")
	}
}

func TestGuard_UTCDayRolloverResets(t *testing.T) {
	now := time.Date(2026, 8, 10, 23, 59, 0, 0, time.UTC)
	g := newTestGuard(map[string]float64{"extract/firecrawl": 0.01}, time.Minute, &now)

	g.RecordCost("extract", "firecrawl", 0.02) // over cap for the 10th
	if ok, _ := g.Allow("extract", "firecrawl"); ok {
		t.Fatalf("provider should be blocked on the day it exhausted its budget")
	}
	// Roll into the next UTC day: the accumulator resets on the next
	// record, and Allow no longer sees the previous day's spend.
	now = now.Add(2 * time.Minute) // now 2026-08-11 00:01 UTC
	if ok, _ := g.Allow("extract", "firecrawl"); !ok {
		t.Fatalf("budget must reset at UTC midnight")
	}
	// Spend on the new day accumulates from zero.
	g.RecordCost("extract", "firecrawl", 0.005)
	if ok, _ := g.Allow("extract", "firecrawl"); !ok {
		t.Fatalf("half-cap spend on the new day should be allowed")
	}
	g.RecordCost("extract", "firecrawl", 0.005)
	if ok, _ := g.Allow("extract", "firecrawl"); ok {
		t.Fatalf("new-day spend reaching the cap should block again")
	}
}

func TestGuard_CooldownStartAndExpiry(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	g := newTestGuard(nil, 90*time.Second, &now)

	g.RecordRateLimit("browse", "browserless")
	if ok, why := g.Allow("browse", "browserless"); ok || why == "" {
		t.Fatalf("provider should be cooling down right after a rate limit")
	}
	// Just before expiry: still cooling.
	now = now.Add(89 * time.Second)
	if ok, _ := g.Allow("browse", "browserless"); ok {
		t.Fatalf("provider should still be cooling at 89s of a 90s window")
	}
	// Past expiry: allowed again.
	now = now.Add(2 * time.Second)
	if ok, _ := g.Allow("browse", "browserless"); !ok {
		t.Fatalf("cooldown should have expired after 90s")
	}
}

func TestGuard_DefaultCooldownWhenNonPositive(t *testing.T) {
	if g := NewGuard(nil, 0); g.cooldown != defaultCooldown {
		t.Errorf("cooldown 0 should default to %v, got %v", defaultCooldown, g.cooldown)
	}
	if g := NewGuard(nil, -5*time.Second); g.cooldown != defaultCooldown {
		t.Errorf("negative cooldown should default to %v, got %v", defaultCooldown, g.cooldown)
	}
}

func TestGuard_IndependentKeysPerCapability(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	// Same provider name under two capabilities, each with its own cap.
	g := newTestGuard(map[string]float64{
		"search/shared":  0.01,
		"extract/shared": 0.01,
	}, time.Minute, &now)

	g.RecordCost("search", "shared", 0.02) // exhaust search only
	if ok, _ := g.Allow("search", "shared"); ok {
		t.Fatalf("search/shared should be blocked")
	}
	if ok, _ := g.Allow("extract", "shared"); !ok {
		t.Fatalf("extract/shared must have an independent budget")
	}
}

func TestGuard_NilIsNoOp(t *testing.T) {
	var g *Guard // nil
	// None of these should panic, and Allow must permit.
	g.RecordCost("search", "youcom", 5)
	g.RecordRateLimit("search", "youcom")
	if ok, why := g.Allow("search", "youcom"); !ok || why != "" {
		t.Fatalf("nil Guard.Allow = (%v, %q), want (true, \"\")", ok, why)
	}
}

func TestGuard_ConcurrentRecordCost(t *testing.T) {
	// Concurrency smoke test: run under -race to catch data races. Real
	// wall clock here (not the *nowp hook) so all writes land on the same
	// UTC day and the total is deterministic.
	g := NewGuard(map[string]float64{"search/youcom": 1e9}, time.Minute)

	const workers, perWorker = 16, 500
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				g.RecordCost("search", "youcom", 0.001)
				g.Allow("search", "youcom")
			}
		}()
	}
	wg.Wait()

	g.mu.Lock()
	got := g.spend["search/youcom"].total
	g.mu.Unlock()
	want := float64(workers*perWorker) * 0.001
	// Float accumulation is not exact; allow a small epsilon.
	if diff := got - want; diff > 1e-6 || diff < -1e-6 {
		t.Errorf("concurrent spend total = %v, want ~%v", got, want)
	}
}
