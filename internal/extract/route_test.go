package extract

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
)

// stubExtractor injects results / errors for one provider in fallback tests.
type stubExtractor struct {
	name  string
	cost  float64
	res   Result
	err   error
	calls int
}

func (s *stubExtractor) Name() string         { return s.name }
func (s *stubExtractor) CostPerCall() float64 { return s.cost }
func (s *stubExtractor) Extract(_ context.Context, _ Query) (Result, error) {
	s.calls++
	return s.res, s.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestOrderByCost_Stable(t *testing.T) {
	a := &stubExtractor{name: "a", cost: 0}
	b := &stubExtractor{name: "b", cost: 0} // tie with a
	c := &stubExtractor{name: "c", cost: 0.001}
	in := []Extractor{c, a, b}
	out := OrderByCost(in)
	if out[0].Name() != "a" || out[1].Name() != "b" || out[2].Name() != "c" {
		t.Errorf("OrderByCost: got %v %v %v, want a b c", out[0].Name(), out[1].Name(), out[2].Name())
	}
}

func TestCallWithFallback_PicksCheapestOnSuccess(t *testing.T) {
	cheap := &stubExtractor{name: "cheap", cost: 0, res: Result{Markdown: "ok"}}
	pricey := &stubExtractor{name: "pricey", cost: 0.001, res: Result{Markdown: "should-not-be-used"}}
	used, res, err := CallWithFallback(context.Background(), []Extractor{pricey, cheap}, Query{URL: "https://x"}, discardLogger(), nil)
	if err != nil {
		t.Fatalf("CallWithFallback: %v", err)
	}
	if used.Name() != "cheap" {
		t.Errorf("used = %q, want cheap", used.Name())
	}
	if pricey.calls != 0 {
		t.Errorf("pricey should not have been called; calls=%d", pricey.calls)
	}
	if res.Markdown != "ok" {
		t.Errorf("unexpected markdown: %q", res.Markdown)
	}
}

func TestCallWithFallback_FallsBackOnTransient(t *testing.T) {
	free := &stubExtractor{name: "free", cost: 0,
		err: routing.Transient("free", 503, errors.New("upstream blip"))}
	paid := &stubExtractor{name: "paid", cost: 0.001,
		res: Result{Markdown: "via-paid", CostUSD: 0.001}}
	used, res, err := CallWithFallback(context.Background(), []Extractor{paid, free}, Query{URL: "https://x"}, discardLogger(), nil)
	if err != nil {
		t.Fatalf("CallWithFallback: %v", err)
	}
	if free.calls != 1 || paid.calls != 1 {
		t.Errorf("expected each tried once; free=%d paid=%d", free.calls, paid.calls)
	}
	if used.Name() != "paid" || res.Markdown != "via-paid" {
		t.Errorf("unexpected winner: %+v / %+v", used, res)
	}
}

func TestCallWithFallback_FallsThroughOnPermanent(t *testing.T) {
	// The advertised goreadability → Firecrawl handoff: empty content on a
	// JS-rendered page classifies Permanent, and the next driver renders it.
	free := &stubExtractor{name: "free", cost: 0,
		err: routing.Permanent("free", 0, errors.New("page empty (needs JS?)"))}
	paid := &stubExtractor{name: "paid", cost: 0.001,
		res: Result{Markdown: "via-paid", CostUSD: 0.001}}
	used, res, err := CallWithFallback(context.Background(), []Extractor{paid, free}, Query{URL: "https://x"}, discardLogger(), nil)
	if err != nil {
		t.Fatalf("expected fallback past the permanent error, got %v", err)
	}
	if free.calls != 1 || paid.calls != 1 {
		t.Errorf("expected each tried once; free=%d paid=%d", free.calls, paid.calls)
	}
	if used.Name() != "paid" || res.Markdown != "via-paid" {
		t.Errorf("unexpected winner: %v / %+v", used, res)
	}
}

func TestCallWithFallback_StopsOnFatal(t *testing.T) {
	// A dead target URL (404/410) is dead for every provider — the chain
	// must stop before spending a paid scrape on it.
	free := &stubExtractor{name: "free", cost: 0,
		err: routing.Fatal("free", 404, errors.New("http 404"))}
	paid := &stubExtractor{name: "paid", cost: 0.001,
		res: Result{Markdown: "should-not-be-tried"}}
	_, _, err := CallWithFallback(context.Background(), []Extractor{paid, free}, Query{URL: "https://x/dead"}, discardLogger(), nil)
	if err == nil {
		t.Fatalf("expected fatal error to propagate")
	}
	if !routing.IsFatal(err) {
		t.Errorf("expected IsFatal, got %v", err)
	}
	if paid.calls != 0 {
		t.Errorf("paid must NOT be tried after a fatal error; calls=%d", paid.calls)
	}
}

func TestCallWithFallback_StopsWhenContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	first := &stubExtractor{name: "first", cost: 0,
		err: routing.Permanent("first", 0, context.Canceled)}
	second := &stubExtractor{name: "second", cost: 0.001,
		res: Result{Markdown: "should-not-be-tried"}}
	_, _, err := CallWithFallback(ctx, []Extractor{second, first}, Query{URL: "https://x"}, discardLogger(), nil)
	if err == nil {
		t.Fatalf("expected error when context is canceled")
	}
	if first.calls != 1 || second.calls != 0 {
		t.Errorf("expected only first tried once ctx is done; first=%d second=%d", first.calls, second.calls)
	}
}

func TestCallWithFallback_NoExtractorsErrors(t *testing.T) {
	_, _, err := CallWithFallback(context.Background(), nil, Query{URL: "https://x"}, discardLogger(), nil)
	if err == nil {
		t.Fatalf("expected error when no extractors configured")
	}
}

func TestCallWithFallback_HookFiresPerAttempt(t *testing.T) {
	free := &stubExtractor{name: "free", cost: 0,
		err: routing.Transient("free", 503, errors.New("blip"))}
	paid := &stubExtractor{name: "paid", cost: 0.001,
		res: Result{Markdown: "via-paid", CostUSD: 0.001}}
	type rec struct {
		provider string
		hadErr   bool
	}
	var got []rec
	hook := func(provider string, _ time.Duration, _ float64, _ bool, err error) {
		got = append(got, rec{provider, err != nil})
	}
	_, _, err := CallWithFallback(context.Background(), []Extractor{paid, free}, Query{URL: "https://x"}, discardLogger(), hook)
	if err != nil {
		t.Fatalf("CallWithFallback: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("hook fired %d times, want 2", len(got))
	}
	if got[0].provider != "free" || !got[0].hadErr {
		t.Errorf("first attempt should be free+error; got %+v", got[0])
	}
	if got[1].provider != "paid" || got[1].hadErr {
		t.Errorf("second attempt should be paid+success; got %+v", got[1])
	}
}

func TestCallPinned_UnknownProviderErrors(t *testing.T) {
	es := []Extractor{&stubExtractor{name: "only", cost: 0}}
	_, _, err := CallPinned(context.Background(), es, "nope", Query{URL: "https://x"}, discardLogger(), nil)
	var notConfigured *ErrProviderNotConfigured
	if !errors.As(err, &notConfigured) {
		t.Fatalf("expected ErrProviderNotConfigured, got %v", err)
	}
	if notConfigured.Name != "nope" {
		t.Errorf("Name = %q, want nope", notConfigured.Name)
	}
}

func TestCallPinned_DoesNotFallBack(t *testing.T) {
	pinned := &stubExtractor{name: "pinned", cost: 0.001,
		err: routing.Transient("pinned", 503, errors.New("blip"))}
	other := &stubExtractor{name: "other", cost: 0,
		res: Result{Markdown: "should-not-be-used"}}
	_, _, err := CallPinned(context.Background(), []Extractor{other, pinned}, "pinned", Query{URL: "https://x"}, discardLogger(), nil)
	if err == nil {
		t.Fatalf("expected transient error to propagate")
	}
	if other.calls != 0 {
		t.Errorf("CallPinned must not fall back; other.calls=%d", other.calls)
	}
}
