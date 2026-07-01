// Read side of the ledger: stream the monthly JSONL files back into the
// aggregates the `frugal stats` receipt renders. Unparseable lines are
// skipped, not fatal — a torn write on an exotic filesystem loses one
// record, never the receipt.

package ledger

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Row is one (tool, provider) aggregate in a Stats.
type Row struct {
	Tool      string  `json:"tool"`
	Provider  string  `json:"provider"`
	Calls     int64   `json:"calls"`
	Errors    int64   `json:"errors"`
	PaidUSD   float64 `json:"paid_usd"`
	RackUSD   float64 `json:"rack_usd"`
	LatencyMS int64   `json:"latency_ms_total"`
}

// Stats aggregates every record Load consumed. RackUSD is what the same
// successful calls would have cost at each capability's premium rack
// rate — the counterfactual the receipt's savings line is built on.
type Stats struct {
	Rows    []Row    `json:"rows"`
	Calls   int64    `json:"calls"`
	Errors  int64    `json:"errors"`
	PaidUSD float64  `json:"paid_usd"`
	RackUSD float64  `json:"rack_usd"`
	Months  []string `json:"months"`
}

// SavedUSD is the receipt headline: rack-rate counterfactual minus what
// was actually paid. Clamped at zero — a config with no premium
// providers can't "save negative dollars".
func (s Stats) SavedUSD() float64 {
	if d := s.RackUSD - s.PaidUSD; d > 0 {
		return d
	}
	return 0
}

// toolRank orders receipt rows by capability in the product's canonical
// order rather than alphabetically. Unknown tools sort last.
var toolRank = map[string]int{"search": 0, "extract": 1, "browse": 2}

// readLine returns the next newline-terminated line from r without its
// terminator. Lines longer than max are consumed but returned empty —
// unusable, but never fatal to the scan. The returned error is nil while
// more input remains; io.EOF (possibly alongside a final unterminated
// line) or a read error means stop after processing the returned line.
func readLine(r *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	overlong := false
	for {
		frag, err := r.ReadSlice('\n')
		line = append(line, frag...)
		if err == bufio.ErrBufferFull {
			if len(line) > max {
				overlong = true
				line = line[:0]
			}
			continue
		}
		if n := len(line); n > 0 && line[n-1] == '\n' {
			line = line[:n-1]
		}
		if overlong || len(line) > max {
			line = nil
		}
		return line, err
	}
}

// Load reads the ledger under dir. With all=false only the UTC month
// containing now is read; all=true reads every *.jsonl file present.
// A missing directory or month file yields empty Stats, not an error —
// "no usage yet" is a normal state the caller renders, not a failure.
func Load(dir string, all bool, now time.Time) (Stats, error) {
	var files []string
	if all {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return Stats{}, nil
			}
			return Stats{}, err
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
				files = append(files, filepath.Join(dir, e.Name()))
			}
		}
		sort.Strings(files)
	} else {
		files = []string{filepath.Join(dir, MonthFile(now))}
	}

	agg := make(map[[2]string]*Row)
	var st Stats
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Stats{}, err
		}
		st.Months = append(st.Months, strings.TrimSuffix(filepath.Base(path), ".jsonl"))
		// Read line-by-line with an explicit overlong-line skip rather
		// than bufio.Scanner: Scanner's ErrTooLong aborts the whole scan,
		// and one foreign or torn oversized line must cost one record,
		// never the receipt. A mid-file read error likewise keeps what was
		// aggregated so far and moves on.
		r := bufio.NewReaderSize(f, 64*1024)
		for {
			line, rerr := readLine(r, 1024*1024)
			if len(line) > 0 {
				var rec Record
				if json.Unmarshal(line, &rec) == nil {
					key := [2]string{rec.Tool, rec.Provider}
					row, ok := agg[key]
					if !ok {
						row = &Row{Tool: rec.Tool, Provider: rec.Provider}
						agg[key] = row
					}
					row.Calls++
					if !rec.OK {
						row.Errors++
					}
					row.PaidUSD += rec.CostUSD
					row.RackUSD += rec.RackUSD
					row.LatencyMS += rec.LatencyMS
				}
			}
			if rerr != nil {
				break // io.EOF or unreadable tail
			}
		}
		f.Close()
	}

	for _, row := range agg {
		st.Rows = append(st.Rows, *row)
		st.Calls += row.Calls
		st.Errors += row.Errors
		st.PaidUSD += row.PaidUSD
		st.RackUSD += row.RackUSD
	}
	sort.Slice(st.Rows, func(i, j int) bool {
		ri, rj := st.Rows[i], st.Rows[j]
		ti, iKnown := toolRank[ri.Tool]
		tj, jKnown := toolRank[rj.Tool]
		switch {
		case iKnown != jKnown:
			return iKnown
		case iKnown && ti != tj:
			return ti < tj
		case !iKnown && ri.Tool != rj.Tool:
			return ri.Tool < rj.Tool
		default:
			return ri.Provider < rj.Provider
		}
	})
	return st, nil
}
