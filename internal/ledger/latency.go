// Latency read side: the per-provider aggregates the fast routing
// policy ranks on. Kept separate from Load because Row.LatencyMS sums
// latency across every attempt including errors — a provider that fails
// fast (connection refused in 5ms) would look "fast" there. Routing must
// rank on OK attempts only.

package ledger

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
)

// LatencySnapshot aggregates OK-attempt latency per (tool, provider)
// from the ledger under dir. Only the UTC month containing now and the
// month before it are read — enough to stay warm across a month
// rollover without letting stale history rank a provider forever.
// Missing directory or month files yield an empty map, not an error;
// unparseable lines are skipped, same contract as Load. Outer key is
// the tool ("search", "extract", "browse"), inner key the provider.
func LatencySnapshot(dir string, now time.Time) (map[string]map[string]routing.LatencyStat, error) {
	type acc struct {
		totalMS int64
		okCalls int64
	}
	agg := make(map[[2]string]*acc)

	// AddDate straight off `now` on the 31st can skip a month (Mar 31 −1
	// month → Mar 3); anchoring to the first of the month keeps
	// "previous month" meaning exactly that.
	prev := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	for _, month := range []time.Time{prev, now} {
		f, err := os.Open(filepath.Join(dir, MonthFile(month)))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		r := bufio.NewReaderSize(f, 64*1024)
		for {
			line, rerr := readLine(r, 1024*1024)
			if len(line) > 0 {
				var rec Record
				if json.Unmarshal(line, &rec) == nil && rec.OK {
					key := [2]string{rec.Tool, rec.Provider}
					a, ok := agg[key]
					if !ok {
						a = &acc{}
						agg[key] = a
					}
					a.totalMS += rec.LatencyMS
					a.okCalls++
				}
			}
			if rerr != nil {
				break
			}
		}
		f.Close()
	}

	out := make(map[string]map[string]routing.LatencyStat)
	for key, a := range agg {
		tool, provider := key[0], key[1]
		if out[tool] == nil {
			out[tool] = make(map[string]routing.LatencyStat)
		}
		out[tool][provider] = routing.LatencyStat{
			AvgMS:   float64(a.totalMS) / float64(a.okCalls),
			OKCalls: a.okCalls,
		}
	}
	return out, nil
}
