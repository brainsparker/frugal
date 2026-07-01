package obs

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRecordCall_AggregatesPerProvider(t *testing.T) {
	m := NewMetrics()
	m.EnsureProvider("youcom", "search")
	m.RecordCall("youcom", 120*time.Millisecond, 0.008, true, nil)
	m.RecordCall("youcom", 200*time.Millisecond, 0.008, true, nil)
	m.RecordCall("youcom", 300*time.Millisecond, 0, false, errors.New("blip"))
	m.RecordCall("searxng", 30*time.Millisecond, 0, true, nil)

	snap := m.Snapshot()
	if len(snap.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(snap.Providers))
	}
	// Sorted by name.
	if snap.Providers[0].Name != "searxng" || snap.Providers[1].Name != "youcom" {
		t.Errorf("provider order: %v", []string{snap.Providers[0].Name, snap.Providers[1].Name})
	}
	you := snap.Providers[1]
	if you.Calls != 3 {
		t.Errorf("youcom.Calls = %d, want 3", you.Calls)
	}
	if you.Errors != 1 {
		t.Errorf("youcom.Errors = %d, want 1", you.Errors)
	}
	if you.CostUSD != 0.016 {
		// Two successful 0.008 calls = 0.016; the error call's cost=0.
		t.Errorf("youcom.CostUSD = %v, want 0.016", you.CostUSD)
	}
	if you.AvgLatencyMS != (120+200+300)/3 {
		t.Errorf("youcom.AvgLatencyMS = %d, want %d", you.AvgLatencyMS, (120+200+300)/3)
	}
	if snap.TotalCost != 0.016 {
		t.Errorf("TotalCost = %v, want 0.016", snap.TotalCost)
	}
}

func TestRecordCall_CostRoundsToNearestMicroUSD(t *testing.T) {
	// 0.005*1e6 is 4999.999… in float64; truncation would record 4999
	// micro-USD per call and systematically undercount. Rounding must
	// keep 1000 such calls summing to exactly $5.
	m := NewMetrics()
	for i := 0; i < 1000; i++ {
		m.RecordCall("serper", time.Millisecond, 0.005, true, nil)
	}
	snap := m.Snapshot()
	if got := snap.Providers[0].CostUSD; got != 5.0 {
		t.Errorf("CostUSD after 1000×$0.005 = %v, want 5.0 (truncation undercounts)", got)
	}
}

func TestHasActivity(t *testing.T) {
	m := NewMetrics()
	m.EnsureProvider("youcom", "search")
	if m.HasActivity() {
		t.Errorf("HasActivity should be false before any call")
	}
	m.RecordCall("youcom", time.Millisecond, 0.001, true, nil)
	if !m.HasActivity() {
		t.Errorf("HasActivity should be true after a call")
	}
}

func TestMonthlyCalls_IncrementsAndIsolatesProviders(t *testing.T) {
	m := NewMetrics()
	m.RecordCall("youcom", time.Millisecond, 0.008, true, nil)
	m.RecordCall("youcom", time.Millisecond, 0.008, true, nil)
	m.RecordCall("serper", time.Millisecond, 0.001, true, nil)
	if got := m.MonthlyCalls("youcom"); got != 2 {
		t.Errorf("youcom MonthlyCalls = %d, want 2", got)
	}
	if got := m.MonthlyCalls("serper"); got != 1 {
		t.Errorf("serper MonthlyCalls = %d, want 1", got)
	}
	if got := m.MonthlyCalls("unknown"); got != 0 {
		t.Errorf("unknown MonthlyCalls = %d, want 0", got)
	}
}

func TestMonthlyCalls_ResetsOnMonthRollover(t *testing.T) {
	// We can't easily warp wall-clock time, but ProviderStats.bumpMonthly
	// accepts a clock argument so the rollover code is exercise-able
	// directly. Verify the reset behavior by driving the internal helper.
	ps := &ProviderStats{}
	prev := time.Date(2026, time.March, 31, 23, 59, 0, 0, time.UTC)
	ps.bumpMonthly(prev)
	ps.bumpMonthly(prev)
	if ps.MonthlyCalls != 2 {
		t.Fatalf("MonthlyCalls before rollover = %d, want 2", ps.MonthlyCalls)
	}
	next := time.Date(2026, time.April, 1, 0, 0, 1, 0, time.UTC)
	ps.bumpMonthly(next)
	if ps.MonthlyCalls != 1 {
		t.Errorf("MonthlyCalls after rollover = %d, want 1 (reset + this call)", ps.MonthlyCalls)
	}
	// Lifetime Calls counter is unaffected by rollover. (We didn't add to
	// it here — that's the RecordCall path. Just confirm the rollover
	// didn't leak into it.)
	if got := ps.Calls.Load(); got != 0 {
		t.Errorf("lifetime Calls touched by rollover; got %d", got)
	}
}

func TestMonthlyCalls_ConcurrentSafe(t *testing.T) {
	m := NewMetrics()
	const goroutines = 16
	const perGoroutine = 100
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				m.RecordCall("youcom", time.Microsecond, 0.001, true, nil)
			}
		}()
	}
	wg.Wait()
	if got := m.MonthlyCalls("youcom"); got != goroutines*perGoroutine {
		t.Errorf("MonthlyCalls = %d, want %d (no lost updates under contention)", got, goroutines*perGoroutine)
	}
}

func TestWritePrometheus(t *testing.T) {
	m := NewMetrics()
	// Register providers with their tools so the Prometheus output gets
	// the right label. Tool label survives lazy-create (unregistered
	// providers get tool="").
	m.EnsureProvider("youcom", "search")
	m.EnsureProvider("searxng", "search")
	m.EnsureProvider("firecrawl", "extract")
	m.RecordCall("youcom", 100*time.Millisecond, 0.008, true, nil)
	m.RecordCall("youcom", 200*time.Millisecond, 0, false, errors.New("x"))
	m.RecordCall("searxng", 20*time.Millisecond, 0, true, nil)
	m.RecordCall("firecrawl", 400*time.Millisecond, 0.001, true, nil)

	var buf bytes.Buffer
	if err := m.WritePrometheus(&buf); err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
	out := buf.String()

	mustContain := []string{
		// Single family per metric type, tool+provider as labels.
		`# TYPE frugal_calls_total counter`,
		`frugal_calls_total{tool="search",provider="youcom"} 2`,
		`frugal_calls_total{tool="search",provider="searxng"} 1`,
		`frugal_calls_total{tool="extract",provider="firecrawl"} 1`,
		`# TYPE frugal_errors_total counter`,
		`frugal_errors_total{tool="search",provider="youcom"} 1`,
		`# TYPE frugal_cost_usd_total counter`,
		`frugal_cost_usd_total{tool="search",provider="youcom"} 0.008000`,
		`frugal_cost_usd_total{tool="search",provider="searxng"} 0.000000`,
		`frugal_cost_usd_total{tool="extract",provider="firecrawl"} 0.001000`,
		`# TYPE frugal_latency_ms_avg gauge`,
		`frugal_latency_ms_avg{tool="search",provider="youcom"} 150`,
		`frugal_latency_ms_avg{tool="extract",provider="firecrawl"} 400`,
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Errorf("Prometheus output missing %q. Full output:\n%s", want, out)
		}
	}
	// Old metric names must be gone — no frugal_search_* infix family.
	if strings.Contains(out, "frugal_search_calls_total") {
		t.Errorf("Old metric name frugal_search_calls_total should be removed; got:\n%s", out)
	}
}

func TestEnsureProvider_TagsTool(t *testing.T) {
	m := NewMetrics()
	m.EnsureProvider("foo", "search")
	m.RecordCall("foo", time.Millisecond, 0, true, nil)
	snap := m.Snapshot()
	if len(snap.Providers) != 1 || snap.Providers[0].Tool != "search" {
		t.Errorf("expected tool=search; got %+v", snap.Providers)
	}
	// Re-EnsureProvider with a new tool is idempotent and updates.
	m.EnsureProvider("foo", "extract")
	snap = m.Snapshot()
	if snap.Providers[0].Tool != "extract" {
		t.Errorf("expected tool updated to extract; got %q", snap.Providers[0].Tool)
	}
}

func TestRecordCall_UnregisteredProviderHasEmptyTool(t *testing.T) {
	// Lazy-create path: RecordCall without EnsureProvider. Tool is "".
	m := NewMetrics()
	m.RecordCall("orphan", time.Millisecond, 0, true, nil)
	snap := m.Snapshot()
	if len(snap.Providers) != 1 || snap.Providers[0].Tool != "" {
		t.Errorf("expected unregistered provider with empty tool; got %+v", snap.Providers)
	}
}
