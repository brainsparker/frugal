// Package search defines the shared interface for routed web-search
// providers (SearXNG, Serper, You.com, …) and the data types the
// frugal__search MCP tool exposes to clients.
//
// Provider drivers (internal/provider/searxng, internal/provider/serper,
// internal/provider/youcom) implement Searcher. The MCP tool handler in
// internal/mcp/tools picks one at call-time using the routing rule
// documented on RouteCheapest.
//
// All numeric costs are USD per call. Item published_at timestamps, when
// available, are ISO-8601 strings — provider-specific parsing is out of
// scope for this package.
package search

import (
	"context"
	"time"
)

// Quoted is satisfied by providers that publish a recurring free-call
// budget (You.com's developer tier, etc.). The router prefers
// Quoted.EffectiveCostPerCall(now) over the static CostPerCall() — so a
// premium provider sorts ahead of a cheaper paid provider while its
// monthly quota holds, then falls back behind once the quota exhausts.
//
// Implementing Quoted is optional. Drivers that don't (SearXNG, Serper,
// also You.com when the operator hasn't configured a quota) keep their
// static CostPerCall() unchanged.
type Quoted interface {
	// EffectiveCostPerCall returns 0 while the provider is under its
	// monthly free quota, and CostPerCall() once exhausted. `now` is
	// passed in so tests can pin the clock — the driver also uses it to
	// compare against its last seen month for rollover.
	EffectiveCostPerCall(now time.Time) float64
}

// EffectiveCostOf returns s.EffectiveCostPerCall(now) if s implements
// Quoted, otherwise s.CostPerCall(). Used by OrderByCost so all routing
// decisions see the same effective price.
func EffectiveCostOf(s Searcher, now time.Time) float64 {
	if q, ok := s.(Quoted); ok {
		return q.EffectiveCostPerCall(now)
	}
	return s.CostPerCall()
}

// Searcher is the contract every search-provider driver satisfies. Drivers
// are independent of the MCP layer — they're plain Go clients that the
// tool handler dispatches to.
type Searcher interface {
	// Name reports the provider's identifier ("searxng", "serper", "youcom", …). Used
	// in tool-result metadata and in error messages. Must be stable across
	// releases — routing-policy order / deny lists and the `provider:`
	// tool argument pin providers by these names.
	Name() string
	// CostPerCall is the published per-call price the operator agreed to.
	// The auto-router picks the lowest CostPerCall among configured
	// providers (subject to call-time overrides). Zero means "free or
	// unmetered" — treated as the absolute cheapest.
	CostPerCall() float64
	// Search runs one search request. Returns the result list and the cost
	// charged for that call. Implementations must respect ctx for
	// cancellation and request timeouts.
	Search(ctx context.Context, q Query) (Results, error)
}

// Query is the input to one search call. Fields are kept small on purpose
// — provider-specific options (region, freshness, domain filters) can be
// added later through a Provider-Options map without breaking existing
// callers.
type Query struct {
	// Text is the search query (required).
	Text string
	// MaxResults caps the result list. Drivers may clamp to provider-side
	// maxima (Serper and You.com both top out around 20 today). Zero is
	// interpreted as "driver default" (typically 5).
	MaxResults int
	// Freshness is an optional time-window hint ("day", "week", "month").
	// Drivers map this to whatever the provider exposes; "" means "any".
	Freshness string
}

// Results carries the items a Search call returned plus the cost that call
// incurred. CostUSD is what the operator will be billed for this single
// call by the provider (not the published per-call price, which can differ
// in volume-discount tiers).
//
// Warnings surfaces degraded-service notes the agent should see alongside
// the items — today, a driver that had to ignore the freshness window
// because its provider has no time filter (Marginalia, Wikipedia). A
// warning instead of an error keeps the zero-key chain serving results;
// telling the agent the filter was dropped is what keeps that honest.
type Results struct {
	Items    []Item
	CostUSD  float64
	Warnings []string
}

// Item is one search hit. Fields mirror the union of what the configured
// providers reliably return; provider-specific extras (favicon, breadcrumb,
// type) drop on the floor today and can be added when an eval shows they
// materially change downstream answer quality.
type Item struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet"`
	PublishedAt string `json:"published_at,omitempty"`
}

// RouteCheapest picks the Searcher with the lowest CostPerCall. Ties broken
// by input order so callers can deterministically prefer a provider by
// listing it first.
//
// Returns nil when searchers is empty. Callers handle nil as "no search
// providers configured" and emit a useful error to the MCP client.
func RouteCheapest(searchers []Searcher) Searcher {
	if len(searchers) == 0 {
		return nil
	}
	best := searchers[0]
	for _, s := range searchers[1:] {
		if s.CostPerCall() < best.CostPerCall() {
			best = s
		}
	}
	return best
}

// Find returns the searcher whose Name matches name, or nil if none does.
// Used when an MCP tool argument pins a specific provider.
func Find(searchers []Searcher, name string) Searcher {
	for _, s := range searchers {
		if s.Name() == name {
			return s
		}
	}
	return nil
}
