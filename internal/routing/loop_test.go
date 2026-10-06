package routing

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func newLoopGuard(limit int, window time.Duration, nowp *time.Time) *Guard {
	g := newTestGuard(nil, time.Minute, nowp)
	g.SetLoopGuard(limit, window)
	return g
}

func TestLoop_OffByDefaultAndNilSafe(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	g := newTestGuard(nil, time.Minute, &now)
	if g.LoopGuardEnabled() {
		t.Fatal("loop rail must be off until SetLoopGuard is called")
	}
	for i := 0; i < 50; i++ {
		if ok, why := g.Admit("frugal__search", "fp"); !ok {
			t.Fatalf("rail off: call %d refused: %s", i, why)
		}
	}
	var nilGuard *Guard
	if ok, _ := nilGuard.Admit("frugal__search", "fp"); !ok {
		t.Fatal("nil guard must admit")
	}
	nilGuard.SetLoopGuard(3, time.Second) // must not panic
	if nilGuard.LoopGuardEnabled() {
		t.Fatal("nil guard reports disabled")
	}
}

func TestLoop_RefusesPastLimitWithinWindow(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	g := newLoopGuard(3, time.Minute, &now)

	for i := 1; i <= 3; i++ {
		if ok, why := g.Admit("frugal__search", "fp"); !ok {
			t.Fatalf("call %d within the limit refused: %s", i, why)
		}
		now = now.Add(2 * time.Second)
	}
	ok, why := g.Admit("frugal__search", "fp")
	if ok {
		t.Fatal("fourth identical call inside the window must be refused")
	}
	for _, want := range []string{"loop guard", "frugal__search", "4 times", "limit 3", "wait"} {
		if !strings.Contains(why, want) {
			t.Errorf("reason %q missing %q", why, want)
		}
	}
	// A different fingerprint on the same tool is unaffected.
	if ok, why := g.Admit("frugal__search", "other"); !ok {
		t.Fatalf("different arguments must be admitted: %s", why)
	}
}

func TestLoop_KeepsRefusingWhileHammeredThenClears(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	g := newLoopGuard(2, 10*time.Second, &now)

	g.Admit("frugal__extract", "fp") // t=0
	now = now.Add(time.Second)
	g.Admit("frugal__extract", "fp") // t=1
	now = now.Add(time.Second)
	if ok, _ := g.Admit("frugal__extract", "fp"); ok { // t=2, third call
		t.Fatal("third call must be refused")
	}
	// Hammering every second keeps the refusal up past the original
	// window, because refused calls are recorded too.
	for i := 0; i < 15; i++ {
		now = now.Add(time.Second)
		if ok, _ := g.Admit("frugal__extract", "fp"); ok {
			t.Fatalf("call at t=%ds admitted while still hammering", 3+i)
		}
	}
	// Stop for a full window and the fingerprint is clean again.
	now = now.Add(11 * time.Second)
	if ok, why := g.Admit("frugal__extract", "fp"); !ok {
		t.Fatalf("after a quiet window the call must be admitted: %s", why)
	}
}

func TestLoop_SlidingWindowAdmitsSpacedRepeats(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	g := newLoopGuard(2, 10*time.Second, &now)

	// Two calls 8s apart, then one more 8s later: the first has aged
	// out, so only two are in the window and the call is admitted.
	g.Admit("t", "fp")
	now = now.Add(8 * time.Second)
	g.Admit("t", "fp")
	now = now.Add(8 * time.Second)
	if ok, why := g.Admit("t", "fp"); !ok {
		t.Fatalf("spaced repeats within the limit must be admitted: %s", why)
	}
}

func TestLoop_SetLoopGuardZeroTurnsOffAndForgets(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	g := newLoopGuard(1, time.Minute, &now)
	g.Admit("t", "fp")
	if ok, _ := g.Admit("t", "fp"); ok {
		t.Fatal("second call must be refused at limit 1")
	}
	g.SetLoopGuard(0, 0)
	if g.LoopGuardEnabled() {
		t.Fatal("limit 0 must turn the rail off")
	}
	if ok, _ := g.Admit("t", "fp"); !ok {
		t.Fatal("rail off must admit the previously refused fingerprint")
	}
}

func TestLoop_WindowDefaultsWhenNonPositive(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	g := newLoopGuard(1, 0, &now)
	g.Admit("t", "fp")
	if ok, _ := g.Admit("t", "fp"); ok {
		t.Fatal("second call must be refused")
	}
	now = now.Add(DefaultLoopWindow + time.Second)
	if ok, _ := g.Admit("t", "fp"); !ok {
		t.Fatal("default window must have elapsed")
	}
}

func TestLoop_MapIsBounded(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	g := newLoopGuard(5, time.Minute, &now)

	// Fill the map past the cap with live (in-window) fingerprints.
	for i := 0; i < maxLoopEntries+200; i++ {
		g.Admit("t", fmt.Sprintf("fp-%d", i))
		now = now.Add(time.Millisecond)
	}
	g.mu.Lock()
	n := len(g.loops)
	g.mu.Unlock()
	if n > maxLoopEntries {
		t.Fatalf("fingerprint map grew to %d, cap is %d", n, maxLoopEntries)
	}

	// Once everything has aged out, a new fingerprint sweeps the stale
	// entries instead of evicting live ones.
	now = now.Add(2 * time.Minute)
	g.Admit("t", "fresh")
	g.mu.Lock()
	n = len(g.loops)
	g.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected the sweep to leave only the fresh entry, got %d", n)
	}
}

func TestLoop_RefusalReasonNamesWaitTime(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	g := newLoopGuard(1, 30*time.Second, &now)
	g.Admit("frugal__browse", "fp")
	now = now.Add(10 * time.Second)
	ok, why := g.Admit("frugal__browse", "fp")
	if ok {
		t.Fatal("must refuse")
	}
	// Oldest call was at t=0, window 30s, now t=10s: 20s to wait.
	if !strings.Contains(why, "wait 20s") {
		t.Errorf("reason %q should say how long to wait (20s)", why)
	}
}
