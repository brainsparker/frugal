package eval

import (
	"fmt"
	"io"
)

// WriteMarkdown renders a Summary as a markdown report: per-query table plus
// an aggregate line. Suitable for pasting into BENCHMARKS.md.
func WriteMarkdown(w io.Writer, s Summary) error {
	if _, err := fmt.Fprintf(w, "# Workload: %s (quality=%s, baseline=%s)\n\n",
		s.Workload, s.Quality, s.BaselineModel); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| # | Query | Selected | Provider | Frugal $ | Baseline $ | Savings % |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "|---|---|---|---|---|---|---|"); err != nil {
		return err
	}
	for i, r := range s.Results {
		if _, err := fmt.Fprintf(w, "| %d | %s | %s | %s | $%.6f | $%.6f | %.1f%% |\n",
			i+1, r.Query.Label, r.Decision.SelectedModel, r.Decision.SelectedProvider,
			r.FrugalCost, r.BaselineCost, r.SavingsPct); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w,
		"\n**Total:** Frugal $%.4f vs baseline $%.4f — **%.1f%% savings** across %d queries.\n",
		s.TotalFrugal, s.TotalBaseline, s.SavingsPct, s.QueryCount)
	return err
}
