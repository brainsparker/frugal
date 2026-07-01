// Metrics — per-provider call counts, error counts, latency, and cost.
//
// Kept in the obs package so the existing logger plumbing is the natural
// home. One global *Metrics per process is fine: drivers ARE the entire
// supply side, and the registry uses a single instance.
//
// Snapshot() returns a copy safe to log or render. WritePrometheus writes
// a Prometheus text-format dump for the optional /metrics HTTP endpoint —
// implemented inline so we don't add a Prometheus client dep just for
// four counters.
//
// Thread-safety: atomic counters for hot-path increments, RWMutex for the
// per-provider map (which only grows during startup as searchers register).

package obs

import (
	"fmt"
	"io"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics is the process-wide aggregator. Construct one via NewMetrics
// and share by pointer.
type Metrics struct {
	mu        sync.RWMutex
	providers map[string]*ProviderStats
	sink      func(tool, provider string, latency time.Duration, costUSD float64, won bool, err error)
}

// ProviderStats holds the running totals for one provider. Fields are
// atomically updated on the hot path; Snapshot reads them with
// LoadX/LoadInt64.
//
// Tool tags the capability the provider belongs to ("search", "extract",
// "browse", …). Set once at registration via EnsureProvider so the
// Prometheus output can group with a label rather than a metric-name
// infix.
//
// MonthlyCalls is a separate counter that resets on calendar-month
// rollover (UTC). It's the input the Quoted interface uses to decide
// whether a provider is still inside its free quota.
type ProviderStats struct {
	Tool       string // capability label (set at EnsureProvider time)
	Calls      atomic.Int64
	Errors     atomic.Int64
	LatencySum atomic.Int64 // milliseconds, summed across all calls (avg = sum/calls)
	CostMicro  atomic.Int64 // cost in micro-USD (×1e6), summed

	// MonthlyCalls resets at the start of every UTC calendar month. Guarded
	// by monthMu rather than atomic-ops because rollover is a
	// compare-and-swap of two fields together (counter + epoch).
	monthMu      sync.Mutex
	MonthlyCalls int64
	monthYear    int        // year of the last roll
	monthMonth   time.Month // month of the last roll
}

// NewMetrics constructs an empty Metrics.
func NewMetrics() *Metrics {
	return &Metrics{providers: make(map[string]*ProviderStats)}
}

// EnsureProvider registers a provider so its row exists in snapshots
// even before any calls, and tags it with the capability it serves
// ("search", "extract", "browse"). Called once per registered driver
// at startup. If the provider already exists and tool is non-empty,
// the tool label is updated (idempotent).
func (m *Metrics) EnsureProvider(name, tool string) {
	m.mu.Lock()
	ps, ok := m.providers[name]
	if !ok {
		ps = &ProviderStats{}
		m.providers[name] = ps
	}
	if tool != "" {
		ps.Tool = tool
	}
	m.mu.Unlock()
}

// SetSink registers a per-call listener invoked after every RecordCall
// with the provider's capability label attached. One sink per process,
// set once at startup before serving begins — the usage ledger is the
// intended consumer. The sink runs synchronously on the request path;
// keep it cheap and never let it panic.
func (m *Metrics) SetSink(fn func(tool, provider string, latency time.Duration, costUSD float64, won bool, err error)) {
	m.mu.Lock()
	m.sink = fn
	m.mu.Unlock()
}

// RecordCall increments the per-provider counters. costUSD is the amount
// charged for this call (0 for free providers). won marks the attempt
// that produced the result the caller returned (passed through to the
// sink; the counters don't use it). err non-nil increments Errors;
// latency is still recorded so error-path latency stays visible.
func (m *Metrics) RecordCall(provider string, latency time.Duration, costUSD float64, won bool, err error) {
	m.mu.RLock()
	ps, ok := m.providers[provider]
	sink := m.sink
	m.mu.RUnlock()
	if !ok {
		// First time we've seen this provider; lazy-create. Cheaper than
		// requiring callers to remember to EnsureProvider for ad-hoc cases.
		m.mu.Lock()
		ps, ok = m.providers[provider]
		if !ok {
			ps = &ProviderStats{}
			m.providers[provider] = ps
		}
		m.mu.Unlock()
	}
	ps.Calls.Add(1)
	if err != nil {
		ps.Errors.Add(1)
	}
	ps.LatencySum.Add(latency.Milliseconds())
	if costUSD > 0 {
		// Round, don't truncate: 0.005*1e6 is 4999.999… in float64, and
		// truncation would undercount every such call by a micro-USD.
		ps.CostMicro.Add(int64(math.Round(costUSD * 1e6)))
	}
	ps.bumpMonthly(time.Now().UTC())
	if sink != nil {
		// ps.Tool is written once at EnsureProvider (startup) and stable
		// by the time calls flow; lazy-created providers report "".
		sink(ps.Tool, provider, latency, costUSD, won, err)
	}
}

// bumpMonthly increments MonthlyCalls and rolls the epoch when the
// calendar month has changed since the last bump. UTC keeps rollover
// timing predictable regardless of operator timezone.
func (p *ProviderStats) bumpMonthly(now time.Time) {
	p.monthMu.Lock()
	y, mo := now.Year(), now.Month()
	if p.monthYear != y || p.monthMonth != mo {
		p.MonthlyCalls = 0
		p.monthYear = y
		p.monthMonth = mo
	}
	p.MonthlyCalls++
	p.monthMu.Unlock()
}

// MonthlyCalls reports the number of calls recorded for `provider` in the
// current UTC calendar month. Returns 0 for unknown providers and rolls
// the epoch lazily if the month has changed since the last write (so a
// reader-only test on the 1st of a new month sees the correct value).
func (m *Metrics) MonthlyCalls(provider string) int64 {
	m.mu.RLock()
	ps, ok := m.providers[provider]
	m.mu.RUnlock()
	if !ok {
		return 0
	}
	ps.monthMu.Lock()
	defer ps.monthMu.Unlock()
	now := time.Now().UTC()
	if ps.monthYear != now.Year() || ps.monthMonth != now.Month() {
		// The month rolled while no one wrote. Return 0 without resetting
		// the epoch so the next RecordCall handles the transition cleanly.
		return 0
	}
	return ps.MonthlyCalls
}

// Snapshot is a point-in-time view safe to render or log. Sorted by
// provider name for stable output.
type Snapshot struct {
	Providers []ProviderSnapshot
	TotalCost float64
	Time      time.Time
}

// ProviderSnapshot is one row in a Snapshot.
type ProviderSnapshot struct {
	Name         string
	Tool         string // capability ("search", "extract", "browse")
	Calls        int64
	Errors       int64
	CostUSD      float64
	AvgLatencyMS int64
}

// Snapshot returns the current totals as a copy.
func (m *Metrics) Snapshot() Snapshot {
	m.mu.RLock()
	names := make([]string, 0, len(m.providers))
	for n := range m.providers {
		names = append(names, n)
	}
	m.mu.RUnlock()
	sort.Strings(names)

	out := Snapshot{Time: time.Now().UTC()}
	for _, n := range names {
		m.mu.RLock()
		ps := m.providers[n]
		m.mu.RUnlock()
		calls := ps.Calls.Load()
		var avg int64
		if calls > 0 {
			avg = ps.LatencySum.Load() / calls
		}
		cost := float64(ps.CostMicro.Load()) / 1e6
		out.Providers = append(out.Providers, ProviderSnapshot{
			Name:         n,
			Tool:         ps.Tool,
			Calls:        calls,
			Errors:       ps.Errors.Load(),
			CostUSD:      cost,
			AvgLatencyMS: avg,
		})
		out.TotalCost += cost
	}
	return out
}

// HasActivity reports whether any provider has been called. Used by the
// periodic logger to skip silent intervals.
func (m *Metrics) HasActivity() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, ps := range m.providers {
		if ps.Calls.Load() > 0 {
			return true
		}
	}
	return false
}

// WritePrometheus writes a Prometheus text-format dump to w. Inlined so
// the binary stays free of a Prometheus client dep — four counters
// don't justify pulling in github.com/prometheus/client_golang.
//
// Schema (single family per metric type, tool + provider as labels —
// scales as new capabilities land):
//
//	frugal_calls_total{tool="...",provider="..."}      counter
//	frugal_errors_total{tool="...",provider="..."}     counter
//	frugal_cost_usd_total{tool="...",provider="..."}   counter
//	frugal_latency_ms_avg{tool="...",provider="..."}   gauge
//
// Providers registered without a tool label appear with tool="".
func (m *Metrics) WritePrometheus(w io.Writer) error {
	snap := m.Snapshot()
	if _, err := fmt.Fprintln(w, "# HELP frugal_calls_total Total tool calls per (tool, provider)."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE frugal_calls_total counter"); err != nil {
		return err
	}
	for _, p := range snap.Providers {
		if _, err := fmt.Fprintf(w, "frugal_calls_total{tool=%q,provider=%q} %d\n", p.Tool, p.Name, p.Calls); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "# HELP frugal_errors_total Total tool errors per (tool, provider)."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE frugal_errors_total counter"); err != nil {
		return err
	}
	for _, p := range snap.Providers {
		if _, err := fmt.Fprintf(w, "frugal_errors_total{tool=%q,provider=%q} %d\n", p.Tool, p.Name, p.Errors); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "# HELP frugal_cost_usd_total Cumulative USD billed per (tool, provider)."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE frugal_cost_usd_total counter"); err != nil {
		return err
	}
	for _, p := range snap.Providers {
		if _, err := fmt.Fprintf(w, "frugal_cost_usd_total{tool=%q,provider=%q} %.6f\n", p.Tool, p.Name, p.CostUSD); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "# HELP frugal_latency_ms_avg Average end-to-end latency in milliseconds per (tool, provider)."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE frugal_latency_ms_avg gauge"); err != nil {
		return err
	}
	for _, p := range snap.Providers {
		if _, err := fmt.Fprintf(w, "frugal_latency_ms_avg{tool=%q,provider=%q} %d\n", p.Tool, p.Name, p.AvgLatencyMS); err != nil {
			return err
		}
	}
	return nil
}
