package routing

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func transientAfter(d time.Duration) error {
	e := Transient("fake", http.StatusTooManyRequests, errors.New("slow down"))
	e.RetryAfter = d
	return e
}

func TestDoWithRetry_HonorsRetryAfter(t *testing.T) {
	calls := 0
	start := time.Now()
	err := DoWithRetry(context.Background(), 2, []time.Duration{time.Millisecond}, func() error {
		calls++
		if calls == 1 {
			return transientAfter(150 * time.Millisecond)
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("err=%v calls=%d, want success on retry", err, calls)
	}
	if el := time.Since(start); el < 150*time.Millisecond {
		t.Fatalf("retried after %s, want >= Retry-After 150ms", el)
	}
}

func TestDoWithRetry_RetryAfterAboveCapFailsOver(t *testing.T) {
	calls := 0
	err := DoWithRetry(context.Background(), 3, DefaultBackoff, func() error {
		calls++
		return transientAfter(MaxRetryAfter + time.Second)
	})
	if err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d, want one call and the provider error", err, calls)
	}
}

func TestDoWithRetry_NeverSleepsPastDeadline(t *testing.T) {
	ctx, cancel := WithCallDeadline(context.Background(), 100*time.Millisecond)
	defer cancel()
	calls := 0
	start := time.Now()
	err := DoWithRetry(ctx, 3, DefaultBackoff, func() error {
		calls++
		return transientAfter(time.Second) // within cap, but past the deadline
	})
	if calls != 1 {
		t.Fatalf("calls=%d, want 1 (wait would outlast the deadline)", calls)
	}
	var re *Error
	if !errors.As(err, &re) || re.Status != http.StatusTooManyRequests {
		t.Fatalf("err=%v, want the provider's 429", err)
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatalf("slept toward the deadline instead of returning")
	}
}

func TestDoWithRetry_PermanentNotRetried(t *testing.T) {
	calls := 0
	_ = DoWithRetry(context.Background(), 3, []time.Duration{time.Millisecond}, func() error {
		calls++
		return Permanent("fake", http.StatusUnauthorized, errors.New("bad key"))
	})
	if calls != 1 {
		t.Fatalf("calls=%d, want 1", calls)
	}
}

func TestSetRetries(t *testing.T) {
	defer SetRetries(DefaultRetries)
	SetRetries(0)
	if RetryAttempts() != 1 {
		t.Fatalf("RetryAttempts=%d, want 1", RetryAttempts())
	}
	calls := 0
	_ = DoWithRetry(context.Background(), RetryAttempts(), []time.Duration{time.Millisecond}, func() error {
		calls++
		return Transient("fake", 503, errors.New("down"))
	})
	if calls != 1 {
		t.Fatalf("calls=%d with retries=0, want 1", calls)
	}
	SetRetries(-3)
	if Retries() != 0 {
		t.Fatalf("negative retries not clamped")
	}
}

func TestParseRetryAfter(t *testing.T) {
	h := http.Header{}
	if ParseRetryAfter(h) != 0 {
		t.Fatal("absent header should be 0")
	}
	h.Set("Retry-After", "3")
	if ParseRetryAfter(h) != 3*time.Second {
		t.Fatalf("seconds form: %s", ParseRetryAfter(h))
	}
	h.Set("Retry-After", time.Now().Add(10*time.Second).UTC().Format(http.TimeFormat))
	if d := ParseRetryAfter(h); d < 8*time.Second || d > 11*time.Second {
		t.Fatalf("date form: %s", d)
	}
	h.Set("Retry-After", "soon")
	if ParseRetryAfter(h) != 0 {
		t.Fatal("garbage should be 0")
	}
}

func TestGuard_CooldownHonorsRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	g := NewGuard(nil, time.Minute)
	g.now = func() time.Time { return now }
	g.RecordRateLimitFor("search", "p", 5*time.Minute)
	now = now.Add(2 * time.Minute)
	if ok, _ := g.Allow("search", "p"); ok {
		t.Fatal("cooldown should extend to Retry-After (5m)")
	}
	now = now.Add(4 * time.Minute)
	if ok, _ := g.Allow("search", "p"); !ok {
		t.Fatal("cooldown should have elapsed")
	}
	g.RecordRateLimitFor("search", "q", 24*time.Hour)
	now = now.Add(MaxCooldownRetryAfter + time.Second)
	if ok, _ := g.Allow("search", "q"); !ok {
		t.Fatal("Retry-After cooldown should be capped")
	}
}
