package routing

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DefaultCallTimeout is the overall wall-clock budget for one routed tool
// call — every attempt across the whole failover chain, including
// frugal__execute's extract-to-browse fall-forward. Each provider client
// keeps its own per-request timeout (15–90s); without an overall bound a
// chain that walks several slow providers could block an agent for
// minutes. 60s fits one slow extract plus a fall-forward and still bounds
// the worst-case search chain.
const DefaultCallTimeout = 60 * time.Second

// ErrCallDeadline is the cause attached to a context whose overall call
// deadline expired. Callers can test for it with errors.Is.
var ErrCallDeadline = errors.New("frugal: call deadline exceeded")

// WithCallDeadline bounds ctx by d. A d at or below zero disables the
// overall deadline and returns ctx unchanged (with a no-op cancel). The
// remaining budget flows to every attempt through the returned context.
func WithCallDeadline(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeoutCause(ctx, d, ErrCallDeadline)
}

// CallDeadlineHit reports whether ctx ended because WithCallDeadline's
// budget ran out (as opposed to the client cancelling, or an outer
// deadline such as the HTTP request timeout).
func CallDeadlineHit(ctx context.Context) bool {
	return ctx.Err() != nil && errors.Is(context.Cause(ctx), ErrCallDeadline)
}

// ContextStopError builds the error a routing loop returns when ctx is
// done: attempts already made are joined, and when the overall call
// deadline caused the stop the error says so and wraps ErrCallDeadline.
func ContextStopError(ctx context.Context, capability string, errs []error) error {
	joined := JoinAttempts(errs)
	if CallDeadlineHit(ctx) {
		if joined == nil {
			return fmt.Errorf("%w before any %s provider answered (0 attempts)", ErrCallDeadline, capability)
		}
		return fmt.Errorf("%w; %s failover stopped after %d attempt(s): %v", ErrCallDeadline, capability, len(errs), joined)
	}
	if joined == nil {
		return fmt.Errorf("frugal: %s call cancelled before any provider answered: %w", capability, context.Cause(ctx))
	}
	return joined
}
