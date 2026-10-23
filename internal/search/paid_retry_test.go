package search

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
)

// retryingSearcher behaves like a real driver: it wraps each call in
// routing.DoWithRetry with RetryAttemptsFor(its configured cost).
type retryingSearcher struct {
	name  string
	cost  float64
	fails int // transient failures before success; -1 = always fail
	calls int
}

func (s *retryingSearcher) Name() string         { return s.name }
func (s *retryingSearcher) CostPerCall() float64 { return s.cost }
func (s *retryingSearcher) Search(ctx context.Context, _ Query) (Results, error) {
	var res Results
	err := routing.DoWithRetry(ctx, routing.RetryAttemptsFor(s.cost), []time.Duration{time.Millisecond}, func() error {
		s.calls++
		if s.fails < 0 || s.calls <= s.fails {
			return routing.Transient(s.name, 503, errors.New("blip"))
		}
		res = Results{Items: []Item{{Title: s.name, URL: "https://x"}}, CostUSD: s.cost}
		return nil
	})
	return res, err
}

func TestPaidFailsOverWithoutRetry_FreeRetries(t *testing.T) {
	free := &retryingSearcher{name: "free", cost: 0, fails: -1}
	paid := &retryingSearcher{name: "paid", cost: 0.001, fails: -1}
	paid2 := &retryingSearcher{name: "paid2", cost: 0.005, fails: 0}
	used, _, err := CallInOrder(context.Background(), []Searcher{free, paid, paid2}, Query{Text: "q"}, discardLogger(), nil)
	if err != nil || used.Name() != "paid2" {
		t.Fatalf("used=%v err=%v, want paid2", used, err)
	}
	if free.calls != routing.RetryAttempts() {
		t.Errorf("free provider calls = %d, want %d (retried)", free.calls, routing.RetryAttempts())
	}
	if paid.calls != 1 {
		t.Errorf("paid provider calls = %d, want 1 (no retry, failover)", paid.calls)
	}
}
