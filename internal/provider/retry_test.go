package provider

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/types"
)

type mockInner struct {
	calls    atomic.Int32
	errs     []error
	response *types.ChatCompletionResponse
}

func (m *mockInner) Name() string     { return "mock" }
func (m *mockInner) Models() []string { return nil }

func (m *mockInner) ChatCompletion(ctx context.Context, model string, req *types.ChatCompletionRequest) (*types.ChatCompletionResponse, error) {
	n := int(m.calls.Add(1)) - 1
	if n < len(m.errs) && m.errs[n] != nil {
		return nil, m.errs[n]
	}
	return m.response, nil
}

func (m *mockInner) ChatCompletionStream(ctx context.Context, model string, req *types.ChatCompletionRequest) (<-chan StreamChunk, error) {
	return nil, errors.New("stream not tested")
}

func TestWithRetry_RetriesOn503UntilSuccess(t *testing.T) {
	// Shrink backoff so the test completes instantly.
	orig := retryBackoff
	retryBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	defer func() { retryBackoff = orig }()

	inner := &mockInner{
		errs:     []error{errors.New("openai error 503: unavailable"), errors.New("openai error 503: unavailable")},
		response: &types.ChatCompletionResponse{ID: "ok"},
	}
	p := WithRetry(inner)

	resp, err := p.ChatCompletion(context.Background(), "m", &types.ChatCompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.ID != "ok" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if got := inner.calls.Load(); got != 3 {
		t.Fatalf("expected 3 calls (2 retries + success), got %d", got)
	}
}

func TestWithRetry_GivesUpAfterMaxAttempts(t *testing.T) {
	orig := retryBackoff
	retryBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	defer func() { retryBackoff = orig }()

	inner := &mockInner{
		errs: []error{
			errors.New("openai error 429: rate limit"),
			errors.New("openai error 429: rate limit"),
			errors.New("openai error 429: rate limit"),
			errors.New("openai error 429: rate limit"),
		},
	}
	p := WithRetry(inner)

	_, err := p.ChatCompletion(context.Background(), "m", &types.ChatCompletionRequest{})
	if err == nil {
		t.Fatalf("expected error after giving up")
	}
	if got := inner.calls.Load(); got != 4 {
		t.Fatalf("expected 4 total attempts (initial + 3 retries), got %d", got)
	}
}

func TestWithRetry_DoesNotRetryNonTransient(t *testing.T) {
	inner := &mockInner{
		errs: []error{errors.New("openai error 400: bad request")},
	}
	p := WithRetry(inner)

	_, err := p.ChatCompletion(context.Background(), "m", &types.ChatCompletionRequest{})
	if err == nil {
		t.Fatalf("expected error")
	}
	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 call for non-retryable error, got %d", got)
	}
}

func TestParseRetryAfter_ExtractsSeconds(t *testing.T) {
	err := errors.New("openai error 429: rate limit exceeded, retry-after: 5")
	if got := parseRetryAfter(err); got != 5*time.Second {
		t.Fatalf("parseRetryAfter = %s, want 5s", got)
	}
}
