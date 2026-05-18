package provider

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/frugalsh/frugal/internal/obs"
	"github.com/frugalsh/frugal/internal/types"
)

// retryBackoff is capped and intentionally tight: Frugal proxies user-facing
// latency-sensitive calls, so we'd rather surface a failure quickly than
// stretch a bad upstream window. Streaming is never retried past the first
// handshake — once bytes flow, the router owns fallback.
var retryBackoff = []time.Duration{50 * time.Millisecond, 200 * time.Millisecond, 800 * time.Millisecond}

// WithRetry wraps a Provider so non-streaming ChatCompletion calls retry
// on transient upstream failures (429 / 502 / 503 / 504), honoring
// Retry-After when the provider includes it in the error string.
func WithRetry(p Provider) Provider {
	return &retryingProvider{inner: p}
}

type retryingProvider struct{ inner Provider }

func (r *retryingProvider) Name() string     { return r.inner.Name() }
func (r *retryingProvider) Models() []string { return r.inner.Models() }

func (r *retryingProvider) ChatCompletion(ctx context.Context, model string, req *types.ChatCompletionRequest) (*types.ChatCompletionResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= len(retryBackoff); attempt++ {
		resp, err := r.inner.ChatCompletion(ctx, model, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isRetryable(err) || attempt == len(retryBackoff) {
			return nil, err
		}
		delay := retryBackoff[attempt]
		if hint := parseRetryAfter(err); hint > 0 && hint < 30*time.Second {
			delay = hint
		}
		obs.L(ctx).Warn("upstream retry",
			"provider", r.inner.Name(),
			"model", model,
			"attempt", attempt+1,
			"delay_ms", delay.Milliseconds(),
			"err", err,
		)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, lastErr
}

func (r *retryingProvider) ChatCompletionStream(ctx context.Context, model string, req *types.ChatCompletionRequest) (<-chan StreamChunk, error) {
	// Streams are not retried here. Fallback chain in the proxy handler owns
	// the handshake-level retry; once a chunk has been written to the client,
	// retry is no longer safe.
	return r.inner.ChatCompletionStream(ctx, model, req)
}

// isRetryable classifies provider errors. Matches on the error string because
// every provider.ChatCompletion error is formatted as `<provider> error <code>:
// <body>` today; parsing the numeric status keeps us from coupling to the
// concrete error types.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{" 408", " 429", " 500", " 502", " 503", " 504", "rate limit", "temporarily unavailable"} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

var retryAfterRe = regexp.MustCompile(`(?i)retry[- ]after[: ]+([^\n]+)`)

func parseRetryAfter(err error) time.Duration {
	if err == nil {
		return 0
	}
	m := retryAfterRe.FindStringSubmatch(err.Error())
	if len(m) != 2 {
		return 0
	}
	raw := strings.TrimSpace(strings.TrimRight(m[1], ".,;"))
	if secs, perr := strconv.Atoi(raw); perr == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}

	for _, layout := range []string{time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC} {
		if when, perr := time.Parse(layout, raw); perr == nil {
			delta := time.Until(when)
			if delta <= 0 {
				return 0
			}
			return delta
		}
	}

	return 0
}
