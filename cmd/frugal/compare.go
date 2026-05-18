package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/classifier"
	"github.com/frugalsh/frugal/internal/config"
	"github.com/frugalsh/frugal/internal/mcp"
	"github.com/frugalsh/frugal/internal/mcp/tools"
	"github.com/frugalsh/frugal/internal/recipe"
	"github.com/frugalsh/frugal/internal/router"
)

// runCompare dispatches `frugal compare <task> [flags]`. Runs the task
// through TWO paths and prints them side-by-side:
//
//  1. The routed recipe (cheapest reliable toolchain).
//  2. A baseline: one chat call to a frontier model with no tools.
//
// Useful for "would Frugal actually save me money on this task" — agents
// and humans get to see the cost gap and the answer-quality delta in one
// command before committing to the routed path in production.
//
// Flags:
//   - --quality high|balanced|cost   (default balanced; applies to the recipe path)
//   - --recipe ID                    (override classifier)
//   - --baseline MODEL               (override default gpt-4o)
func runCompare(args []string) int {
	fs := flag.NewFlagSet("frugal compare", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	quality := fs.String("quality", "balanced", "quality tier for the recipe path (high | balanced | cost)")
	recipeID := fs.String("recipe", "", "force a specific recipe ID for the routed path")
	baseline := fs.String("baseline", "gpt-4o", "baseline model: the comparison frontier model run with no tools")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: frugal compare <task> [flags]")
		fmt.Fprintln(os.Stderr, "Run a task through the routed recipe AND a baseline (frontier model, no tools),")
		fmt.Fprintln(os.Stderr, "then print cost + answer side-by-side. Helps decide whether Frugal's routing saves")
		fmt.Fprintln(os.Stderr, "real money on this kind of task before flipping production traffic.")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return 2
	}
	task := strings.Join(fs.Args(), " ")

	rc := runConfig{subcommand: "compare", task: task, quality: *quality, recipeID: *recipeID}
	rec, _, err := resolveRecipe(rc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal compare: %v\n", err)
		return 1
	}
	cfg, err := config.Load(configPathOrDefault())
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal compare: load config: %v\n", err)
		return 1
	}
	provReg, modelEntries, thresholds, err := buildProviders(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal compare: %v\n", err)
		return 1
	}
	if err := validateBaselineModel(*baseline, provReg.AllModels()); err != nil {
		fmt.Fprintf(os.Stderr, "frugal compare: %v\n", err)
		return 2
	}
	rtr := router.New(modelEntries, thresholds)
	cls := classifier.NewRuleBased()
	costs := buildModelCosts(cfg)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// MCP loopback so the recipe path uses the same code path agents do.
	mcpServer := mcp.New("frugal", version(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	tools.RegisterSearch(mcpServer.Inner, buildSearchers(cfg))
	clientT, serverT := sdkmcp.NewInMemoryTransports()
	serverSess, err := mcpServer.Inner.Connect(ctx, serverT, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal compare: mcp loopback: %v\n", err)
		return 1
	}
	defer serverSess.Close()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "frugal-cli", Version: version()}, nil)
	clientSess, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal compare: mcp loopback client: %v\n", err)
		return 1
	}
	defer clientSess.Close()

	chat := &routedChat{cfg: cfg, rtr: rtr, cls: cls, reg: provReg, costs: costs}
	exec := recipe.NewExecutor(&loopbackTools{session: clientSess}, chat)

	// Run the recipe path.
	fmt.Fprintln(os.Stderr, "running routed recipe...")
	recipeRes, recipeErr := exec.Execute(ctx, rec, *quality, task)

	// Run the baseline: a synthetic single-step recipe that pins the
	// frontier model with no tools.
	fmt.Fprintln(os.Stderr, "running baseline (no tools)...")
	baselineRec := syntheticBaselineRecipe(*baseline)
	baselineRes, baselineErr := exec.Execute(ctx, baselineRec, *quality, task)

	printComparison(os.Stdout, task, *baseline, recipeRes, recipeErr, baselineRes, baselineErr)
	if recipeErr != nil || baselineErr != nil {
		return 1
	}
	return 0
}

func validateBaselineModel(model string, available []string) error {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return fmt.Errorf("baseline model cannot be empty")
	}
	for _, m := range available {
		if m == trimmed {
			return nil
		}
	}
	sorted := append([]string(nil), available...)
	sort.Strings(sorted)
	if len(sorted) > 8 {
		sorted = sorted[:8]
	}
	return fmt.Errorf("unknown baseline model %q; choose one of the registered models: %s", trimmed, strings.Join(sorted, ", "))
}

// syntheticBaselineRecipe builds an in-memory single-chat-step Recipe for
// the baseline leg. It never touches disk and has no classifier hints —
// callers always pass an explicit task, never go through Classify.
func syntheticBaselineRecipe(model string) recipe.Recipe {
	step := recipe.Step{Chat: &recipe.ChatStep{
		Model:  model,
		System: "Answer the task using your prior knowledge. Be accurate.",
		Input:  "{task}",
	}}
	tier := recipe.TierRecipe{Steps: []recipe.Step{step}}
	return recipe.Recipe{
		ID: "baseline",
		Recipes: map[string]recipe.TierRecipe{
			"high":     tier,
			"balanced": tier,
			"cost":     tier,
		},
	}
}

// printComparison writes a two-column-feel report contrasting the routed
// recipe leg with the baseline leg. Costs are real; we don't try to score
// answer quality automatically — the reader does that.
func printComparison(w io.Writer, task, baselineModel string, recipeRes *recipe.Execution, recipeErr error, baselineRes *recipe.Execution, baselineErr error) {
	fmt.Fprintf(w, "task: %s\n\n", task)

	fmt.Fprintln(w, "─── routed recipe ──────────────────────────────────────────")
	if recipeErr != nil {
		fmt.Fprintf(w, "  ERROR: %v\n", recipeErr)
	} else {
		fmt.Fprintf(w, "  recipe:    %s @ %s\n", recipeRes.RecipeID, recipeRes.Tier)
		for _, s := range recipeRes.Steps {
			extra := ""
			if s.Provider != "" {
				extra = " via " + s.Provider
			}
			fmt.Fprintf(w, "  step %d:    %s %s%s  ($%.5f · %dms)\n",
				s.Index, s.Kind, s.Name, extra, s.CostUSD, s.LatencyMS)
		}
		fmt.Fprintf(w, "  cost:      $%.5f total · %dms latency\n", recipeRes.TotalCostUSD, recipeRes.TotalLatencyMS)
		fmt.Fprintf(w, "  answer:    %s\n", indent(recipeRes.FinalOutput))
	}

	fmt.Fprintln(w, "\n─── baseline (no tools) ────────────────────────────────────")
	if baselineErr != nil {
		fmt.Fprintf(w, "  ERROR: %v\n", baselineErr)
	} else {
		fmt.Fprintf(w, "  model:     %s\n", baselineModel)
		fmt.Fprintf(w, "  cost:      $%.5f total · %dms latency\n", baselineRes.TotalCostUSD, baselineRes.TotalLatencyMS)
		fmt.Fprintf(w, "  answer:    %s\n", indent(baselineRes.FinalOutput))
	}

	if recipeErr == nil && baselineErr == nil {
		printSavings(w, recipeRes.TotalCostUSD, baselineRes.TotalCostUSD)
	}
}

func printSavings(w io.Writer, recipeCost, baselineCost float64) {
	if baselineCost <= 0 {
		return
	}
	delta := baselineCost - recipeCost
	pct := delta / baselineCost * 100
	if delta > 0 {
		fmt.Fprintf(w, "\nrouted path saved $%.5f (%.1f%%) on this task.\n", delta, pct)
	} else if delta < 0 {
		fmt.Fprintf(w, "\nrouted path was $%.5f (%.1f%%) MORE expensive on this task.\n", -delta, -pct)
	} else {
		fmt.Fprintln(w, "\nrouted path cost matched the baseline on this task.")
	}
}

func indent(s string) string {
	const prefix = "             "
	if s == "" {
		return "(empty)"
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) == 1 {
		return lines[0]
	}
	for i := 1; i < len(lines); i++ {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}
