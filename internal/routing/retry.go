package routing

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// DefaultRetries is how many times a driver retries a transient failure
// (429, 408, 5xx, timeouts, connection resets) before the router fails
// over to the next provider. Matches the historical schedule
// (len(DefaultBackoff)).
const DefaultRetries = 2

// MaxRetryAfter caps how long an in-driver retry will wait on a
// provider's Retry-After hint. Longer hints skip the retry and fail over
// instead — waiting minutes on one provider defeats routing.
const MaxRetryAfter = 5 * time.Second

var driverRetries atomic.Int32

func init() { driverRetries.Store(DefaultRetries) }

// SetRetries sets the process-wide in-driver retry count (limits.retries).
// Negative values are treated as 0 (no retries: one attempt per provider).
func SetRetries(n int) {
	if n < 0 {
		n = 0
	}
	driverRetries.Store(int32(n))
}

// Retries reports the configured in-driver retry count.
func Retries() int { return int(driverRetries.Load()) }

// RetryAttempts is the total attempts per provider call: 1 + Retries().
// Drivers pass it to DoWithRetry.
func RetryAttempts() int { return 1 + Retries() }

// ParseRetryAfter reads a Retry-After header (delta-seconds or HTTP-date).
// Returns 0 when absent, unparseable, or in the past.
func ParseRetryAfter(h http.Header) time.Duration {
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// retryWait is how long to sleep before retry i after err: the jittered
// backoff, raised to the provider's Retry-After hint. ok=false means
// don't retry (hint above MaxRetryAfter).
func retryWait(backoff []time.Duration, i int, err error) (time.Duration, bool) {
	wait := jittered(backoff, i)
	var re *Error
	if errors.As(err, &re) && re.RetryAfter > 0 {
		if re.RetryAfter > MaxRetryAfter {
			return 0, false
		}
		if re.RetryAfter > wait {
			wait = re.RetryAfter
		}
	}
	return wait, true
}

// DoWithRetry runs fn up to attempts times. After each transient failure
// it sleeps for the next backoff in the schedule (with up to 30% jitter)
// before retrying (at least the provider's Retry-After, if it sent one
// within MaxRetryAfter, and never past ctx's deadline). On permanent failure or context cancel it returns
// immediately. On success returns nil.
//
// attempts ≤ 1 disables retries (one call total). Callers pass the schedule
// they want — defaults are short on purpose: providers we hit have their
// own ~20s HTTP timeout, and the router is already prepared to fall back
// to a different provider when all retries are exhausted.
func DoWithRetry(ctx context.Context, attempts int, backoff []time.Duration, fn func() error) error {
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err = fn()
		if err == nil {
			return nil
		}
		if IsPermanent(err) {
			return err
		}
		if i == attempts-1 {
			break // out of attempts
		}
		wait, ok := retryWait(backoff, i, err)
		if !ok {
			return err // Retry-After too long: fail over instead
		}
		// Never sleep past the call deadline: if the wait would outlast
		// the remaining budget, return now so the error is the
		// provider's, not a bare deadline.
		if dl, has := ctx.Deadline(); has && time.Until(dl) <= wait {
			return err
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return err
}

// DefaultBackoff is the in-driver retry schedule used by HTTP-bound
// search drivers. 2 retries (3 attempts total) with sub-second waits.
// Tight on purpose: the router will fall back to a different provider
// anyway, so the goal is to absorb brief blips, not to wait out a real
// outage.
var DefaultBackoff = []time.Duration{200 * time.Millisecond, 800 * time.Millisecond}

func jittered(schedule []time.Duration, i int) time.Duration {
	if i < 0 || i >= len(schedule) {
		// Past the schedule — use the last value (or zero if empty).
		if len(schedule) == 0 {
			return 0
		}
		i = len(schedule) - 1
	}
	base := schedule[i]
	if base <= 0 {
		return 0
	}
	jitter := time.Duration(rand.Int63n(int64(base) / 3)) // 0–33%
	return base + jitter
}
