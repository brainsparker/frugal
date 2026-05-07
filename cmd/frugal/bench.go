package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/frugalsh/frugal/internal/classifier"
	"github.com/frugalsh/frugal/internal/config"
	"github.com/frugalsh/frugal/internal/eval"
	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/provider/anthropic"
	"github.com/frugalsh/frugal/internal/provider/google"
	"github.com/frugalsh/frugal/internal/provider/openai"
	"github.com/frugalsh/frugal/internal/router"
	"github.com/frugalsh/frugal/internal/types"
	"github.com/frugalsh/frugal/internal/usecase"
)

const defaultBenchWorkload = "config/workloads/starter.yaml"

// runBench executes `frugal bench [-workload PATH] [-quality TIER] [-out FILE]
// [-timeout DUR]`. Returns the process exit code.
//
// Example: `frugal bench -quality balanced -out bench.md`.
func runBench(configPath string, args []string) int {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	useCaseID := fs.String("use-case", "", "use-case ID from config/use_cases (factual-qa | code-dev | research-synthesis | structured-extraction). When set, the bundle's chat model becomes the baseline.")
	workloadPath := fs.String("workload", "", "path to workload YAML (overrides the use-case's workload; defaults to "+defaultBenchWorkload+" when --use-case is unset)")
	qualityStr := fs.String("quality", "balanced", "quality tier (high | balanced | cost)")
	outPath := fs.String("out", "", "write markdown report to this path in addition to stdout")
	judgeModel := fs.String("judge-model", "", "optional LLM-judge model. When set, problems with judge_rubric run an additional pass and report a score 0-1.")
	stream := fs.Bool("stream", false, "use streaming chat completions to capture time-to-first-token. Adds a TTFT p50/p95 column to the report.")
	timeout := fs.Duration("timeout", 10*time.Minute, "overall bench timeout")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: frugal bench [flags]")
		fmt.Fprintln(os.Stderr, "Measures cost, quality, latency, and tool-use accuracy for every problem")
		fmt.Fprintln(os.Stderr, "in the workload, calling real provider APIs. Use --use-case to compare")
		fmt.Fprintln(os.Stderr, "Frugal's bundle vs the curated baseline for that use case.")
		fmt.Fprintln(os.Stderr, "Requires whichever provider keys the workload's models + baseline need")
		fmt.Fprintln(os.Stderr, "(OPENAI_API_KEY, ANTHROPIC_API_KEY, GOOGLE_API_KEY).")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	quality, ok := types.ParseQualityThreshold(*qualityStr)
	if !ok {
		fmt.Fprintf(os.Stderr, "frugal bench: unknown quality %q (want high | balanced | cost)\n", *qualityStr)
		return 2
	}

	// Resolve the use case if requested. The bundle's chat model becomes the
	// canonical baseline ("what would I get without Frugal for this use case").
	var bundle usecase.Bundle
	var useCaseLabel string
	if *useCaseID != "" {
		dir := os.Getenv("FRUGAL_USE_CASES_DIR")
		if dir == "" {
			dir = "config/use_cases"
		}
		ucReg, err := usecase.Load(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "frugal bench: load use cases: %v\n", err)
			return 1
		}
		uc, ok := ucReg.Get(*useCaseID)
		if !ok {
			fmt.Fprintf(os.Stderr, "frugal bench: unknown use case %q (known: %v)\n", *useCaseID, ucReg.IDs())
			return 2
		}
		b, ok := ucReg.Bundle(*useCaseID, string(quality))
		if !ok || b.Chat == "" {
			fmt.Fprintf(os.Stderr, "frugal bench: use case %q has no bundle for quality %q\n", *useCaseID, quality)
			return 2
		}
		bundle = b
		useCaseLabel = *useCaseID
		// Fall back to the use case's referenced workload when --workload isn't set.
		if *workloadPath == "" && uc.Workload != "" {
			if _, err := os.Stat(uc.Workload); err == nil {
				*workloadPath = uc.Workload
			} else {
				fmt.Fprintf(os.Stderr, "frugal bench: use case workload %q not found, falling back to %s\n", uc.Workload, defaultBenchWorkload)
			}
		}
	}

	resolvedWorkload := *workloadPath
	if resolvedWorkload == "" {
		resolvedWorkload = defaultBenchWorkload
	}
	if !filepath.IsAbs(resolvedWorkload) {
		if _, err := os.Stat(resolvedWorkload); err != nil {
			// Fall back to the installed config dir when running from elsewhere.
			if p := os.Getenv("FRUGAL_CONFIG_DIR"); p != "" {
				alt := filepath.Join(p, filepath.Base(resolvedWorkload))
				if _, err := os.Stat(alt); err == nil {
					resolvedWorkload = alt
				}
			}
		}
	}

	w, err := eval.LoadLiveWorkload(resolvedWorkload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal bench: %v\n", err)
		return 1
	}

	// Use-case bundle's chat model is the authoritative baseline for that use
	// case. Only override after a successful workload load so YAML errors still
	// surface clearly.
	if bundle.Chat != "" {
		w.Baseline = bundle.Chat
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal bench: load config: %v\n", err)
		return 1
	}

	reg := provider.NewRegistry()
	registerBenchProviders(cfg, reg)
	if len(reg.AllModels()) == 0 {
		fmt.Fprintln(os.Stderr, "frugal bench: no API keys found. Set OPENAI_API_KEY, ANTHROPIC_API_KEY, or GOOGLE_API_KEY.")
		return 1
	}

	if _, err := reg.Resolve(w.Baseline); err != nil {
		fmt.Fprintf(os.Stderr, "frugal bench: baseline model %q not registered (no key for its provider?): %v\n",
			w.Baseline, err)
		return 1
	}

	modelEntries, thresholds := router.BuildTaxonomy(cfg)
	modelEntries = filterRegisteredModels(modelEntries, reg)
	if len(modelEntries) == 0 {
		fmt.Fprintln(os.Stderr, "frugal bench: no routable models available for registered providers")
		return 1
	}
	rtr := router.New(modelEntries, thresholds)
	cls := classifier.NewRuleBased()
	runner := eval.NewLiveRunner(cfg, cls, rtr, reg)

	if *judgeModel != "" {
		prov, err := reg.Resolve(*judgeModel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "frugal bench: judge model %q not registered: %v\n", *judgeModel, err)
			return 1
		}
		mc := runner.ModelCosts[*judgeModel]
		runner.Judge = &eval.Judge{Model: *judgeModel, Provider: prov, ModelCost: mc}
	}
	runner.Bundle = bundle
	runner.Stream = *stream

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if useCaseLabel != "" {
		fmt.Fprintf(os.Stderr, "running %d problems from %q · use case=%s · baseline=%s @ quality=%s...\n",
			len(w.Problems), w.Name, useCaseLabel, w.Baseline, quality)
	} else {
		fmt.Fprintf(os.Stderr, "running %d problems from %q against baseline %s @ quality=%s...\n",
			len(w.Problems), w.Name, w.Baseline, quality)
	}
	summary, err := runner.Run(ctx, w, quality)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal bench: %v\n", err)
		return 1
	}
	summary.UseCase = useCaseLabel

	// Always write to stdout.
	var buf bytes.Buffer
	if err := eval.WriteLiveMarkdown(&buf, summary); err != nil {
		fmt.Fprintf(os.Stderr, "frugal bench: render report: %v\n", err)
		return 1
	}
	fmt.Print(buf.String())

	if *outPath != "" {
		if err := os.WriteFile(*outPath, buf.Bytes(), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "frugal bench: write %s: %v\n", *outPath, err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", *outPath)
	}

	// Print a one-line summary on stderr for CI log scraping.
	fmt.Fprintf(os.Stderr,
		"bench done: %d problems · frugal %.1f%% pass · baseline %.1f%% pass · savings %.1f%%\n",
		summary.ProblemCount, summary.FrugalPassRate, summary.BaselinePassRate, summary.SavingsPct)
	return 0
}

// registerBenchProviders mirrors runWrap's provider registration without the
// retry wrapper: benchmarks should see raw upstream behavior so retries don't
// mask quality/rate-limit issues.
func registerBenchProviders(cfg *config.Config, reg *provider.Registry) {
	if pc, ok := cfg.Providers["openai"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			reg.Register(openai.New(key, pc.BaseURL, modelNames(pc)))
		}
	}
	if pc, ok := cfg.Providers["anthropic"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			reg.Register(anthropic.New(key, pc.BaseURL, modelNames(pc)))
		}
	}
	if pc, ok := cfg.Providers["google"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			reg.Register(google.New(key, pc.BaseURL, modelNames(pc)))
		}
	}
}
