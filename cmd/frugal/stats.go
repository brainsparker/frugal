package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/frugalsh/frugal/internal/ledger"
)

// runStats renders the savings receipt from the local usage ledger the
// MCP server appends to (~/.frugal/usage). Returns the process exit code.
func runStats(args []string) int {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	all := fs.Bool("all", false, "aggregate every month on record, not just the current UTC month")
	asJSON := fs.Bool("json", false, "emit aggregates as JSON instead of the receipt")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: frugal stats [--all] [--json]")
		fmt.Fprintln(os.Stderr, "Show what your agent's tool calls cost this month — and what the")
		fmt.Fprintln(os.Stderr, "same calls would have cost at premium rack rate.")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dir, err := ledger.Dir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal stats: resolve ledger dir: %v\n", err)
		return 1
	}
	st, err := ledger.Load(dir, *all, time.Now().UTC())
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal stats: read ledger: %v\n", err)
		return 1
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(st); err != nil {
			fmt.Fprintf(os.Stderr, "frugal stats: encode: %v\n", err)
			return 1
		}
		return 0
	}

	renderReceipt(os.Stdout, st, *all)
	return 0
}

const receiptRule = "────────────────────────────────────────────────"

// renderReceipt prints the human receipt. Plain text, no TTY styling —
// consistent with the rest of the CLI, and it screenshots cleanly.
func renderReceipt(w io.Writer, st ledger.Stats, all bool) {
	fmt.Fprintf(w, "\nfrugal receipt · %s\n", receiptTitle(st, all))

	if st.Calls == 0 {
		fmt.Fprintln(w, "\nno tool calls recorded yet.")
		fmt.Fprintln(w, "\nroute some agent traffic through frugal__search or")
		fmt.Fprintln(w, "frugal__extract, then come back.")
		fmt.Fprintln(w, "\nledger: ~/.frugal/usage · local only · FRUGAL_STATS=off to disable")
		return
	}

	fmt.Fprintln(w, receiptRule)
	fmt.Fprintf(w, "%-9s %-18s %6s %12s\n", "tool", "provider", "calls", "paid")
	for _, r := range st.Rows {
		fmt.Fprintf(w, "%-9s %-18s %6d %12s\n", r.Tool, r.Provider, r.Calls, usd(r.PaidUSD))
	}
	fmt.Fprintln(w, receiptRule)
	fmt.Fprintf(w, "%-9s %-18s %6d %12s\n", "total", "", st.Calls, usd(st.PaidUSD))
	if st.Errors > 0 {
		fmt.Fprintf(w, "(%d failed calls — no rack credit counted)\n", st.Errors)
	}

	fmt.Fprintf(w, "\nsame calls at premium rack rate* %15s\n", usd(st.RackUSD))
	fmt.Fprintf(w, "you paid %39s\n", usd(st.PaidUSD))
	fmt.Fprintln(w, receiptRule)
	saved := st.SavedUSD()
	if st.RackUSD > 0 {
		pct := int(math.Round(saved / st.RackUSD * 100))
		fmt.Fprintf(w, "you saved %28s   (%d%%)\n", usd(saved), pct)
	} else {
		fmt.Fprintf(w, "you saved %28s\n", usd(saved))
	}
	fmt.Fprintln(w, receiptRule)
	fmt.Fprintln(w, "* rack rate = list price of each capability's premium")
	fmt.Fprintln(w, "  provider, snapshotted at call time. failed calls excluded.")
	fmt.Fprintln(w, "  data: ~/.frugal/usage · local only · frugal.sh")
}

// receiptTitle names the window the receipt covers: the single month in
// scope, or the span on record with --all.
func receiptTitle(st ledger.Stats, all bool) string {
	if all {
		switch len(st.Months) {
		case 0:
			return "all time (UTC)"
		case 1:
			return monthName(st.Months[0]) + " (UTC)"
		default:
			return fmt.Sprintf("%s – %s (UTC)", monthName(st.Months[0]), monthName(st.Months[len(st.Months)-1]))
		}
	}
	if len(st.Months) == 1 {
		return monthName(st.Months[0]) + " (UTC)"
	}
	return monthName(time.Now().UTC().Format("2006-01")) + " (UTC)"
}

// monthName renders "2026-07" as "July 2026"; unparseable input passes
// through untouched.
func monthName(yyyymm string) string {
	t, err := time.Parse("2006-01", strings.TrimSpace(yyyymm))
	if err != nil {
		return yyyymm
	}
	return t.Format("January 2006")
}

// usd formats a dollar amount at tool-call granularity (tenths of a
// cent matter when calls cost $0.001).
func usd(v float64) string {
	return fmt.Sprintf("$%.4f", v)
}
