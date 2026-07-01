// Package ledger persists one JSON line per provider attempt so a later
// `frugal stats` run — a separate process from the MCP server the agent
// client spawned — can render the month's savings receipt.
//
// Write path: the MCP server attaches Writer.Record as the obs.Metrics
// sink, so every attempt (fallback losers included) lands here with no
// changes to the tool handlers or routers. Each record is one write(2)
// on an O_APPEND fd opened per call — atomic for line-sized writes on
// local filesystems — so concurrent frugal processes (Claude Desktop +
// Cursor + a terminal session) need no lock files, and month rollover
// is just a different filename on the next write.
//
// The ledger is local-only (~/.frugal/usage, dir 0700 / files 0600),
// records no query content — only tool, provider, cost, latency — and
// FRUGAL_STATS=off disables writes entirely.
package ledger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Record is one provider attempt as persisted. RackUSD is the premium
// rack rate for the capability snapshotted at call time — rates change,
// the historical record shouldn't — and is nonzero only on the winning
// attempt: fallback losers (errors AND zero-hit successes the router
// walked past) never inflate the savings line.
type Record struct {
	TS        time.Time `json:"ts"`
	Tool      string    `json:"tool"`
	Provider  string    `json:"provider"`
	CostUSD   float64   `json:"cost_usd"`
	RackUSD   float64   `json:"rack_usd"`
	LatencyMS int64     `json:"latency_ms"`
	Won       bool      `json:"won"`
	OK        bool      `json:"ok"`
}

// Enabled reports whether the ledger should record at all.
// FRUGAL_STATS=off is the opt-out.
func Enabled() bool {
	return strings.ToLower(strings.TrimSpace(os.Getenv("FRUGAL_STATS"))) != "off"
}

// Dir returns the ledger directory, ~/.frugal/usage — sibling of the
// ~/.frugal/config layout the installer writes.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".frugal", "usage"), nil
}

// Writer appends records to UTC-monthly JSONL files under dir.
// Safe for concurrent use from multiple goroutines and processes.
type Writer struct {
	dir  string
	rack map[string]float64 // capability → premium rack rate (USD/call)
	now  func() time.Time   // test hook; defaults to time.Now

	warnOnce sync.Once
	warnFn   func(error) // set via NewWriter; nil-safe
}

// NewWriter constructs a Writer. rack maps capability ("search",
// "extract", "browse") to the premium rack rate recorded on successful
// attempts; missing capabilities record RackUSD 0. warnFn, if non-nil,
// receives the first write failure — and only the first, so a
// permanently broken ledger dir doesn't spam the log once per tool call.
// Pass nil to drop failures silently (tests).
func NewWriter(dir string, rack map[string]float64, warnFn func(error)) *Writer {
	return &Writer{dir: dir, rack: rack, now: time.Now, warnFn: warnFn}
}

// Record appends one attempt. won marks the attempt that produced the
// tool result — the only one that earns rack credit. Ledger failures
// must never break tool calls: errors are swallowed after at most one
// warning via warnFn. Matches the obs.Metrics sink signature via a
// trivial adapter at the call site (ok := err == nil).
func (w *Writer) Record(tool, provider string, latency time.Duration, costUSD float64, won, ok bool) {
	now := w.now().UTC()
	rec := Record{
		TS:        now.Truncate(time.Second),
		Tool:      tool,
		Provider:  provider,
		CostUSD:   costUSD,
		LatencyMS: latency.Milliseconds(),
		Won:       won,
		OK:        ok,
	}
	if won {
		rec.RackUSD = w.rack[tool]
	}
	line, err := json.Marshal(rec)
	if err != nil {
		w.warn(err)
		return
	}
	if err := w.append(now, append(line, '\n')); err != nil {
		w.warn(err)
	}
}

// append opens the month file O_APPEND and writes one line in a single
// Write call. Open-per-write keeps month rollover and multi-process
// interleaving coordination-free.
func (w *Writer) append(now time.Time, line []byte) error {
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(w.dir, MonthFile(now))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(line)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

func (w *Writer) warn(err error) {
	w.warnOnce.Do(func() {
		if w.warnFn != nil {
			w.warnFn(err)
		}
	})
}

// MonthFile is the ledger filename for the UTC month containing t.
func MonthFile(t time.Time) string {
	return t.UTC().Format("2006-01") + ".jsonl"
}
