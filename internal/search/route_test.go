package search

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
)

// stubSearcher injects results / errors for one provider in fallback tests.
// On Search call counter increments so the test can assert who was called.
type stubSearcher struct {
	name  string
	cost  float64
	res   Results
	err   error
	calls int
}

func (s *stubSearcher) Name() string         { return s.name }
func (s *stubSearcher) CostPerCall() float64 { return s.cost }
func (s *stubSearcher) Search(_ context.Context, _ Query) (Results, error) {
	s.calls++
	return s.res, s.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestOrderByCost_Stable(t *testing.T) {
	a := &stubSearcher{name: "a", cost: 0.001}
	b := &stubSearcher{name: "b", cost: 0.001} // tie with a
	c := &stubSearcher{name: "c", cost: 0.005}
	in := []Searcher{c, a, b}
	out := OrderByCost(in)
	if got := []string{out[0].Name(), out[1].Name(), out[2].Name()}; got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("OrderByCost: got %v, want [a b c]", got)
	}
	if &in[0] == &out[0] {
		t.Errorf("OrderByCost should not mutate the input slice")
	}
}

func TestCallWithFallback_PicksCheapestOnSuccess(t *testing.T) {
	cheap := &stubSearcher{name: "cheap", cost: 0.001,
		res: Results{Items: []Item{{Title: "ok"}}, CostUSD: 0.001}}
	pricey := &stubSearcher{name: "pricey", cost: 0.01,
		res: Results{Items: []Item{{Title: "should-not-be-used"}}, CostUSD: 0.01}}
	used, res, err := CallWithFallback(context.Background(), []Searcher{pricey, cheap}, Query{Text: "x"}, discardLogger(), nil)
	if err != nil {
		t.Fatalf("CallWithFallback: %v", err)
	}
	if used.Name() != "cheap" {
		t.Errorf("used = %q, want cheap", used.Name())
	}
	if pricey.calls != 0 {
		t.Errorf("pricey should not have been called; calls=%d", pricey.calls)
	}
	if len(res.Items) != 1 || res.Items[0].Title != "ok" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestCallWithFallback_FallsBackOnTransient(t *testing.T) {
	free := &stubSearcher{name: "free", cost: 0,
		err: routing.Transient("free", 503, errors.New("upstream timeout"))}
	cheap := &stubSearcher{name: "cheap", cost: 0.001,
		res: Results{Items: []Item{{Title: "via-cheap"}}, CostUSD: 0.001}}
	used, res, err := CallWithFallback(context.Background(), []Searcher{cheap, free}, Query{Text: "x"}, discardLogger(), nil)
	if err != nil {
		t.Fatalf("CallWithFallback: %v", err)
	}
	if free.calls != 1 {
		t.Errorf("free should have been tried once; calls=%d", free.calls)
	}
	if cheap.calls != 1 {
		t.Errorf("cheap should have been tried after free's failure; calls=%d", cheap.calls)
	}
	if used.Name() != "cheap" {
		t.Errorf("used = %q, want cheap", used.Name())
	}
	if res.Items[0].Title != "via-cheap" {
		t.Errorf("got result from wrong provider: %+v", res)
	}
}

func TestCallWithFallback_FallsThroughOnPermanent(t *testing.T) {
	// A permanent error is provider-scoped — a 401 from one provider says
	// nothing about the next one's credentials — so the chain keeps going.
	free := &stubSearcher{name: "free", cost: 0,
		err: routing.Permanent("free", 401, errors.New("invalid api key"))}
	cheap := &stubSearcher{name: "cheap", cost: 0.001,
		res: Results{Items: []Item{{Title: "via-cheap"}}, CostUSD: 0.001}}
	used, res, err := CallWithFallback(context.Background(), []Searcher{cheap, free}, Query{Text: "x"}, discardLogger(), nil)
	if err != nil {
		t.Fatalf("expected fallback past the permanent error, got %v", err)
	}
	if free.calls != 1 {
		t.Errorf("free calls=%d, want 1", free.calls)
	}
	if cheap.calls != 1 {
		t.Errorf("cheap should be tried after free's permanent failure; calls=%d", cheap.calls)
	}
	if used.Name() != "cheap" || res.Items[0].Title != "via-cheap" {
		t.Errorf("unexpected winner: %v / %+v", used.Name(), res)
	}
}

func TestCallWithFallback_StopsOnFatal(t *testing.T) {
	free := &stubSearcher{name: "free", cost: 0,
		err: routing.Fatal("free", 0, errors.New("request cannot succeed"))}
	cheap := &stubSearcher{name: "cheap", cost: 0.001,
		res: Results{Items: []Item{{Title: "should-not-be-tried"}}}}
	_, _, err := CallWithFallback(context.Background(), []Searcher{cheap, free}, Query{Text: "x"}, discardLogger(), nil)
	if err == nil {
		t.Fatalf("expected fatal error to propagate")
	}
	if !routing.IsFatal(err) {
		t.Errorf("expected IsFatal, got %v", err)
	}
	if cheap.calls != 0 {
		t.Errorf("cheap must NOT be tried after a fatal error; calls=%d", cheap.calls)
	}
}

func TestCallWithFallback_AllFailedErrorNamesEveryProvider(t *testing.T) {
	// The agent-visible error must not hide the cheap provider's root
	// cause behind whatever the last provider happened to say.
	free := &stubSearcher{name: "free", cost: 0,
		err: routing.Permanent("free", 401, errors.New("invalid api key"))}
	cheap := &stubSearcher{name: "cheap", cost: 0.001,
		err: routing.Transient("cheap", 503, errors.New("upstream down"))}
	_, _, err := CallWithFallback(context.Background(), []Searcher{cheap, free}, Query{Text: "x"}, discardLogger(), nil)
	if err == nil {
		t.Fatalf("expected error when all providers fail")
	}
	for _, want := range []string{"invalid api key", "upstream down"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("joined error should mention %q; got %v", want, err)
		}
	}
}

func TestCallWithFallback_StopsWhenContextDone(t *testing.T) {
	// When the caller cancels, walking the remaining providers just burns
	// quota on requests nobody is waiting for.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	first := &stubSearcher{name: "first", cost: 0,
		err: routing.Permanent("first", 0, context.Canceled)}
	second := &stubSearcher{name: "second", cost: 0.001,
		res: Results{Items: []Item{{Title: "should-not-be-tried"}}}}
	_, _, err := CallWithFallback(ctx, []Searcher{second, first}, Query{Text: "x"}, discardLogger(), nil)
	if err == nil {
		t.Fatalf("expected error when context is canceled")
	}
	if first.calls != 1 {
		t.Errorf("first calls=%d, want 1", first.calls)
	}
	if second.calls != 0 {
		t.Errorf("second should NOT be tried once ctx is done; calls=%d", second.calls)
	}
}

func TestCallWithFallback_AllTransientReturnsLastError(t *testing.T) {
	free := &stubSearcher{name: "free", cost: 0,
		err: routing.Transient("free", 503, errors.New("first"))}
	cheap := &stubSearcher{name: "cheap", cost: 0.001,
		err: routing.Transient("cheap", 502, errors.New("second"))}
	_, _, err := CallWithFallback(context.Background(), []Searcher{cheap, free}, Query{Text: "x"}, discardLogger(), nil)
	if err == nil {
		t.Fatalf("expected non-nil error when all providers fail transiently")
	}
	if !routing.IsTransient(err) {
		t.Errorf("expected IsTransient, got %v", err)
	}
	var e *routing.Error
	if !errors.As(err, &e) || e.Provider != "cheap" {
		t.Errorf("expected last error from cheap, got %v", err)
	}
	if free.calls != 1 || cheap.calls != 1 {
		t.Errorf("expected each to be tried once; free=%d cheap=%d", free.calls, cheap.calls)
	}
}

func TestCallWithFallback_NoSearchersErrors(t *testing.T) {
	_, _, err := CallWithFallback(context.Background(), nil, Query{Text: "x"}, discardLogger(), nil)
	if err == nil {
		t.Fatalf("expected error when no providers configured")
	}
}

func TestCallWithFallback_HookFiresPerAttempt(t *testing.T) {
	free := &stubSearcher{name: "free", cost: 0,
		err: routing.Transient("free", 503, errors.New("blip"))}
	paid := &stubSearcher{name: "paid", cost: 0.001,
		res: Results{Items: []Item{{Title: "via-paid"}}, CostUSD: 0.001}}
	type record struct {
		provider string
		hadErr   bool
		won      bool
		cost     float64
	}
	var got []record
	hook := func(provider string, _ time.Duration, cost float64, won bool, err error) {
		got = append(got, record{provider, err != nil, won, cost})
	}
	_, _, err := CallWithFallback(context.Background(), []Searcher{paid, free}, Query{Text: "x"}, discardLogger(), hook)
	if err != nil {
		t.Fatalf("CallWithFallback: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("hook fired %d times, want 2", len(got))
	}
	if got[0].provider != "free" || !got[0].hadErr || got[0].won {
		t.Errorf("first attempt should be free with error, not won; got %+v", got[0])
	}
	if got[1].provider != "paid" || got[1].hadErr || !got[1].won || got[1].cost != 0.001 {
		t.Errorf("second attempt should be paid winning success at 0.001; got %+v", got[1])
	}
}

func TestCallWithFallback_ZeroHitAttemptIsNotWon(t *testing.T) {
	// The zero-hit attempt the router walks past must not report won=true
	// to the hook — it earned no rack credit in the usage ledger.
	empty := &stubSearcher{name: "empty", cost: 0, res: Results{CostUSD: 0}}
	paid := &stubSearcher{name: "paid", cost: 0.001,
		res: Results{Items: []Item{{Title: "via-paid"}}, CostUSD: 0.001}}
	wins := map[string]bool{}
	hook := func(provider string, _ time.Duration, _ float64, won bool, _ error) {
		wins[provider] = won
	}
	_, _, err := CallWithFallback(context.Background(), []Searcher{paid, empty}, Query{Text: "x"}, discardLogger(), hook)
	if err != nil {
		t.Fatalf("CallWithFallback: %v", err)
	}
	if wins["empty"] || !wins["paid"] {
		t.Errorf("won flags wrong: %+v (want empty=false paid=true)", wins)
	}
}

func TestCallWithFallback_ZeroHitsFallsThrough(t *testing.T) {
	// A provider that succeeds with zero hits isn't "a result" — the
	// chain must keep walking. Marginalia whiffing on a mainstream query
	// should hand off to Serper, not end the call empty.
	empty := &stubSearcher{name: "empty", cost: 0,
		res: Results{Items: nil, CostUSD: 0}}
	paid := &stubSearcher{name: "paid", cost: 0.001,
		res: Results{Items: []Item{{Title: "via-paid"}}, CostUSD: 0.001}}
	used, res, err := CallWithFallback(context.Background(), []Searcher{paid, empty}, Query{Text: "x"}, discardLogger(), nil)
	if err != nil {
		t.Fatalf("CallWithFallback: %v", err)
	}
	if empty.calls != 1 {
		t.Errorf("empty (cheapest) should have been tried first; calls=%d", empty.calls)
	}
	if used.Name() != "paid" || len(res.Items) != 1 {
		t.Errorf("expected fall-through to paid with 1 item; used=%q res=%+v", used.Name(), res)
	}
}

func TestCallWithFallback_PaidZeroHitsReturnsThatEmptySuccess(t *testing.T) {
	// A free provider's whiff falls through, but a PAID provider's empty
	// answer is evidence the query has no hits: it becomes the return
	// value, carrying its own real CostUSD — and it still isn't "won"
	// (no hits, no savings credit).
	free := &stubSearcher{name: "free", cost: 0, res: Results{CostUSD: 0}}
	paid := &stubSearcher{name: "paid", cost: 0.001, res: Results{CostUSD: 0.001}}
	wins := map[string]bool{}
	hook := func(provider string, _ time.Duration, _ float64, won bool, _ error) {
		wins[provider] = won
	}
	used, res, err := CallWithFallback(context.Background(), []Searcher{paid, free}, Query{Text: "x"}, discardLogger(), hook)
	if err != nil {
		t.Fatalf("CallWithFallback: %v", err)
	}
	if used.Name() != "paid" {
		t.Errorf("used = %q, want paid (its empty success ends the chain)", used.Name())
	}
	if len(res.Items) != 0 || res.CostUSD != 0.001 {
		t.Errorf("expected empty results at paid's real cost; got %+v", res)
	}
	if free.calls != 1 || paid.calls != 1 {
		t.Errorf("both providers should have been tried; free=%d paid=%d", free.calls, paid.calls)
	}
	if wins["free"] || wins["paid"] {
		t.Errorf("zero-hit attempts must not be won; wins=%+v", wins)
	}
}

func TestCallWithFallback_PaidZeroHitsStopsBeforePricierPaid(t *testing.T) {
	// Once a paid provider confirms zero hits, a more expensive paid
	// provider must not be called just to re-discover emptiness.
	free := &stubSearcher{name: "free", cost: 0, res: Results{CostUSD: 0}}
	cheapPaid := &stubSearcher{name: "cheap-paid", cost: 0.001, res: Results{CostUSD: 0.001}}
	priceyPaid := &stubSearcher{name: "pricey-paid", cost: 0.005,
		res: Results{Items: []Item{{Title: "should-not-be-fetched"}}, CostUSD: 0.005}}
	used, res, err := CallWithFallback(context.Background(), []Searcher{priceyPaid, cheapPaid, free}, Query{Text: "x"}, discardLogger(), nil)
	if err != nil {
		t.Fatalf("CallWithFallback: %v", err)
	}
	if priceyPaid.calls != 0 {
		t.Errorf("pricey-paid must NOT be called after cheap-paid's empty success; calls=%d", priceyPaid.calls)
	}
	if used.Name() != "cheap-paid" || len(res.Items) != 0 {
		t.Errorf("expected cheap-paid's empty success; used=%q res=%+v", used.Name(), res)
	}
}

func TestCallWithFallback_AllFreeZeroHitsReturnsFirstEmptySuccess(t *testing.T) {
	// Free providers' coverage gaps say little, so they all get tried;
	// when the whole (free) chain comes up empty the query just has no
	// hits: return the first empty success, not an error.
	a := &stubSearcher{name: "a", cost: 0, res: Results{CostUSD: 0}}
	b := &stubSearcher{name: "b", cost: 0, res: Results{CostUSD: 0}}
	used, res, err := CallWithFallback(context.Background(), []Searcher{a, b}, Query{Text: "x"}, discardLogger(), nil)
	if err != nil {
		t.Fatalf("CallWithFallback: %v", err)
	}
	if used.Name() != "a" {
		t.Errorf("used = %q, want a (first empty success)", used.Name())
	}
	if len(res.Items) != 0 {
		t.Errorf("expected empty results, got %+v", res.Items)
	}
	if a.calls != 1 || b.calls != 1 {
		t.Errorf("both providers should have been tried; a=%d b=%d", a.calls, b.calls)
	}
}

func TestCallWithFallback_ZeroHitsThenErrorReturnsEmptySuccess(t *testing.T) {
	// Empty-but-succeeded beats a later provider's failure: the query
	// was processed fine by someone, it just had no hits there.
	empty := &stubSearcher{name: "empty", cost: 0, res: Results{CostUSD: 0}}
	broken := &stubSearcher{name: "broken", cost: 0.001,
		err: routing.Transient("broken", 503, errors.New("down"))}
	used, res, err := CallWithFallback(context.Background(), []Searcher{broken, empty}, Query{Text: "x"}, discardLogger(), nil)
	if err != nil {
		t.Fatalf("CallWithFallback: %v", err)
	}
	if used.Name() != "empty" || len(res.Items) != 0 {
		t.Errorf("expected zero-hit success from empty; used=%q res=%+v", used.Name(), res)
	}
}

func TestCallPinned_ZeroHitSuccessIsNotWon(t *testing.T) {
	// A pinned provider returning zero items must not earn rack-rate
	// savings credit — same rule as the fallback path: no hits, no win.
	empty := &stubSearcher{name: "empty", cost: 0.001, res: Results{CostUSD: 0.001}}
	full := &stubSearcher{name: "full", cost: 0.001,
		res: Results{Items: []Item{{Title: "hit"}}, CostUSD: 0.001}}
	wins := map[string]bool{}
	hook := func(provider string, _ time.Duration, _ float64, won bool, _ error) {
		wins[provider] = won
	}
	if _, _, err := CallPinned(context.Background(), []Searcher{empty, full}, "empty", Query{Text: "x"}, discardLogger(), hook); err != nil {
		t.Fatalf("CallPinned(empty): %v", err)
	}
	if _, _, err := CallPinned(context.Background(), []Searcher{empty, full}, "full", Query{Text: "x"}, discardLogger(), hook); err != nil {
		t.Fatalf("CallPinned(full): %v", err)
	}
	if wins["empty"] || !wins["full"] {
		t.Errorf("won flags wrong: %+v (want empty=false full=true)", wins)
	}
}

func TestCallPinned_UnknownProviderErrors(t *testing.T) {
	s := []Searcher{&stubSearcher{name: "only", cost: 0.001}}
	_, _, err := CallPinned(context.Background(), s, "nope", Query{Text: "x"}, discardLogger(), nil)
	var notConfigured *ErrProviderNotConfigured
	if !errors.As(err, &notConfigured) {
		t.Fatalf("expected ErrProviderNotConfigured, got %v", err)
	}
	if notConfigured.Name != "nope" {
		t.Errorf("Name = %q, want nope", notConfigured.Name)
	}
}

func TestCallPinned_DoesNotFallBackOnTransient(t *testing.T) {
	// Pinned calls bypass fallback even when the named provider fails
	// transiently — the caller asked for THIS provider specifically.
	pinned := &stubSearcher{name: "pinned", cost: 0.01,
		err: routing.Transient("pinned", 503, errors.New("upstream blip"))}
	otherCheap := &stubSearcher{name: "other", cost: 0.001,
		res: Results{Items: []Item{{Title: "should-not-be-used"}}, CostUSD: 0.001}}
	_, _, err := CallPinned(context.Background(), []Searcher{otherCheap, pinned}, "pinned", Query{Text: "x"}, discardLogger(), nil)
	if err == nil {
		t.Fatalf("expected transient error to propagate")
	}
	if otherCheap.calls != 0 {
		t.Errorf("CallPinned must not fall back to other providers; otherCheap.calls=%d", otherCheap.calls)
	}
}
