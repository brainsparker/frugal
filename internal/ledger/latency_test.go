package ledger

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeMonthFile(t *testing.T, dir string, month time.Time, lines string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, MonthFile(month)), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLatencySnapshot_AggregatesOKAttemptsOnly(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	writeMonthFile(t, dir, now, `
{"ts":"2026-07-01T00:00:00Z","tool":"search","provider":"serper","cost_usd":0.001,"latency_ms":100,"won":true,"ok":true}
{"ts":"2026-07-02T00:00:00Z","tool":"search","provider":"serper","cost_usd":0.001,"latency_ms":300,"won":true,"ok":true}
{"ts":"2026-07-03T00:00:00Z","tool":"search","provider":"serper","cost_usd":0,"latency_ms":5,"won":false,"ok":false}
{"ts":"2026-07-03T00:00:00Z","tool":"extract","provider":"goreadability","cost_usd":0,"latency_ms":40,"won":true,"ok":true}
`)
	snap, err := LatencySnapshot(dir, now)
	if err != nil {
		t.Fatalf("LatencySnapshot: %v", err)
	}
	st := snap["search"]["serper"]
	// The 5ms failed attempt must not drag the average down.
	if st.OKCalls != 2 || st.AvgMS != 200 {
		t.Errorf("serper = %+v, want OKCalls=2 AvgMS=200", st)
	}
	if got := snap["extract"]["goreadability"]; got.OKCalls != 1 || got.AvgMS != 40 {
		t.Errorf("goreadability = %+v, want OKCalls=1 AvgMS=40", got)
	}
}

func TestLatencySnapshot_SpansPreviousMonth(t *testing.T) {
	dir := t.TempDir()
	// The 31st: naive AddDate(0,-1,0) would skip a month.
	now := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	prev := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	old := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	writeMonthFile(t, dir, now, `{"ts":"2026-07-01T00:00:00Z","tool":"search","provider":"serper","latency_ms":100,"ok":true}`+"\n")
	writeMonthFile(t, dir, prev, `{"ts":"2026-06-01T00:00:00Z","tool":"search","provider":"serper","latency_ms":300,"ok":true}`+"\n")
	writeMonthFile(t, dir, old, `{"ts":"2026-04-01T00:00:00Z","tool":"search","provider":"serper","latency_ms":9000,"ok":true}`+"\n")

	snap, err := LatencySnapshot(dir, now)
	if err != nil {
		t.Fatalf("LatencySnapshot: %v", err)
	}
	st := snap["search"]["serper"]
	// Current + previous month only; April's 9000ms record must be out.
	if st.OKCalls != 2 || st.AvgMS != 200 {
		t.Errorf("serper = %+v, want OKCalls=2 AvgMS=200 (two-month window)", st)
	}
}

func TestLatencySnapshot_MissingDirIsEmpty(t *testing.T) {
	snap, err := LatencySnapshot(filepath.Join(t.TempDir(), "nope"), time.Now())
	if err != nil {
		t.Fatalf("LatencySnapshot: %v", err)
	}
	if len(snap) != 0 {
		t.Errorf("snap = %v, want empty", snap)
	}
}

func TestLatencySnapshot_SkipsGarbageLines(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	writeMonthFile(t, dir, now, `garbage not json
{"ts":"2026-07-01T00:00:00Z","tool":"search","provider":"serper","latency_ms":150,"ok":true}
{"truncated`+"\n")
	snap, err := LatencySnapshot(dir, now)
	if err != nil {
		t.Fatalf("LatencySnapshot: %v", err)
	}
	if st := snap["search"]["serper"]; st.OKCalls != 1 || st.AvgMS != 150 {
		t.Errorf("serper = %+v, want OKCalls=1 AvgMS=150", st)
	}
}
