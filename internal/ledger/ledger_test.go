package ledger

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)

func newTestWriter(t *testing.T, rack map[string]float64) (*Writer, string) {
	t.Helper()
	dir := t.TempDir()
	w := NewWriter(dir, rack, nil)
	w.now = func() time.Time { return fixedNow }
	return w, dir
}

func TestRoundTrip(t *testing.T) {
	w, dir := newTestWriter(t, map[string]float64{"search": 0.005, "extract": 0.001})

	// A zero-hit success the router walked past: ok but not won — no
	// rack credit.
	w.Record("search", "marginalia", 500*time.Millisecond, 0, false, true)
	w.Record("search", "serper", 300*time.Millisecond, 0.001, true, true)
	w.Record("search", "serper", 250*time.Millisecond, 0.001, true, true)
	w.Record("extract", "goreadability", 80*time.Millisecond, 0, true, true)
	// A failed attempt: paid nothing, must not count as rack savings.
	w.Record("search", "youcom", 900*time.Millisecond, 0, false, false)

	st, err := Load(dir, false, fixedNow)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if st.Calls != 5 || st.Errors != 1 {
		t.Errorf("calls=%d errors=%d, want 5/1", st.Calls, st.Errors)
	}
	if got, want := st.PaidUSD, 0.002; !close(got, want) {
		t.Errorf("PaidUSD = %v, want %v", got, want)
	}
	// Rack: 2 winning searches à 0.005 + 1 winning extract à 0.001. The
	// zero-hit marginalia attempt and the failed youcom row contribute
	// nothing.
	if got, want := st.RackUSD, 0.011; !close(got, want) {
		t.Errorf("RackUSD = %v, want %v", got, want)
	}
	if got, want := st.SavedUSD(), 0.009; !close(got, want) {
		t.Errorf("SavedUSD = %v, want %v", got, want)
	}
	// Canonical row order: search rows before extract, providers
	// alphabetical within a tool.
	order := make([]string, 0, len(st.Rows))
	for _, r := range st.Rows {
		order = append(order, r.Tool+"/"+r.Provider)
	}
	want := []string{"search/marginalia", "search/serper", "search/youcom", "extract/goreadability"}
	if len(order) != len(want) {
		t.Fatalf("rows = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("rows = %v, want %v", order, want)
		}
	}
}

func TestConcurrentAppends(t *testing.T) {
	w, dir := newTestWriter(t, map[string]float64{"search": 0.005})
	const goroutines, perG = 8, 50
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				w.Record("search", "marginalia", time.Millisecond, 0, true, true)
			}
		}()
	}
	wg.Wait()
	st, err := Load(dir, false, fixedNow)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if st.Calls != goroutines*perG {
		t.Errorf("calls = %d, want %d (lost or torn appends)", st.Calls, goroutines*perG)
	}
}

func TestLoadSkipsGarbageLines(t *testing.T) {
	w, dir := newTestWriter(t, nil)
	w.Record("search", "marginalia", time.Millisecond, 0, true, true)
	path := filepath.Join(dir, MonthFile(fixedNow))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{torn line\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	w.Record("search", "marginalia", time.Millisecond, 0, true, true)

	st, err := Load(dir, false, fixedNow)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if st.Calls != 2 {
		t.Errorf("calls = %d, want 2 (garbage line must be skipped, not fatal)", st.Calls)
	}
}

func TestLoadSkipsOverlongLines(t *testing.T) {
	// One foreign or torn >1 MiB line must cost one record, never the
	// receipt — bufio.Scanner's ErrTooLong used to abort the whole load.
	w, dir := newTestWriter(t, nil)
	w.Record("search", "marginalia", time.Millisecond, 0, true, true)
	path := filepath.Join(dir, MonthFile(fixedNow))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	huge := make([]byte, 2<<20) // 2 MiB, no newline until the end
	for i := range huge {
		huge[i] = 'x'
	}
	huge[len(huge)-1] = '\n'
	if _, err := f.Write(huge); err != nil {
		t.Fatal(err)
	}
	f.Close()
	w.Record("search", "marginalia", time.Millisecond, 0, true, true)

	st, err := Load(dir, false, fixedNow)
	if err != nil {
		t.Fatalf("Load must not fail on an overlong line: %v", err)
	}
	if st.Calls != 2 {
		t.Errorf("calls = %d, want 2 (overlong line skipped, records kept)", st.Calls)
	}
}

func TestLoadMonthScoping(t *testing.T) {
	w, dir := newTestWriter(t, nil)
	w.Record("search", "marginalia", time.Millisecond, 0, true, true) // July file

	june := time.Date(2026, time.June, 15, 0, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return june }
	w.Record("search", "marginalia", time.Millisecond, 0, true, true) // June file

	st, err := Load(dir, false, fixedNow)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if st.Calls != 1 || len(st.Months) != 1 || st.Months[0] != "2026-07" {
		t.Errorf("month scope: calls=%d months=%v, want 1 call in [2026-07]", st.Calls, st.Months)
	}

	all, err := Load(dir, true, fixedNow)
	if err != nil {
		t.Fatalf("Load all: %v", err)
	}
	if all.Calls != 2 || len(all.Months) != 2 {
		t.Errorf("all: calls=%d months=%v, want 2 calls across 2 months", all.Calls, all.Months)
	}
}

func TestLoadMissingDirIsEmptyNotError(t *testing.T) {
	st, err := Load(filepath.Join(t.TempDir(), "never-created"), true, fixedNow)
	if err != nil {
		t.Fatalf("missing dir must not error: %v", err)
	}
	if st.Calls != 0 || len(st.Rows) != 0 {
		t.Errorf("expected empty stats, got %+v", st)
	}
}

func TestSavedUSDClampsAtZero(t *testing.T) {
	s := Stats{PaidUSD: 0.5, RackUSD: 0.1}
	if s.SavedUSD() != 0 {
		t.Errorf("SavedUSD must clamp at zero; got %v", s.SavedUSD())
	}
}

func TestWarnFiresOnceOnWriteFailure(t *testing.T) {
	// A regular file where the ledger dir should be makes MkdirAll fail on
	// every Record. The callback must see the first failure — that's what
	// tells the operator `frugal stats` will undercount — and only the
	// first, so a permanently broken dir doesn't warn once per tool call.
	dir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(dir, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	var warns []error
	w := NewWriter(dir, nil, func(err error) { warns = append(warns, err) })
	w.now = func() time.Time { return fixedNow }

	w.Record("search", "marginalia", time.Millisecond, 0, true, true)
	w.Record("search", "marginalia", time.Millisecond, 0, true, true)

	if len(warns) != 1 {
		t.Fatalf("warnFn fired %d times, want exactly 1", len(warns))
	}
	if warns[0] == nil {
		t.Errorf("warnFn must receive the write error, got nil")
	}
}

func TestNilWarnFnIsSafe(t *testing.T) {
	// NewWriter's warn callback is nil-safe: a write failure with no
	// callback must be swallowed, not panic mid-tool-call.
	dir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(dir, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := NewWriter(dir, nil, nil)
	w.now = func() time.Time { return fixedNow }
	w.Record("search", "marginalia", time.Millisecond, 0, true, true)
}

func TestEnabled(t *testing.T) {
	t.Setenv("FRUGAL_STATS", "")
	if !Enabled() {
		t.Errorf("default must be enabled")
	}
	t.Setenv("FRUGAL_STATS", "off")
	if Enabled() {
		t.Errorf("FRUGAL_STATS=off must disable")
	}
	t.Setenv("FRUGAL_STATS", "OFF")
	if Enabled() {
		t.Errorf("FRUGAL_STATS=OFF must disable (case-insensitive)")
	}
}

// close compares floats accumulated from per-call sums.
func close(a, b float64) bool {
	d := a - b
	return d < 1e-9 && d > -1e-9
}
