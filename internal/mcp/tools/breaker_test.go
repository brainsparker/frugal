package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// flakySearcher is a fakeSearcher that counts how many times the chain
// actually reached it, which is what the failure breaker protects: a
// provider that is down should stop being called, not merely stop
// winning.
type flakySearcher struct {
	fakeSearcher
	calls int
}

func (f *flakySearcher) Search(ctx context.Context, q search.Query) (search.Results, error) {
	f.calls++
	return f.fakeSearcher.Search(ctx, q)
}

func breakerGuard(threshold int) *routing.Guard {
	g := routing.NewGuard(nil, time.Minute)
	g.SetFailureBreaker(threshold, time.Minute)
	return g
}

func TestCallTool_BreakerStopsCallingAProviderThatKeepsTimingOut(t *testing.T) {
	// The cheap rung times out on every call. Without the breaker every
	// search pays that timeout before failing over; with it, the third
	// call skips the rung entirely and the fallback still answers.
	dead := &flakySearcher{fakeSearcher: fakeSearcher{
		name: "dead", cost: 0,
		err: routing.Transient("dead", 0, context.DeadlineExceeded),
	}}
	alive := &flakySearcher{fakeSearcher: fakeSearcher{
		name: "alive", cost: 0.001,
		results: []search.Item{{Title: "hit", URL: "https://alive.example/x"}},
	}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{dead, alive}, nil, WithGuard(breakerGuard(2)))

	client, cleanup := dialClient(t, srv)
	defer cleanup()

	for i := 1; i <= 3; i++ {
		// Distinct queries so the loop guard and result cache stay out of
		// the picture; the breaker keys on the provider, not the query.
		res := callSearch(t, client, map[string]any{"query": "query " + strings.Repeat("x", i)})
		if res.IsError {
			t.Fatalf("call %d must still be answered by the fallback: %s", i, errorText(res))
		}
		out, err := decodeSearchOutput(res.StructuredContent)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.ProviderUsed != "alive" {
			t.Fatalf("call %d: provider_used = %q, want alive", i, out.ProviderUsed)
		}
	}
	if dead.calls != 2 {
		t.Errorf("dead provider reached %d times, want 2: the breaker must skip it once the threshold is hit", dead.calls)
	}
	if alive.calls != 3 {
		t.Errorf("alive provider reached %d times, want 3", alive.calls)
	}
}

func TestExecute_ReasonNamesTheOpenBreaker(t *testing.T) {
	// The routing trace on frugal__execute is the receipt; a skipped
	// rung must be visible there with the reason, like budget skips are.
	dead := &flakySearcher{fakeSearcher: fakeSearcher{
		name: "dead", cost: 0,
		err: routing.Transient("dead", 503, errors.New("upstream unavailable")),
	}}
	alive := &fakeSearcher{name: "alive", cost: 0.001, results: []search.Item{{Title: "hit"}}}
	srv := newServer()
	RegisterExecute(srv, []search.Searcher{dead, alive}, nil, nil, nil, WithGuard(breakerGuard(1)))

	first := callExecute(t, srv, map[string]any{"intent": "search for anything at all"})
	if first.IsError {
		t.Fatalf("first call must fail over: %s", errorText(first))
	}
	if dead.calls != 1 {
		t.Fatalf("dead reached %d times on the first call, want 1", dead.calls)
	}

	second := callExecute(t, srv, map[string]any{"intent": "search for something else entirely"})
	if second.IsError {
		t.Fatalf("second call must be answered by the fallback: %s", errorText(second))
	}
	out := decodeExecuteOutput(t, second.StructuredContent)
	for _, want := range []string{"skipped dead", "circuit open", "1 consecutive failure", "HTTP 503"} {
		if !strings.Contains(out.Reason, want) {
			t.Errorf("reason %q missing %q", out.Reason, want)
		}
	}
	if dead.calls != 1 {
		t.Errorf("dead reached %d times after the breaker opened, want still 1", dead.calls)
	}
	if out.ProviderUsed != "alive" {
		t.Errorf("provider_used = %q, want alive", out.ProviderUsed)
	}
}

func TestCallTool_BreakerRefusesPinOfFencedProvider(t *testing.T) {
	// A pin does not bypass the breaker, same as deny / budget / cooldown:
	// fenced means fenced, and the error says why and for how long.
	dead := &flakySearcher{fakeSearcher: fakeSearcher{
		name: "dead", cost: 0,
		err: routing.Transient("dead", 502, errors.New("bad gateway")),
	}}
	alive := &fakeSearcher{name: "alive", cost: 0.001, results: []search.Item{{Title: "hit"}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{dead, alive}, nil, WithGuard(breakerGuard(1)))

	client, cleanup := dialClient(t, srv)
	defer cleanup()

	if res := callSearch(t, client, map[string]any{"query": "first"}); res.IsError {
		t.Fatalf("first call must fail over: %s", errorText(res))
	}
	res := callSearch(t, client, map[string]any{"query": "second", "provider": "dead"})
	if !res.IsError {
		t.Fatal("pinning a fenced provider must be refused before any spend")
	}
	text := errorText(res)
	for _, want := range []string{`provider "dead" unavailable`, "circuit open", "HTTP 502"} {
		if !strings.Contains(text, want) {
			t.Errorf("error %q missing %q", text, want)
		}
	}
	if dead.calls != 1 {
		t.Errorf("dead reached %d times, want 1", dead.calls)
	}
}

func TestCallTool_RateLimitDoesNotFeedTheBreaker(t *testing.T) {
	// A 429 belongs to the cooldown rail. With a one-failure breaker and
	// a 429-only provider, the breaker must stay closed: the provider is
	// fenced by the cooldown instead, and the reason says rate limit.
	limited := &flakySearcher{fakeSearcher: fakeSearcher{
		name: "limited", cost: 0,
		err: routing.Transient("limited", 429, errors.New("slow down")),
	}}
	alive := &fakeSearcher{name: "alive", cost: 0.001, results: []search.Item{{Title: "hit"}}}
	srv := newServer()
	RegisterSearch(srv, []search.Searcher{limited, alive}, nil, WithGuard(breakerGuard(1)))

	client, cleanup := dialClient(t, srv)
	defer cleanup()

	if res := callSearch(t, client, map[string]any{"query": "first"}); res.IsError {
		t.Fatalf("first call must fail over: %s", errorText(res))
	}
	res := callSearch(t, client, map[string]any{"query": "second", "provider": "limited"})
	if !res.IsError {
		t.Fatal("rate-limited provider must be fenced by the cooldown")
	}
	text := errorText(res)
	if !strings.Contains(text, "rate limit") || strings.Contains(text, "circuit open") {
		t.Errorf("want the cooldown reason, not the breaker, got %q", text)
	}
}
