package extract

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
)

// slowExtractor blocks until its context is done, like a provider whose
// upstream hangs until the HTTP client gives up.
type slowExtractor struct {
	name  string
	calls atomic.Int32
}

func (s *slowExtractor) Name() string         { return s.name }
func (s *slowExtractor) CostPerCall() float64 { return 0 }
func (s *slowExtractor) Extract(ctx context.Context, _ Query) (Result, error) {
	s.calls.Add(1)
	<-ctx.Done()
	return Result{}, routing.Transient(s.name, 0, ctx.Err())
}

func TestCallInOrder_CallDeadlineStopsFailover(t *testing.T) {
	first := &slowExtractor{name: "slow-a"}
	second := &slowExtractor{name: "slow-b"}
	fast := &stubExtractor{name: "fast", res: Result{Markdown: "ok"}}
	ctx, cancel := routing.WithCallDeadline(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := CallInOrder(ctx, []Extractor{first, second, fast}, Query{URL: "https://x"}, discardLogger(), nil)
	elapsed := time.Since(start)

	if !errors.Is(err, routing.ErrCallDeadline) {
		t.Fatalf("err = %v, want ErrCallDeadline", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("deadline not honored: took %s", elapsed)
	}
	if first.calls.Load() != 1 || second.calls.Load() != 0 || fast.calls != 0 {
		t.Fatalf("failover continued past deadline: calls a=%d b=%d fast=%d", first.calls.Load(), second.calls.Load(), fast.calls)
	}
}

func TestCallPinned_CallDeadline(t *testing.T) {
	slow := &slowExtractor{name: "slow"}
	ctx, cancel := routing.WithCallDeadline(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _, err := CallPinned(ctx, []Extractor{slow}, "slow", Query{URL: "https://x"}, discardLogger(), nil)
	if !errors.Is(err, routing.ErrCallDeadline) {
		t.Fatalf("err = %v, want ErrCallDeadline", err)
	}
}

func TestCallInOrder_ClientCancelIsNotCallDeadline(t *testing.T) {
	slow := &slowExtractor{name: "slow"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, err := CallInOrder(ctx, []Extractor{slow}, Query{URL: "https://x"}, discardLogger(), nil)
	if err == nil || errors.Is(err, routing.ErrCallDeadline) {
		t.Fatalf("err = %v, want non-nil error not tagged ErrCallDeadline", err)
	}
}
