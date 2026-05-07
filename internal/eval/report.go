package eval

import (
	"fmt"
	"io"
	"sort"
	"strings"
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

// WriteLiveMarkdown renders a LiveSummary as a four-section markdown report:
//
//  1. Headline table — Quality / Cost / Latency p50-p95 / Tool-use accuracy
//     for Frugal vs Baseline, the four dimensions the landing page advertises.
//  2. By category — same columns broken down by factual / reasoning / hybrid.
//  3. Model selection — frequency of each model the router picked.
//  4. Per-problem results — id, category, frugal model, tool ✓, judge score,
//     deterministic pass/fail for both legs.
//
// The judge-cost footer is only printed when at least one leg's judge cost is
// non-zero, so reports without --judge-model stay clean.
func WriteLiveMarkdown(w io.Writer, s LiveSummary) error {
	header := fmt.Sprintf("# %s (quality=%s, baseline=%s)", s.Workload, s.Quality, s.Baseline)
	if s.UseCase != "" {
		header = fmt.Sprintf("# %s · use case: %s (quality=%s, baseline=%s)", s.Workload, s.UseCase, s.Quality, s.Baseline)
	}
	if _, err := fmt.Fprintf(w, "%s\n\n", header); err != nil {
		return err
	}
	fmt.Fprintf(w, "Problems: %d · savings **%.1f%%** · quality Δ %+.1fpp (frugal − baseline)\n\n",
		s.ProblemCount, s.SavingsPct, -s.QualityDeltaPP)

	fmt.Fprintln(w, "## Headline")
	headers := []string{"Strategy", "Quality", "Cost", "Latency p50/p95", "Tool-use accuracy"}
	if s.StreamingUsed {
		headers = append(headers, "TTFT p50/p95")
	}
	if s.DecisionScored > 0 {
		headers = append(headers, "Decision ✓")
	}
	if s.JudgeScored > 0 {
		headers = append(headers, "Halluc. rate")
	}
	fmt.Fprintln(w, "| "+strings.Join(headers, " | ")+" |")
	fmt.Fprintln(w, "|"+strings.Repeat("---|", len(headers)))

	frugalRow := []string{
		"Frugal",
		fmt.Sprintf("%.1f%%", s.FrugalPassRate),
		fmt.Sprintf("$%.4f", s.FrugalCostUSD),
		fmt.Sprintf("%dms / %dms", s.FrugalLatencyP50MS, s.FrugalLatencyP95MS),
		fmt.Sprintf("%.1f%%", s.FrugalToolUseAccuracy),
	}
	baselineRow := []string{
		"Baseline",
		fmt.Sprintf("%.1f%%", s.BaselinePassRate),
		fmt.Sprintf("$%.4f", s.BaselineCostUSD),
		fmt.Sprintf("%dms / %dms", s.BaselineLatencyP50MS, s.BaselineLatencyP95MS),
		fmt.Sprintf("%.1f%%", s.BaselineToolUseAccuracy),
	}
	if s.StreamingUsed {
		frugalRow = append(frugalRow, fmt.Sprintf("%dms / %dms", s.FrugalTTFTP50MS, s.FrugalTTFTP95MS))
		baselineRow = append(baselineRow, fmt.Sprintf("%dms / %dms", s.BaselineTTFTP50MS, s.BaselineTTFTP95MS))
	}
	if s.DecisionScored > 0 {
		frugalRow = append(frugalRow, fmt.Sprintf("%.1f%%", s.FrugalDecisionAccuracy))
		baselineRow = append(baselineRow, fmt.Sprintf("%.1f%%", s.BaselineDecisionAccuracy))
	}
	if s.JudgeScored > 0 {
		frugalRow = append(frugalRow, fmt.Sprintf("%.1f%%", s.FrugalHallucinationRate))
		baselineRow = append(baselineRow, fmt.Sprintf("%.1f%%", s.BaselineHallucinationRate))
	}
	fmt.Fprintln(w, "| "+strings.Join(frugalRow, " | ")+" |")
	fmt.Fprintln(w, "| "+strings.Join(baselineRow, " | ")+" |")
	fmt.Fprintln(w)

	if len(s.CategoryStats) > 0 {
		fmt.Fprintln(w, "## By category")
		fmt.Fprintln(w, "| Category | n | Frugal pass | Baseline pass | Frugal $ | Baseline $ | Frugal latency | Baseline latency |")
		fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|")
		cats := make([]string, 0, len(s.CategoryStats))
		for c := range s.CategoryStats {
			cats = append(cats, c)
		}
		sort.Strings(cats)
		for _, c := range cats {
			st := s.CategoryStats[c]
			fmt.Fprintf(w, "| %s | %d | %.1f%% | %.1f%% | $%.4f | $%.4f | %dms | %dms |\n",
				c, st.Count, st.FrugalPassRate, st.BaselinePassRate,
				st.FrugalCostUSD, st.BaselineCostUSD,
				st.FrugalLatencyMS, st.BaselineLatencyMS)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "## Model selection")
	names := make([]string, 0, len(s.ModelBreakdown))
	for m := range s.ModelBreakdown {
		names = append(names, m)
	}
	sort.Slice(names, func(i, j int) bool { return s.ModelBreakdown[names[i]] > s.ModelBreakdown[names[j]] })
	for _, m := range names {
		fmt.Fprintf(w, "- `%s` × %d\n", m, s.ModelBreakdown[m])
	}

	fmt.Fprintln(w, "\n## Per-problem results")
	fmt.Fprintln(w, "| # | Problem | Category | Frugal model | Tool ✓ | Judge | Frugal ✓ | Baseline ✓ |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|")
	for i, r := range s.Results {
		fmt.Fprintf(w, "| %d | `%s` | %s | `%s` | %s | %s | %s | %s |\n",
			i+1, r.ProblemID, r.Category, r.FrugalModel,
			toolCheckbox(r.ToolUseExpected, r.FrugalToolUsePass, r.BaselineToolUsePass),
			judgeCell(r.FrugalJudge, r.BaselineJudge),
			checkbox(r.FrugalPass), checkbox(r.BaselinePass))
	}

	if s.FrugalJudgeCostUSD > 0 || s.BaselineJudgeCostUSD > 0 {
		fmt.Fprintf(w, "\nJudge cost: frugal $%.4f · baseline $%.4f (separate from agent spend)\n",
			s.FrugalJudgeCostUSD, s.BaselineJudgeCostUSD)
	}
	return nil
}

func checkbox(b bool) string {
	if b {
		return "✓"
	}
	return "✗"
}

// toolCheckbox condenses both legs' tool-use pass into one indicator. When
// expected is "optional" we render "—" instead of "✓✓" since the dimension
// doesn't carry information for that problem.
func toolCheckbox(expected string, frugalPass, baselinePass bool) string {
	if expected == "" || expected == ToolUseOptional {
		return "—"
	}
	return checkbox(frugalPass) + "/" + checkbox(baselinePass)
}

// judgeCell renders the judge score for both legs, or "—" when neither leg ran
// the judge. Format: "F:0.85 B:0.40".
func judgeCell(frugal, baseline *JudgeResult) string {
	if frugal == nil && baseline == nil {
		return "—"
	}
	f := "—"
	if frugal != nil {
		f = fmt.Sprintf("%.2f", frugal.Score)
	}
	b := "—"
	if baseline != nil {
		b = fmt.Sprintf("%.2f", baseline.Score)
	}
	return "F:" + f + " B:" + b
}
