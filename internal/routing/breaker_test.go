package routing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// newBreakerGuard builds a Guard with the failure rail on, clock driven
// by *nowp so tests can walk through the open window without sleeping.
func newBreakerGuard(threshold int, failCooldown, rateCooldown time.Duration, nowp *time.Time) *Guard {
	g := newTestGuard(nil, rateCooldown, nowp)
	g.SetFailureBreaker(threshold, failCooldown)
	return g
}

func timeoutErr(provider string) error {
	return Transient(provider, 0, context.DeadlineExceeded)
}

func TestBreaker_OpensAtThresholdAndSkipsProvider(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	g := newBreakerGuard(3, 30*time.Second, time.Minute, &now)

	// Two failures: still admitted, a blip is not an outage.
	for i := 1; i <= 2; i++ {
		if opened := g.RecordOutcome("search", "serper", timeoutErr("serper")); opened {
			t.Fatalf("failure %d must not open the breaker (threshold 3)", i)
		}
		if ok, _ := g.Allow("search", "serper"); !ok {
			t.Fatalf("provider must stay allowed after %d failure(s)", i)
		}
	}
	// Third: opens, and RecordOutcome reports the transition exactly once.
	if opened := g.RecordOutcome("search", "serper", Transient("serper", 503, errors.New("upstream"))); !opened {
		t.Fatal("third consecutive failure must open the breaker")
	}
	ok, why := g.Allow("search", "serper")
	if ok {
		t.Fatal("open breaker must refuse the provider")
	}
	for _, want := range []string{"circuit open", "3 consecutive failures", "HTTP 503", "30s left"} {
		if !strings.Contains(why, want) {
			t.Errorf("reason %q missing %q", why, want)
		}
	}
}

func TestBreaker_SuccessResetsRun(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	g := newBreakerGuard(3, 30*time.Second, time.Minute, &now)

	g.RecordOutcome("search", "serper", timeoutErr("serper"))
	g.RecordOutcome("search", "serper", timeoutErr("serper"))
	g.RecordOutcome("search", "serper", nil) // any success, zero hits included
	g.RecordOutcome("search", "serper", timeoutErr("serper"))
	g.RecordOutcome("search", "serper", timeoutErr("serper"))
	if ok, why := g.Allow("search", "serper"); !ok {
		t.Fatalf("two failures after a success must not open the breaker: %s", why)
	}
}

func TestBreaker_HalfOpenAdmitsThenReopensOnOneFailure(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	g := newBreakerGuard(3, 30*time.Second, time.Minute, &now)
	for i := 0; i < 3; i++ {
		g.RecordOutcome("extract", "jina", timeoutErr("jina"))
	}
	if ok, _ := g.Allow("extract", "jina"); ok {
		t.Fatal("breaker should be open")
	}

	// Window elapses: half-open, the provider gets one more chance.
	now = now.Add(31 * time.Second)
	if ok, why := g.Allow("extract", "jina"); !ok {
		t.Fatalf("expired breaker must admit the provider again: %s", why)
	}
	// The probe fails: reopen at once, no fresh run of three required.
	if opened := g.RecordOutcome("extract", "jina", timeoutErr("jina")); !opened {
		t.Fatal("a failure in the half-open state must reopen the breaker (and report the transition)")
	}
	ok, why := g.Allow("extract", "jina")
	if ok {
		t.Fatal("reopened breaker must refuse the provider")
	}
	if !strings.Contains(why, "4 consecutive failures") {
		t.Errorf("reason should carry the full run, got %q", why)
	}

	// Window elapses again, the probe succeeds: closed, run cleared.
	now = now.Add(31 * time.Second)
	if ok, _ := g.Allow("extract", "jina"); !ok {
		t.Fatal("expired breaker must admit the provider again")
	}
	g.RecordOutcome("extract", "jina", nil)
	for i := 0; i < 2; i++ {
		g.RecordOutcome("extract", "jina", timeoutErr("jina"))
	}
	if ok, why := g.Allow("extract", "jina"); !ok {
		t.Fatalf("after a successful probe the run must start from zero: %s", why)
	}
}

func TestBreaker_IgnoresRateLimitPermanentAndFatal(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	g := newBreakerGuard(2, 30*time.Second, time.Minute, &now)

	cases := []struct {
		name string
		err  error
	}{
		{"429 belongs to the rate-limit rail", Transient("youcom", 429, errors.New("slow down"))},
		{"401 is a key problem, not an outage", Permanent("youcom", 401, errors.New("bad key"))},
		{"402 is a billing problem", Permanent("youcom", 402, errors.New("payment"))},
		{"fatal is about the request", Fatal("youcom", 404, errors.New("gone"))},
		{"cancelled caller", Permanent("youcom", 0, context.Canceled)},
	}
	for _, c := range cases {
		for i := 0; i < 5; i++ {
			if opened := g.RecordOutcome("search", "youcom", c.err); opened {
				t.Fatalf("%s: must never open the breaker", c.name)
			}
		}
		if ok, why := g.Allow("search", "youcom"); !ok {
			t.Fatalf("%s: provider must stay allowed on the failure rail, got %q", c.name, why)
		}
	}
	// And they do not extend a real run either: one timeout, then a 401,
	// then a timeout is two consecutive failures and trips threshold 2.
	g.RecordOutcome("search", "youcom", timeoutErr("youcom"))
	g.RecordOutcome("search", "youcom", Permanent("youcom", 401, errors.New("bad key")))
	if opened := g.RecordOutcome("search", "youcom", timeoutErr("youcom")); !opened {
		t.Fatal("a permanent error in the middle must neither count nor reset the run")
	}
}

func TestBreaker_UntypedErrorsCount(t *testing.T) {
	// Drivers that return a bare error (not *routing.Error) are treated as
	// transient everywhere else in the router; the breaker agrees.
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	g := newBreakerGuard(2, 30*time.Second, time.Minute, &now)
	g.RecordOutcome("browse", "browserless", errors.New("dial tcp: connection refused"))
	if opened := g.RecordOutcome("browse", "browserless", errors.New("dial tcp: connection refused")); !opened {
		t.Fatal("bare network errors must count toward the breaker")
	}
	_, why := g.Allow("browse", "browserless")
	if !strings.Contains(why, "connection refused") {
		t.Errorf("reason should name the symptom, got %q", why)
	}
}

func TestBreaker_KeysAreIndependentPerCapability(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	g := newBreakerGuard(1, 30*time.Second, time.Minute, &now)
	g.RecordOutcome("search", "youcom", timeoutErr("youcom"))
	if ok, _ := g.Allow("search", "youcom"); ok {
		t.Fatal("search/youcom should be fenced")
	}
	if ok, _ := g.Allow("extract", "youcom"); !ok {
		t.Fatal("extract/youcom is a different key and must be unaffected")
	}
	if ok, _ := g.Allow("search", "serper"); !ok {
		t.Fatal("search/serper must be unaffected")
	}
}

func TestBreaker_CooldownDefaultsToRateLimitCooldown(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	g := newBreakerGuard(1, 0, 90*time.Second, &now)
	if th, cd := g.FailureBreakerSettings(); th != 1 || cd != 90*time.Second {
		t.Fatalf("settings = (%d, %s), want (1, 90s): a zero failure cooldown must reuse routing.cooldown", th, cd)
	}
	g.RecordOutcome("search", "serper", timeoutErr("serper"))
	now = now.Add(89 * time.Second)
	if ok, _ := g.Allow("search", "serper"); ok {
		t.Fatal("still inside the 90s window")
	}
	now = now.Add(2 * time.Second)
	if ok, _ := g.Allow("search", "serper"); !ok {
		t.Fatal("window elapsed")
	}
}

func TestBreaker_DisabledAndNilGuardAreNoOps(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	g := newTestGuard(nil, time.Minute, &now) // rail never enabled
	if g.FailureBreakerEnabled() {
		t.Fatal("rail must be off by default at the library level")
	}
	for i := 0; i < 10; i++ {
		if opened := g.RecordOutcome("search", "serper", timeoutErr("serper")); opened {
			t.Fatal("disabled rail must never open")
		}
	}
	if ok, _ := g.Allow("search", "serper"); !ok {
		t.Fatal("disabled rail must never refuse")
	}

	// Turning it on and then off again clears state.
	g.SetFailureBreaker(1, time.Second)
	g.RecordOutcome("search", "serper", timeoutErr("serper"))
	g.SetFailureBreaker(0, 0)
	if ok, _ := g.Allow("search", "serper"); !ok {
		t.Fatal("turning the rail off must release fenced providers")
	}

	var nilG *Guard
	if nilG.RecordOutcome("search", "x", timeoutErr("x")) || nilG.FailureBreakerEnabled() {
		t.Fatal("nil guard must be a no-op")
	}
	nilG.SetFailureBreaker(3, time.Second) // must not panic
}

func TestBreaker_RateLimitAndBreakerBothReportedCoherently(t *testing.T) {
	// A provider inside its 429 cooldown reports that reason first; once
	// the cooldown lapses an open breaker still refuses it. The two rails
	// do not mask each other's expiry.
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	g := newBreakerGuard(1, 5*time.Minute, time.Minute, &now)
	g.RecordOutcome("search", "serper", timeoutErr("serper"))
	g.RecordRateLimit("search", "serper")
	if _, why := g.Allow("search", "serper"); !strings.Contains(why, "rate limit") {
		t.Fatalf("rate-limit reason expected first, got %q", why)
	}
	now = now.Add(2 * time.Minute)
	if _, why := g.Allow("search", "serper"); !strings.Contains(why, "circuit open") {
		t.Fatalf("breaker reason expected once the cooldown lapsed, got %q", why)
	}
}

func TestShortError(t *testing.T) {
	cases := map[error]string{
		Transient("p", 503, errors.New("x")):        "HTTP 503",
		Transient("p", 0, context.DeadlineExceeded): "context deadline exceeded",
		errors.New("line one\nline two"):            "line one...",
		errors.New(strings.Repeat("a", 100)):        strings.Repeat("a", 80) + "...",
	}
	for err, want := range cases {
		if got := shortError(err); got != want {
			t.Errorf("shortError(%v) = %q, want %q", err, got, want)
		}
	}
}
