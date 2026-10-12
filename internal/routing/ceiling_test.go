package routing

import (
	"strings"
	"testing"
	"time"
)

func f64(v float64) *float64 { return &v }

func TestCeilingFrom(t *testing.T) {
	c, err := CeilingFrom(nil)
	if err != nil || c.Set {
		t.Fatalf("nil input must mean no ceiling, got %+v err=%v", c, err)
	}
	c, err = CeilingFrom(f64(0))
	if err != nil || !c.Set || c.MaxUSD != 0 {
		t.Fatalf("zero must be a set ceiling of $0, got %+v err=%v", c, err)
	}
	c, err = CeilingFrom(f64(0.002))
	if err != nil || !c.Set || c.MaxUSD != 0.002 {
		t.Fatalf("positive ceiling mis-parsed: %+v err=%v", c, err)
	}
	if _, err := CeilingFrom(f64(-0.001)); err == nil {
		t.Fatal("negative ceiling must error")
	}
}

func TestApplyCeiling_UnsetIsNoOp(t *testing.T) {
	now := time.Now()
	in := []*fakeProvider{{name: "free", cost: 0}, {name: "paid", cost: 0.005}}
	out, reason := ApplyCeiling(in, NoCeiling, "policy=cheap", now)
	if len(out) != 2 || reason != "policy=cheap" {
		t.Fatalf("unset ceiling must leave chain and reason alone, got %d items, reason %q", len(out), reason)
	}
}

func TestApplyCeiling_FiltersAboveCeilingAndNotesTrace(t *testing.T) {
	now := time.Now()
	in := []*fakeProvider{{name: "wikipedia", cost: 0}, {name: "serper", cost: 0.001}, {name: "youcom", cost: 0.005}}
	out, reason := ApplyCeiling(in, Ceiling{MaxUSD: 0.001, Set: true}, "policy=cheap", now)
	if len(out) != 2 || out[0].Name() != "wikipedia" || out[1].Name() != "serper" {
		t.Fatalf("expected [wikipedia serper], got %v", names(out))
	}
	if !strings.Contains(reason, "ceiling: skipped youcom") || !strings.Contains(reason, "$0.0050") || !strings.Contains(reason, "$0.0010") {
		t.Errorf("reason must name the skipped provider and both prices, got %q", reason)
	}
}

func TestApplyCeiling_ZeroKeepsFreeRungsOnly(t *testing.T) {
	now := time.Now()
	in := []*fakeProvider{{name: "serper", cost: 0.001}, {name: "marginalia", cost: 0}}
	out, _ := ApplyCeiling(in, Ceiling{MaxUSD: 0, Set: true}, "", now)
	if len(out) != 1 || out[0].Name() != "marginalia" {
		t.Fatalf("zero ceiling must keep only free providers, got %v", names(out))
	}
}

func TestApplyCeiling_HonorsQuotaAwareEffectiveCost(t *testing.T) {
	// A paid provider with free quota remaining is effectively free this
	// month and must pass a zero ceiling; one whose quota is exhausted
	// must not.
	now := time.Now()
	inQuota := &fakeQuoted{fakeProvider: fakeProvider{name: "firecrawl", cost: 0.001}, effective: 0}
	exhausted := &fakeQuoted{fakeProvider: fakeProvider{name: "browserless", cost: 0.002}, effective: 0.002}
	out, _ := ApplyCeiling([]Costed{inQuota, exhausted}, Ceiling{MaxUSD: 0, Set: true}, "", now)
	if len(out) != 1 || out[0].Name() != "firecrawl" {
		t.Fatalf("expected in-quota provider only, got %d items", len(out))
	}
}

func TestCeilingAllows_PinnedProviderMessage(t *testing.T) {
	c := Ceiling{MaxUSD: 0.001, Set: true}
	ok, why := c.Allows(&fakeProvider{name: "youcom", cost: 0.005}, time.Now())
	if ok {
		t.Fatal("youcom at $0.005 must not fit under a $0.001 ceiling")
	}
	if !strings.Contains(why, "youcom") || !strings.Contains(why, "max_cost_usd") {
		t.Errorf("reason = %q", why)
	}
	if ok, _ := c.Allows(&fakeProvider{name: "serper", cost: 0.001}, time.Now()); !ok {
		t.Error("a provider priced exactly at the ceiling must be allowed")
	}
}

func TestCeilingEmptyError_NamesCheapestOption(t *testing.T) {
	now := time.Now()
	configured := []*fakeProvider{{name: "youcom", cost: 0.005}, {name: "serper", cost: 0.001}}
	err := CeilingEmptyError("search", Ceiling{MaxUSD: 0.0005, Set: true}, configured, now)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"search", "$0.0005", "serper", "$0.0010"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
	if err := CeilingEmptyError("browse", Ceiling{Set: true}, []*fakeProvider{}, now); err == nil {
		t.Fatal("empty configured chain must still produce an error")
	}
}
