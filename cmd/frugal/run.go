package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/classifier"
	"github.com/frugalsh/frugal/internal/config"
	"github.com/frugalsh/frugal/internal/mcp"
	"github.com/frugalsh/frugal/internal/mcp/tools"
	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/provider/anthropic"
	"github.com/frugalsh/frugal/internal/provider/google"
	"github.com/frugalsh/frugal/internal/provider/openai"
	"github.com/frugalsh/frugal/internal/recipe"
	"github.com/frugalsh/frugal/internal/router"
	"github.com/frugalsh/frugal/internal/types"
)

// runRun dispatches `frugal run <task> [flags]`. Executes the cheapest
// reliable recipe for the task end-to-end.
//
// Flags:
//   - --quality high|balanced|cost   (default balanced)
//   - --recipe ID                    (override classifier; bypass auto-pick)
//   - --verbose                      (print step trace before final output)
func runRun(args []string) int {
	cfg, err := setupAndParse("run", args)
	if err != nil {
		return cfg.exitCode
	}
	return cfg.executeRun()
}

// runRoute dispatches `frugal route <task> [flags]`. Prints the recipe
// pick + step list + resolved arg previews without making network calls.
//
// Flags mirror run; --execute promotes route to a full run.
func runRoute(args []string) int {
	cfg, err := setupAndParse("route", args)
	if err != nil {
		return cfg.exitCode
	}
	if cfg.execute {
		return cfg.executeRun()
	}
	return cfg.executeRoute()
}

type runConfig struct {
	subcommand string
	task       string
	quality    string
	recipeID   string
	verbose    bool
	execute    bool
	exitCode   int
}

// setupAndParse implements the flag parsing shared by `run` and `route`,
// returning a populated runConfig or a non-zero exit code on usage error.
// On usage error the caller exits with exitCode.
func setupAndParse(subcommand string, args []string) (runConfig, error) {
	rc := runConfig{subcommand: subcommand}
	fs := flag.NewFlagSet("frugal "+subcommand, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&rc.quality, "quality", "balanced", "quality tier (high | balanced | cost)")
	fs.StringVar(&rc.recipeID, "recipe", "", "force a specific recipe ID (bypass classifier)")
	fs.BoolVar(&rc.verbose, "verbose", false, "print the step trace before the final output")
	if subcommand == "route" {
		fs.BoolVar(&rc.execute, "execute", false, "actually run the recipe instead of dry-running it")
	}
	fs.Usage = func() {
		if subcommand == "run" {
			fmt.Fprintln(os.Stderr, "Usage: frugal run <task> [flags]")
			fmt.Fprintln(os.Stderr, "Execute the cheapest reliable toolchain for the task.")
		} else {
			fmt.Fprintln(os.Stderr, "Usage: frugal route <task> [flags]")
			fmt.Fprintln(os.Stderr, "Dry-run: print the recipe + step list without executing. Add --execute to run.")
		}
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		rc.exitCode = 2
		return rc, err
	}
	if fs.NArg() < 1 {
		fs.Usage()
		rc.exitCode = 2
		return rc, errors.New("missing task argument")
	}
	rc.task = strings.Join(fs.Args(), " ")
	if _, ok := types.ParseQualityThreshold(rc.quality); !ok {
		fmt.Fprintf(os.Stderr, "frugal %s: unknown quality %q (want high | balanced | cost)\n", subcommand, rc.quality)
		rc.exitCode = 2
		return rc, errors.New("unknown quality")
	}
	return rc, nil
}

// recipeDir resolves the use-cases directory. FRUGAL_USE_CASES_DIR wins;
// otherwise fall back to "config/use_cases" relative to cwd.
func recipeDir() string {
	if d := strings.TrimSpace(os.Getenv("FRUGAL_USE_CASES_DIR")); d != "" {
		return d
	}
	return "config/use_cases"
}

// configPathOrDefault reads FRUGAL_CONFIG; falls back to "config/models.yaml".
func configPathOrDefault() string {
	if p := strings.TrimSpace(os.Getenv("FRUGAL_CONFIG")); p != "" {
		return p
	}
	return "config/models.yaml"
}

// resolveRecipe loads the registry and picks a recipe — either an
// explicit override or via the classifier.
func resolveRecipe(rc runConfig) (recipe.Recipe, *recipe.Registry, error) {
	reg, err := recipe.Load(recipeDir())
	if err != nil {
		return recipe.Recipe{}, nil, fmt.Errorf("load recipes: %w", err)
	}
	if reg.Len() == 0 {
		return recipe.Recipe{}, nil, fmt.Errorf("no recipes found in %s — set FRUGAL_USE_CASES_DIR or run from the repo root", recipeDir())
	}
	id := rc.recipeID
	if id == "" {
		picked, ok := reg.Classify(rc.task)
		if !ok {
			return recipe.Recipe{}, nil, fmt.Errorf("no recipe matched %q (known recipes: %s); pass --recipe ID to pick explicitly", rc.task, strings.Join(reg.IDs(), ", "))
		}
		id = picked
	}
	rec, ok := reg.Get(id)
	if !ok {
		return recipe.Recipe{}, nil, fmt.Errorf("unknown recipe %q (known: %s)", id, strings.Join(reg.IDs(), ", "))
	}
	return rec, reg, nil
}

// executeRun does the real run — sets up provider registry, MCP loopback,
// recipe executor, and runs through every step.
func (rc runConfig) executeRun() int {
	rec, _, err := resolveRecipe(rc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal %s: %v\n", rc.subcommand, err)
		return 1
	}

	cfg, err := config.Load(configPathOrDefault())
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal %s: load config: %v\n", rc.subcommand, err)
		return 1
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	provReg, modelEntries, thresholds, err := buildProviders(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal %s: %v\n", rc.subcommand, err)
		return 1
	}
	rtr := router.New(modelEntries, thresholds)
	cls := classifier.NewRuleBased()
	costs := buildModelCosts(cfg)

	// In-process MCP loopback — same code path agents use. We register the
	// same tools we'd advertise via `mcp serve`, but route the session
	// through NewInMemoryTransports so there's no socket overhead.
	mcpServer := mcp.New("frugal", version(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	searchers := buildSearchers(cfg)
	tools.RegisterSearch(mcpServer.Inner, searchers)

	clientT, serverT := sdkmcp.NewInMemoryTransports()
	serverSess, err := mcpServer.Inner.Connect(ctx, serverT, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal %s: mcp loopback server: %v\n", rc.subcommand, err)
		return 1
	}
	defer serverSess.Close()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "frugal-cli", Version: version()}, nil)
	clientSess, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal %s: mcp loopback client: %v\n", rc.subcommand, err)
		return 1
	}
	defer clientSess.Close()

	exec := recipe.NewExecutor(
		&loopbackTools{session: clientSess},
		&routedChat{cfg: cfg, rtr: rtr, cls: cls, reg: provReg, costs: costs},
	)

	out, err := exec.Execute(ctx, rec, rc.quality, rc.task)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal %s: %v\n", rc.subcommand, err)
		if out != nil && rc.verbose {
			printStepTrace(os.Stderr, out)
		}
		return 1
	}

	if rc.verbose {
		printStepTrace(os.Stdout, out)
	}
	fmt.Println(out.FinalOutput)
	fmt.Fprintf(os.Stderr, "\nrecipe %s @ %s · cost $%.4f · latency %dms\n", out.RecipeID, out.Tier, out.TotalCostUSD, out.TotalLatencyMS)
	return 0
}

// executeRoute is the dry-run path. No network calls; just classify the
// task, load the recipe, and print the planned step list.
func (rc runConfig) executeRoute() int {
	rec, _, err := resolveRecipe(rc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal %s: %v\n", rc.subcommand, err)
		return 1
	}
	cfg, err := config.Load(configPathOrDefault())
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal %s: load config: %v\n", rc.subcommand, err)
		return 1
	}
	provReg, modelEntries, thresholds, err := buildProviders(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal %s: %v\n", rc.subcommand, err)
		return 1
	}
	rtr := router.New(modelEntries, thresholds)
	cls := classifier.NewRuleBased()
	costs := buildModelCosts(cfg)
	exec := recipe.NewExecutor(
		nil, // route does not call tools
		&routedChat{cfg: cfg, rtr: rtr, cls: cls, reg: provReg, costs: costs},
	)
	plan, err := exec.Plan(rec, rc.quality, rc.task)
	if err != nil {
		fmt.Fprintf(os.Stderr, "frugal %s: %v\n", rc.subcommand, err)
		return 1
	}
	printPlan(os.Stdout, plan)
	return 0
}

// printPlan renders a route plan as a step-by-step preview.
func printPlan(w io.Writer, plan *recipe.Plan) {
	fmt.Fprintf(w, "→ recipe: %s (quality=%s)\n", plan.RecipeID, plan.Tier)
	if plan.Reason != "" {
		fmt.Fprintf(w, "  reason: %s\n", strings.TrimSpace(plan.Reason))
	}
	for _, s := range plan.Steps {
		switch s.Kind {
		case "tool":
			args, _ := json.Marshal(s.Args)
			fmt.Fprintf(w, "  step %d: tool %s(%s)\n", s.Index, s.Name, args)
		case "chat":
			fmt.Fprintf(w, "  step %d: chat %s\n", s.Index, s.Name)
			if s.System != "" {
				fmt.Fprintf(w, "      system: %s\n", oneLine(s.System))
			}
			if s.Input != "" {
				fmt.Fprintf(w, "      input:  %s\n", oneLine(s.Input))
			}
		}
	}
	fmt.Fprintln(w, "no execution. add --execute to run.")
}

// printStepTrace prints each executed step's outcome — used for --verbose
// runs and on error to show how far the recipe got.
func printStepTrace(w io.Writer, exec *recipe.Execution) {
	fmt.Fprintf(w, "→ recipe: %s (quality=%s)\n", exec.RecipeID, exec.Tier)
	for _, s := range exec.Steps {
		switch s.Kind {
		case "tool":
			fmt.Fprintf(w, "  step %d: tool %s via %s · cost $%.5f · %dms\n",
				s.Index, s.Name, s.Provider, s.CostUSD, s.LatencyMS)
		case "chat":
			fmt.Fprintf(w, "  step %d: chat %s · cost $%.5f · %dms\n",
				s.Index, s.Name, s.CostUSD, s.LatencyMS)
		}
	}
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	if len(s) > 120 {
		s = s[:117] + "…"
	}
	return s
}

// buildProviders mirrors the bench startup — register each provider whose
// API key is set, then build the router taxonomy filtered to those that
// have a usable provider.
func buildProviders(cfg *config.Config) (*provider.Registry, []router.ModelEntry, map[string]router.Threshold, error) {
	reg := provider.NewRegistry()
	if pc, ok := cfg.Providers["openai"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			reg.Register(provider.WithRetry(openai.New(key, pc.BaseURL, modelNames(pc))))
		}
	}
	if pc, ok := cfg.Providers["anthropic"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			reg.Register(provider.WithRetry(anthropic.New(key, pc.BaseURL, modelNames(pc))))
		}
	}
	if pc, ok := cfg.Providers["google"]; ok {
		if key := os.Getenv(pc.APIKeyEnv); key != "" {
			reg.Register(provider.WithRetry(google.New(key, pc.BaseURL, modelNames(pc))))
		}
	}
	if len(reg.AllModels()) == 0 {
		return nil, nil, nil, fmt.Errorf("no API keys found. Set OPENAI_API_KEY, ANTHROPIC_API_KEY, or GOOGLE_API_KEY")
	}
	entries, thresholds := router.BuildTaxonomy(cfg)
	entries = filterRegisteredModels(entries, reg)
	if len(entries) == 0 {
		return nil, nil, nil, fmt.Errorf("no routable models available for registered providers")
	}
	return reg, entries, thresholds, nil
}

func buildModelCosts(cfg *config.Config) map[string]router.ModelEntry {
	out := make(map[string]router.ModelEntry)
	for _, pc := range cfg.Providers {
		for name, mc := range pc.Models {
			out[name] = router.ModelEntry{
				Name:            name,
				CostPer1KInput:  mc.CostPer1KInput,
				CostPer1KOutput: mc.CostPer1KOutput,
			}
		}
	}
	return out
}

// loopbackTools adapts a real in-process MCP client session to the
// executor's ToolCaller interface. The session was opened via
// NewInMemoryTransports — same wire format as remote MCP, no network.
type loopbackTools struct {
	session *sdkmcp.ClientSession
}

func (l *loopbackTools) CallTool(ctx context.Context, name string, args map[string]any) (recipe.ToolResult, error) {
	res, err := l.session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return recipe.ToolResult{}, err
	}
	if res.IsError {
		msg := ""
		for _, c := range res.Content {
			if tc, ok := c.(*sdkmcp.TextContent); ok {
				msg = tc.Text
				break
			}
		}
		if msg == "" {
			msg = "tool returned error with no content"
		}
		return recipe.ToolResult{}, fmt.Errorf("%s: %s", name, msg)
	}
	out := recipe.ToolResult{}
	if structured, ok := res.StructuredContent.(map[string]any); ok {
		out.Structured = structured
	}
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			out.Content = tc.Text
			break
		}
	}
	return out, nil
}

// routedChat adapts the model router + provider registry to the
// executor's ChatCaller interface. The router does the model selection
// when ChatRequest.Model == "auto".
type routedChat struct {
	cfg   *config.Config
	rtr   *router.Router
	cls   classifier.Classifier
	reg   *provider.Registry
	costs map[string]router.ModelEntry
}

func (c *routedChat) Resolve(req recipe.ChatRequest) string {
	if req.Model != "" && req.Model != "auto" {
		return req.Model
	}
	quality, _ := types.ParseQualityThreshold(req.Quality)
	cReq := buildChatRequest(req)
	features := c.cls.Classify(cReq)
	decision := c.rtr.Route(features, quality, nil)
	return decision.SelectedModel
}

func (c *routedChat) Chat(ctx context.Context, req recipe.ChatRequest) (recipe.ChatResponse, error) {
	model := req.Model
	quality, _ := types.ParseQualityThreshold(req.Quality)
	cReq := buildChatRequest(req)
	if model == "" || model == "auto" {
		features := c.cls.Classify(cReq)
		decision := c.rtr.Route(features, quality, nil)
		model = decision.SelectedModel
		if model == "" {
			return recipe.ChatResponse{}, fmt.Errorf("router returned no model for this request")
		}
	}
	prov, err := c.reg.Resolve(model)
	if err != nil {
		return recipe.ChatResponse{}, fmt.Errorf("model %q not registered: %w", model, err)
	}
	start := time.Now()
	resp, err := prov.ChatCompletion(ctx, model, cReq)
	if err != nil {
		return recipe.ChatResponse{}, err
	}
	latency := recipe.SinceMS(start)
	out := recipe.ChatResponse{Model: model, LatencyMS: latency}
	if len(resp.Choices) > 0 {
		out.Output = unmarshalText(resp.Choices[0].Message.Content)
	}
	if entry, ok := c.costs[model]; ok {
		out.CostUSD = float64(resp.Usage.PromptTokens)/1000*entry.CostPer1KInput +
			float64(resp.Usage.CompletionTokens)/1000*entry.CostPer1KOutput
	}
	return out, nil
}

func buildChatRequest(req recipe.ChatRequest) *types.ChatCompletionRequest {
	out := &types.ChatCompletionRequest{Model: "auto"}
	if req.System != "" {
		raw, _ := json.Marshal(req.System)
		out.Messages = append(out.Messages, types.Message{Role: "system", Content: raw})
	}
	if req.Input != "" {
		raw, _ := json.Marshal(req.Input)
		out.Messages = append(out.Messages, types.Message{Role: "user", Content: raw})
	}
	return out
}

// unmarshalText decodes a json.RawMessage that the provider populated as a
// JSON string. Returns "" on failure — providers that return structured
// content blocks (multi-part messages) drop out of the simple-text path,
// which is fine for the recipe model (chat steps are text-in, text-out
// today).
func unmarshalText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}
