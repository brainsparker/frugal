// Routing policies: the operator- or caller-declared rule for how a
// capability's provider chain is ordered before the fallback walk. The
// zero-value Policy reproduces the historical default exactly — effective
// cost ascending, nobody denied — so callers that pass Policy{} get the
// same routing the product has always done.
package routing

import (
	"sort"
	"time"
)

// Strategy selects the ordering rule Apply uses when no explicit Order
// is given. The zero value is StrategyCheap — the default the product
// launched with.
type Strategy int

const (
	// StrategyCheap orders by effective cost ascending (quota-aware
	// when the provider implements Quoted).
	StrategyCheap Strategy = iota
	// StrategyFast orders by this machine's own observed OK-attempt
	// latency (from the local usage ledger), cheapest-first among
	// providers with no history. Not a live probe.
	StrategyFast
	// StrategyPremium orders by static list price descending. Static on
	// purpose: a premium provider whose free quota makes its effective
	// cost $0 is still the premium choice.
	StrategyPremium
)

// String returns the config-file spelling of the strategy.
func (s Strategy) String() string {
	switch s {
	case StrategyFast:
		return "fast"
	case StrategyPremium:
		return "premium"
	default:
		return "cheap"
	}
}

// Policy is one capability's routing rule. Order, when non-empty, wins
// over Strategy: listed providers come first in listed order, everyone
// else follows in input order — a preference prefix, not a whitelist,
// so the fallback chain still reaches every configured provider. Deny
// removes providers entirely, under every strategy and for pinned calls
// too ("never call" means never).
type Policy struct {
	Strategy Strategy
	Order    []string
	Deny     map[string]bool
}

// LatencyStat is one provider's aggregate from the local ledger: mean
// latency across OK attempts and how many OK attempts back it.
type LatencyStat struct {
	AvgMS   float64
	OKCalls int64
}

// LatencyLookup resolves a provider name to its observed latency. The
// bool reports whether any history exists. A nil LatencyLookup means "no
// data source" — StrategyFast then degrades to cost order.
type LatencyLookup func(provider string) (LatencyStat, bool)

// Costed is the slice of the capability provider interfaces
// (search.Searcher, extract.Extractor, browse.Browser) that routing
// policies need. All three satisfy it already.
type Costed interface {
	Name() string
	CostPerCall() float64
}

// Quoted mirrors the per-capability Quoted interfaces structurally so
// Apply can read quota-aware effective cost without importing them.
type Quoted interface {
	EffectiveCostPerCall(now time.Time) float64
}

// minOKCalls is how much OK-attempt history a provider needs before
// StrategyFast will rank it on latency. Below this it keeps cost order —
// one lucky 90ms call shouldn't jump a provider to the front.
const minOKCalls = 3

// EffectiveCostFor returns the quota-aware effective cost when the
// provider implements Quoted, else its static list price. Same rule the
// per-capability OrderByCost helpers apply.
func EffectiveCostFor(c Costed, now time.Time) float64 {
	if q, ok := c.(Quoted); ok {
		return q.EffectiveCostPerCall(now)
	}
	return c.CostPerCall()
}

// Apply filters items through p.Deny and orders the rest per the policy.
// Returns a fresh slice (input is never mutated) and a one-line reason
// suitable for logs and routing traces. All sorts are stable, so ties
// keep the caller's input order — the canonical registration order — as
// the deterministic tie-break, exactly like OrderByCost. The result may
// be empty when Deny removes everything; callers own that error.
func Apply[T Costed](items []T, p Policy, lat LatencyLookup, now time.Time) ([]T, string) {
	out := make([]T, 0, len(items))
	for _, it := range items {
		if p.Deny[it.Name()] {
			continue
		}
		out = append(out, it)
	}

	if len(p.Order) > 0 {
		rank := make(map[string]int, len(p.Order))
		for i, name := range p.Order {
			rank[name] = i
		}
		// Listed providers first in listed order; unlisted keep input
		// order after them.
		sort.SliceStable(out, func(i, j int) bool {
			ri, iListed := rank[out[i].Name()]
			rj, jListed := rank[out[j].Name()]
			switch {
			case iListed && jListed:
				return ri < rj
			case iListed != jListed:
				return iListed
			default:
				return false
			}
		})
		return out, "policy: explicit provider order"
	}

	switch p.Strategy {
	case StrategyPremium:
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].CostPerCall() > out[j].CostPerCall()
		})
		return out, "policy=premium: list price descending"
	case StrategyFast:
		if lat == nil {
			sortByEffectiveCost(out, now)
			return out, "policy=fast: no latency data source; cost order"
		}
		type keyed struct {
			avg     float64
			hasData bool
		}
		keys := make(map[string]keyed, len(out))
		any := false
		for _, it := range out {
			st, ok := lat(it.Name())
			k := keyed{avg: st.AvgMS, hasData: ok && st.OKCalls >= minOKCalls}
			if k.hasData {
				any = true
			}
			keys[it.Name()] = k
		}
		if !any {
			sortByEffectiveCost(out, now)
			return out, "policy=fast: no latency history yet; cost order"
		}
		// Providers with history rank by observed latency and come
		// first; the rest follow in cost order so the fallback chain
		// still reaches them.
		sortByEffectiveCost(out, now)
		sort.SliceStable(out, func(i, j int) bool {
			ki, kj := keys[out[i].Name()], keys[out[j].Name()]
			switch {
			case ki.hasData && kj.hasData:
				return ki.avg < kj.avg
			default:
				return ki.hasData && !kj.hasData
			}
		})
		return out, "policy=fast: observed avg latency (local ledger)"
	default:
		sortByEffectiveCost(out, now)
		return out, "policy=cheap: effective cost ascending"
	}
}

func sortByEffectiveCost[T Costed](items []T, now time.Time) {
	sort.SliceStable(items, func(i, j int) bool {
		return EffectiveCostFor(items[i], now) < EffectiveCostFor(items[j], now)
	})
}
