package routing

import (
	"testing"
	"time"
)

type fakeProvider struct {
	name string
	cost float64
}

func (f *fakeProvider) Name() string         { return f.name }
func (f *fakeProvider) CostPerCall() float64 { return f.cost }

// fakeQuoted is a fakeProvider with a quota-aware effective price.
type fakeQuoted struct {
	fakeProvider
	effective float64
}

func (f *fakeQuoted) EffectiveCostPerCall(time.Time) float64 { return f.effective }

func names[T Costed](items []T) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Name())
	}
	return out
}

func assertOrder(t *testing.T, got []Costed, want ...string) {
	t.Helper()
	gotNames := names(got)
	if len(gotNames) != len(want) {
		t.Fatalf("order = %v, want %v", gotNames, want)
	}
	for i := range want {
		if gotNames[i] != want[i] {
			t.Fatalf("order = %v, want %v", gotNames, want)
		}
	}
}

func TestApply_CheapOrdersByEffectiveCost(t *testing.T) {
	// premium implements Quoted with an in-quota effective price of $0,
	// so cheap must rank it ahead of the cheap paid provider.
	premium := &fakeQuoted{fakeProvider: fakeProvider{name: "premium", cost: 0.005}, effective: 0}
	paid := &fakeProvider{name: "paid", cost: 0.001}
	free := &fakeProvider{name: "free", cost: 0}

	got, reason := Apply([]Costed{paid, premium, free}, Policy{}, nil, time.Now())
	assertOrder(t, got, "premium", "free", "paid")
	if reason != "policy=cheap: effective cost ascending" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestApply_CheapStableOnTies(t *testing.T) {
	a := &fakeProvider{name: "a", cost: 0}
	b := &fakeProvider{name: "b", cost: 0}
	got, _ := Apply([]Costed{b, a}, Policy{Strategy: StrategyCheap}, nil, time.Now())
	assertOrder(t, got, "b", "a")
}

func TestApply_PremiumUsesStaticListPriceDescending(t *testing.T) {
	// premium's quota makes its effective cost $0, but StrategyPremium
	// ranks on list price on purpose — it's still the premium choice.
	premium := &fakeQuoted{fakeProvider: fakeProvider{name: "premium", cost: 0.005}, effective: 0}
	paid := &fakeProvider{name: "paid", cost: 0.001}
	free := &fakeProvider{name: "free", cost: 0}

	got, reason := Apply([]Costed{free, paid, premium}, Policy{Strategy: StrategyPremium}, nil, time.Now())
	assertOrder(t, got, "premium", "paid", "free")
	if reason != "policy=premium: list price descending" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestApply_FastOrdersByObservedLatency(t *testing.T) {
	slow := &fakeProvider{name: "slow", cost: 0}
	quick := &fakeProvider{name: "quick", cost: 0.005}
	lat := func(provider string) (LatencyStat, bool) {
		switch provider {
		case "slow":
			return LatencyStat{AvgMS: 900, OKCalls: 10}, true
		case "quick":
			return LatencyStat{AvgMS: 120, OKCalls: 10}, true
		}
		return LatencyStat{}, false
	}
	got, reason := Apply([]Costed{slow, quick}, Policy{Strategy: StrategyFast}, lat, time.Now())
	assertOrder(t, got, "quick", "slow")
	if reason != "policy=fast: observed avg latency (local ledger)" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestApply_FastPutsNoDataProvidersAfterInCostOrder(t *testing.T) {
	measured := &fakeProvider{name: "measured", cost: 0.005}
	freeUnknown := &fakeProvider{name: "free-unknown", cost: 0}
	paidUnknown := &fakeProvider{name: "paid-unknown", cost: 0.001}
	lat := func(provider string) (LatencyStat, bool) {
		if provider == "measured" {
			return LatencyStat{AvgMS: 500, OKCalls: 5}, true
		}
		return LatencyStat{}, false
	}
	got, _ := Apply([]Costed{paidUnknown, measured, freeUnknown}, Policy{Strategy: StrategyFast}, lat, time.Now())
	assertOrder(t, got, "measured", "free-unknown", "paid-unknown")
}

func TestApply_FastIgnoresThinHistory(t *testing.T) {
	// Below minOKCalls a provider keeps cost order — one lucky fast call
	// must not jump it to the front.
	lucky := &fakeProvider{name: "lucky", cost: 0.005}
	free := &fakeProvider{name: "free", cost: 0}
	lat := func(provider string) (LatencyStat, bool) {
		if provider == "lucky" {
			return LatencyStat{AvgMS: 5, OKCalls: minOKCalls - 1}, true
		}
		return LatencyStat{}, false
	}
	got, reason := Apply([]Costed{lucky, free}, Policy{Strategy: StrategyFast}, lat, time.Now())
	assertOrder(t, got, "free", "lucky")
	if reason != "policy=fast: no latency history yet; cost order" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestApply_FastColdStartFallsBackToCost(t *testing.T) {
	paid := &fakeProvider{name: "paid", cost: 0.001}
	free := &fakeProvider{name: "free", cost: 0}

	got, reason := Apply([]Costed{paid, free}, Policy{Strategy: StrategyFast}, nil, time.Now())
	assertOrder(t, got, "free", "paid")
	if reason != "policy=fast: no latency data source; cost order" {
		t.Fatalf("reason = %q", reason)
	}

	noData := func(string) (LatencyStat, bool) { return LatencyStat{}, false }
	got, reason = Apply([]Costed{paid, free}, Policy{Strategy: StrategyFast}, noData, time.Now())
	assertOrder(t, got, "free", "paid")
	if reason != "policy=fast: no latency history yet; cost order" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestApply_ExplicitOrderIsPreferencePrefix(t *testing.T) {
	a := &fakeProvider{name: "a", cost: 0}
	b := &fakeProvider{name: "b", cost: 0.001}
	c := &fakeProvider{name: "c", cost: 0.005}

	// b listed first; unlisted a and c keep input order after it —
	// order is a prefix, not a whitelist.
	got, reason := Apply([]Costed{c, a, b}, Policy{Order: []string{"b"}}, nil, time.Now())
	assertOrder(t, got, "b", "c", "a")
	if reason != "policy: explicit provider order" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestApply_ExplicitOrderWinsOverStrategy(t *testing.T) {
	a := &fakeProvider{name: "a", cost: 0}
	c := &fakeProvider{name: "c", cost: 0.005}
	got, _ := Apply([]Costed{a, c}, Policy{Strategy: StrategyPremium, Order: []string{"a", "c"}}, nil, time.Now())
	assertOrder(t, got, "a", "c")
}

func TestApply_DenyFiltersUnderEveryPath(t *testing.T) {
	a := &fakeProvider{name: "a", cost: 0}
	b := &fakeProvider{name: "b", cost: 0.001}
	deny := map[string]bool{"b": true}

	got, _ := Apply([]Costed{a, b}, Policy{Deny: deny}, nil, time.Now())
	assertOrder(t, got, "a")

	got, _ = Apply([]Costed{a, b}, Policy{Strategy: StrategyPremium, Deny: deny}, nil, time.Now())
	assertOrder(t, got, "a")

	got, _ = Apply([]Costed{a, b}, Policy{Order: []string{"b", "a"}, Deny: deny}, nil, time.Now())
	assertOrder(t, got, "a")
}

func TestApply_DenyCanEmptyTheChain(t *testing.T) {
	a := &fakeProvider{name: "a", cost: 0}
	got, _ := Apply([]Costed{a}, Policy{Deny: map[string]bool{"a": true}}, nil, time.Now())
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", names(got))
	}
}

func TestApply_DoesNotMutateInput(t *testing.T) {
	a := &fakeProvider{name: "a", cost: 0.005}
	b := &fakeProvider{name: "b", cost: 0}
	in := []Costed{a, b}
	Apply(in, Policy{}, nil, time.Now())
	if in[0].Name() != "a" || in[1].Name() != "b" {
		t.Fatalf("input mutated: %v", names(in))
	}
}
