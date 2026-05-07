package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/frugalsh/frugal/internal/classifier"
	"github.com/frugalsh/frugal/internal/config"
	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/router"
	"github.com/frugalsh/frugal/internal/types"
	"github.com/frugalsh/frugal/internal/usecase"
)

// LiveRunner executes real ChatCompletion calls for each problem in a
// workload, scores the output with the problem's scorer, and tracks actual
// cost using per-token pricing pulled from the shipped config. It runs each
// problem twice — once through the Frugal router, once pinned to the
// workload's baseline model — so every problem has an apples-to-apples pair.
//
// Concurrency is deliberately left to the caller: the CLI layer runs
// sequentially today because provider rate limits dominate wall time. A
// goroutine pool can be layered on top without touching this struct.
type LiveRunner struct {
	Router     *router.Router
	Classifier classifier.Classifier
	Registry   *provider.Registry
	// ModelCosts maps model name → per-1k-token cost for input/output.
	// Populated from config.Config so the runner can compute real cost from
	// the usage numbers returned by providers.
	ModelCosts map[string]ModelCost
	// Judge is optional. When set, problems with a non-empty JudgeRubric run
	// an additional LLM-judge pass on top of the deterministic scorer.
	Judge *Judge
	// Bundle, when its Search field is non-empty, attaches a synthetic
	// `search` tool to every chat request so tool-use accuracy measures the
	// bundle's actual decisions rather than a hypothetical capability.
	Bundle usecase.Bundle
	// Stream switches to ChatCompletionStream so the runner can capture TTFT.
	// Off by default to keep the cost path identical to non-streaming runs;
	// flip via --stream on the CLI when TTFT is needed.
	Stream bool
}

// ModelCost is the per-1k-token price a LiveRunner uses when billing a
// response. Mirrors the relevant subset of config.ModelConfig so the runner
// doesn't depend on the config package's full struct.
type ModelCost struct {
	InputPer1K  float64
	OutputPer1K float64
}

// NewLiveRunner builds a runner from a loaded config + registry, so the
// caller doesn't have to flatten the cost map by hand.
func NewLiveRunner(cfg *config.Config, cls classifier.Classifier, rtr *router.Router, reg *provider.Registry) *LiveRunner {
	costs := make(map[string]ModelCost)
	for _, pc := range cfg.Providers {
		for name, mc := range pc.Models {
			costs[name] = ModelCost{
				InputPer1K:  mc.CostPer1KInput,
				OutputPer1K: mc.CostPer1KOutput,
			}
		}
	}
	return &LiveRunner{Router: rtr, Classifier: cls, Registry: reg, ModelCosts: costs}
}

// LiveProblemResult holds one pair of (frugal, baseline) outcomes for a
// single problem. Costs are real (from provider-reported usage) when the
// provider returns a Usage block; fall back to router estimates otherwise.
type LiveProblemResult struct {
	ProblemID string
	Category  string
	// ToolUseExpected is "required" | "forbidden" | "optional".
	ToolUseExpected string
	// ExpectedMinTier is "cost" | "balanced" | "high" or empty when the
	// problem opted out of decision-correctness scoring.
	ExpectedMinTier string
	// Frugal leg.
	FrugalModel        string
	FrugalProvider     string
	FrugalOutput       string
	FrugalPass         bool
	FrugalDetail       string
	FrugalCostUSD      float64
	FrugalLatencyMS    int64
	FrugalTTFTMS       int64
	FrugalErr          string
	FrugalToolUsed     bool
	FrugalToolUsePass  bool
	FrugalDecisionPass bool
	FrugalJudge        *JudgeResult
	// Baseline leg.
	BaselineModel        string
	BaselineOutput       string
	BaselinePass         bool
	BaselineDetail       string
	BaselineCostUSD      float64
	BaselineLatencyMS    int64
	BaselineTTFTMS       int64
	BaselineErr          string
	BaselineToolUsed     bool
	BaselineToolUsePass  bool
	BaselineDecisionPass bool
	BaselineJudge        *JudgeResult
}

// LiveSummary aggregates results across a workload run. Pass-rate is the
// share of problems each leg scored correct; cost is the total USD spent
// across all problems for that leg.
type LiveSummary struct {
	Workload         string
	UseCase          string
	Quality          types.QualityThreshold
	Baseline         string
	ProblemCount     int
	FrugalPassRate   float64
	BaselinePassRate float64
	FrugalCostUSD    float64
	BaselineCostUSD  float64
	SavingsPct       float64
	QualityDeltaPP   float64 // baseline pass-rate minus frugal pass-rate, in percentage points
	Results          []LiveProblemResult
	// ModelBreakdown: how often each model was selected by Frugal routing.
	ModelBreakdown map[string]int
	// Latency, in milliseconds, across all successfully-completed legs.
	FrugalLatencyP50MS   int64
	FrugalLatencyP95MS   int64
	BaselineLatencyP50MS int64
	BaselineLatencyP95MS int64
	// Tool-use accuracy: share of problems where the leg's tool-use decision
	// matched ToolUseExpected. Problems with expected=optional always pass.
	FrugalToolUseAccuracy   float64
	BaselineToolUseAccuracy float64
	// CategoryStats groups results by Problem.EffectiveCategory().
	CategoryStats map[string]CategoryStat
	// Judge cost is tracked separately so reports can split agent vs judge spend.
	FrugalJudgeCostUSD   float64
	BaselineJudgeCostUSD float64
	// Decision accuracy is the share of problems that opted into
	// expected_min_tier and whose chosen model met that tier. Zero opt-ins
	// leaves DecisionScored=0 and the report omits the column.
	DecisionScored           int
	FrugalDecisionAccuracy   float64
	BaselineDecisionAccuracy float64
	// Hallucination rate is the share of judge-scored problems where the
	// judge marked contains_unsupported_claims=true. JudgeScored=0 means the
	// judge never ran and the report omits the column.
	JudgeScored               int
	FrugalHallucinationRate   float64
	BaselineHallucinationRate float64
	// TTFT p50/p95 is reported only when --stream is set; otherwise zero.
	FrugalTTFTP50MS   int64
	FrugalTTFTP95MS   int64
	BaselineTTFTP50MS int64
	BaselineTTFTP95MS int64
	// StreamingUsed indicates whether the runner used the streaming code
	// path for this run, so the report can label TTFT columns as "—" when
	// the data isn't available.
	StreamingUsed bool
}

// CategoryStat is per-category roll-up for both legs.
type CategoryStat struct {
	Count             int
	FrugalPassRate    float64
	BaselinePassRate  float64
	FrugalCostUSD     float64
	BaselineCostUSD   float64
	FrugalLatencyMS   int64 // average
	BaselineLatencyMS int64
}

// Run executes every problem in w sequentially. ctx is threaded into every
// upstream call so cancellation works. Problem-level errors (network,
// invalid response) are captured in the result rather than aborting the run:
// a noisy upstream shouldn't erase the whole benchmark report.
func (r *LiveRunner) Run(ctx context.Context, w LiveWorkload, quality types.QualityThreshold) (LiveSummary, error) {
	if _, err := r.Registry.Resolve(w.Baseline); err != nil {
		return LiveSummary{}, fmt.Errorf("baseline model %q not registered: %w", w.Baseline, err)
	}

	s := LiveSummary{
		Workload:       w.Name,
		Quality:        quality,
		Baseline:       w.Baseline,
		ProblemCount:   len(w.Problems),
		ModelBreakdown: map[string]int{},
	}

	s.StreamingUsed = r.Stream

	var frugalPass, baselinePass int
	var frugalToolPass, baselineToolPass int
	var frugalLatencies, baselineLatencies []int64
	var frugalTTFTs, baselineTTFTs []int64
	var decisionScored, frugalDecisionPass, baselineDecisionPass int
	var judgeScored, frugalHallucinations, baselineHallucinations int

	for _, p := range w.Problems {
		scorer, err := p.Scorer()
		if err != nil {
			// Already validated at load; guard anyway.
			return s, err
		}

		req := buildRequest(p)
		if r.Bundle.Search != "" {
			req.Tools = append(req.Tools, searchTool())
		}
		features := r.Classifier.Classify(req)
		decision := r.Router.Route(features, quality, nil)

		expected := p.EffectiveToolUse()
		pr := LiveProblemResult{
			ProblemID:       p.ID,
			Category:        p.EffectiveCategory(),
			ToolUseExpected: expected,
			ExpectedMinTier: p.ExpectedMinTier,
		}
		if p.ExpectedMinTier != "" {
			decisionScored++
		}

		// --- Frugal leg ---
		pr.FrugalModel = decision.SelectedModel
		pr.FrugalProvider = decision.SelectedProvider
		s.ModelBreakdown[decision.SelectedModel]++

		if decision.SelectedModel == "" {
			pr.FrugalErr = "router returned no model"
		} else if prov, err := r.Registry.Resolve(decision.SelectedModel); err != nil {
			pr.FrugalErr = err.Error()
		} else {
			out := r.callAndCostMaybeStream(ctx, prov, decision.SelectedModel, req)
			pr.FrugalOutput = out.output
			pr.FrugalCostUSD = out.cost
			pr.FrugalLatencyMS = out.latencyMS
			pr.FrugalTTFTMS = out.ttftMS
			pr.FrugalToolUsed = out.toolUsed
			pr.FrugalErr = out.errMsg
			if pr.FrugalErr == "" {
				frugalLatencies = append(frugalLatencies, pr.FrugalLatencyMS)
				if r.Stream && pr.FrugalTTFTMS > 0 {
					frugalTTFTs = append(frugalTTFTs, pr.FrugalTTFTMS)
				}
			}
			res := scorer.Score(pr.FrugalOutput)
			pr.FrugalPass = res.Pass
			pr.FrugalDetail = res.Detail
			if pr.FrugalPass {
				frugalPass++
			}
			pr.FrugalToolUsePass = toolUsePass(expected, pr.FrugalToolUsed)
			if pr.FrugalToolUsePass {
				frugalToolPass++
			}
			if p.ExpectedMinTier != "" {
				pr.FrugalDecisionPass = r.Router.SatisfiesTier(decision.SelectedModel, p.ExpectedMinTier, features)
				if pr.FrugalDecisionPass {
					frugalDecisionPass++
				}
			}
			if r.Judge != nil && p.JudgeRubric != "" && pr.FrugalErr == "" {
				j, err := r.Judge.Evaluate(ctx, p.Prompt, pr.FrugalOutput, p.JudgeRubric)
				if err == nil {
					pr.FrugalJudge = &j
					s.FrugalJudgeCostUSD += j.CostUSD
				}
			}
		}

		// --- Baseline leg ---
		pr.BaselineModel = w.Baseline
		if prov, err := r.Registry.Resolve(w.Baseline); err != nil {
			pr.BaselineErr = err.Error()
		} else {
			out := r.callAndCostMaybeStream(ctx, prov, w.Baseline, req)
			pr.BaselineOutput = out.output
			pr.BaselineCostUSD = out.cost
			pr.BaselineLatencyMS = out.latencyMS
			pr.BaselineTTFTMS = out.ttftMS
			pr.BaselineToolUsed = out.toolUsed
			pr.BaselineErr = out.errMsg
			if pr.BaselineErr == "" {
				baselineLatencies = append(baselineLatencies, pr.BaselineLatencyMS)
				if r.Stream && pr.BaselineTTFTMS > 0 {
					baselineTTFTs = append(baselineTTFTs, pr.BaselineTTFTMS)
				}
			}
			res := scorer.Score(pr.BaselineOutput)
			pr.BaselinePass = res.Pass
			pr.BaselineDetail = res.Detail
			if pr.BaselinePass {
				baselinePass++
			}
			pr.BaselineToolUsePass = toolUsePass(expected, pr.BaselineToolUsed)
			if pr.BaselineToolUsePass {
				baselineToolPass++
			}
			if p.ExpectedMinTier != "" {
				pr.BaselineDecisionPass = r.Router.SatisfiesTier(w.Baseline, p.ExpectedMinTier, features)
				if pr.BaselineDecisionPass {
					baselineDecisionPass++
				}
			}
			if r.Judge != nil && p.JudgeRubric != "" && pr.BaselineErr == "" {
				j, err := r.Judge.Evaluate(ctx, p.Prompt, pr.BaselineOutput, p.JudgeRubric)
				if err == nil {
					pr.BaselineJudge = &j
					s.BaselineJudgeCostUSD += j.CostUSD
				}
			}
		}

		// Hallucination tally: only count problems where both legs got a
		// judge verdict, so the rate is comparable.
		if pr.FrugalJudge != nil && pr.BaselineJudge != nil {
			judgeScored++
			if pr.FrugalJudge.Hallucination {
				frugalHallucinations++
			}
			if pr.BaselineJudge.Hallucination {
				baselineHallucinations++
			}
		}

		s.FrugalCostUSD += pr.FrugalCostUSD
		s.BaselineCostUSD += pr.BaselineCostUSD
		s.Results = append(s.Results, pr)
	}

	if s.ProblemCount > 0 {
		s.FrugalPassRate = float64(frugalPass) / float64(s.ProblemCount) * 100
		s.BaselinePassRate = float64(baselinePass) / float64(s.ProblemCount) * 100
		s.FrugalToolUseAccuracy = float64(frugalToolPass) / float64(s.ProblemCount) * 100
		s.BaselineToolUseAccuracy = float64(baselineToolPass) / float64(s.ProblemCount) * 100
	}
	s.QualityDeltaPP = s.BaselinePassRate - s.FrugalPassRate
	if s.BaselineCostUSD > 0 {
		s.SavingsPct = (s.BaselineCostUSD - s.FrugalCostUSD) / s.BaselineCostUSD * 100
	}
	s.FrugalLatencyP50MS, s.FrugalLatencyP95MS = percentiles(frugalLatencies)
	s.BaselineLatencyP50MS, s.BaselineLatencyP95MS = percentiles(baselineLatencies)
	s.FrugalTTFTP50MS, s.FrugalTTFTP95MS = percentiles(frugalTTFTs)
	s.BaselineTTFTP50MS, s.BaselineTTFTP95MS = percentiles(baselineTTFTs)
	s.CategoryStats = computeCategoryStats(s.Results)

	s.DecisionScored = decisionScored
	if decisionScored > 0 {
		s.FrugalDecisionAccuracy = float64(frugalDecisionPass) / float64(decisionScored) * 100
		s.BaselineDecisionAccuracy = float64(baselineDecisionPass) / float64(decisionScored) * 100
	}

	s.JudgeScored = judgeScored
	if judgeScored > 0 {
		s.FrugalHallucinationRate = float64(frugalHallucinations) / float64(judgeScored) * 100
		s.BaselineHallucinationRate = float64(baselineHallucinations) / float64(judgeScored) * 100
	}

	return s, nil
}

// toolUsePass returns whether the leg's tool-use decision matches the
// expectation. Optional always passes.
func toolUsePass(expected string, used bool) bool {
	switch expected {
	case ToolUseRequired:
		return used
	case ToolUseForbidden:
		return !used
	default:
		return true
	}
}

// percentiles returns p50 and p95 from xs. Empty input returns (0, 0).
func percentiles(xs []int64) (p50, p95 int64) {
	if len(xs) == 0 {
		return 0, 0
	}
	cp := append([]int64(nil), xs...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	pick := func(q float64) int64 {
		idx := int(float64(len(cp)-1) * q)
		return cp[idx]
	}
	return pick(0.50), pick(0.95)
}

func computeCategoryStats(results []LiveProblemResult) map[string]CategoryStat {
	type acc struct {
		count                                int
		frugalPass, baselinePass             int
		frugalCost, baselineCost             float64
		frugalLatencySum, baselineLatencySum int64
		frugalLatencyN, baselineLatencyN     int
	}
	buckets := map[string]*acc{}
	for _, r := range results {
		a, ok := buckets[r.Category]
		if !ok {
			a = &acc{}
			buckets[r.Category] = a
		}
		a.count++
		if r.FrugalPass {
			a.frugalPass++
		}
		if r.BaselinePass {
			a.baselinePass++
		}
		a.frugalCost += r.FrugalCostUSD
		a.baselineCost += r.BaselineCostUSD
		if r.FrugalErr == "" {
			a.frugalLatencySum += r.FrugalLatencyMS
			a.frugalLatencyN++
		}
		if r.BaselineErr == "" {
			a.baselineLatencySum += r.BaselineLatencyMS
			a.baselineLatencyN++
		}
	}
	out := make(map[string]CategoryStat, len(buckets))
	for cat, a := range buckets {
		stat := CategoryStat{
			Count:           a.count,
			FrugalCostUSD:   a.frugalCost,
			BaselineCostUSD: a.baselineCost,
		}
		if a.count > 0 {
			stat.FrugalPassRate = float64(a.frugalPass) / float64(a.count) * 100
			stat.BaselinePassRate = float64(a.baselinePass) / float64(a.count) * 100
		}
		if a.frugalLatencyN > 0 {
			stat.FrugalLatencyMS = a.frugalLatencySum / int64(a.frugalLatencyN)
		}
		if a.baselineLatencyN > 0 {
			stat.BaselineLatencyMS = a.baselineLatencySum / int64(a.baselineLatencyN)
		}
		out[cat] = stat
	}
	return out
}

// callResult bundles every signal a single chat call produces. Returned by
// both the streaming and non-streaming paths so the runner doesn't need to
// know which one was used.
type callResult struct {
	output    string
	cost      float64
	latencyMS int64
	ttftMS    int64
	toolUsed  bool
	errMsg    string
}

// callAndCostMaybeStream dispatches to streaming when LiveRunner.Stream is on,
// otherwise to the original non-streaming path. Two paths are intentional:
// streaming is the only way to capture TTFT, but non-streaming is the
// well-tested cost path so we keep it as the default.
func (r *LiveRunner) callAndCostMaybeStream(ctx context.Context, prov provider.Provider, model string, req *types.ChatCompletionRequest) callResult {
	if r.Stream {
		return r.callAndCostStream(ctx, prov, model, req)
	}
	return r.callAndCost(ctx, prov, model, req)
}

// callAndCost runs one non-streaming ChatCompletion and extracts a string
// output + real cost from the Usage block. Real cost is preferred so reports
// reflect what the provider actually billed; if Usage is absent, fall back
// to zero rather than fabricating a number.
func (r *LiveRunner) callAndCost(ctx context.Context, prov provider.Provider, model string, req *types.ChatCompletionRequest) callResult {
	start := time.Now()
	resp, err := prov.ChatCompletion(ctx, model, req)
	latencyMS := time.Since(start).Milliseconds()
	if err != nil {
		return callResult{latencyMS: latencyMS, errMsg: err.Error()}
	}
	if len(resp.Choices) == 0 {
		return callResult{latencyMS: latencyMS, errMsg: "empty choices"}
	}
	msg := resp.Choices[0].Message
	out := callResult{
		output:    extractText(msg.Content),
		latencyMS: latencyMS,
		toolUsed:  len(msg.ToolCalls) > 0,
	}
	if resp.Usage != nil {
		mc := r.ModelCosts[model]
		out.cost = float64(resp.Usage.PromptTokens)/1000*mc.InputPer1K +
			float64(resp.Usage.CompletionTokens)/1000*mc.OutputPer1K
	}
	return out
}

// callAndCostStream is the streaming variant. It timestamps the first
// non-empty content chunk for TTFT and accumulates the rest of the response
// for the deterministic scorer. Usage usually rides on the final chunk; when
// it's absent, cost stays zero rather than fabricating a number — same policy
// as the non-streaming path.
func (r *LiveRunner) callAndCostStream(ctx context.Context, prov provider.Provider, model string, req *types.ChatCompletionRequest) callResult {
	streamReq := *req
	streamReq.Stream = true
	start := time.Now()
	ch, err := prov.ChatCompletionStream(ctx, model, &streamReq)
	if err != nil {
		return callResult{latencyMS: time.Since(start).Milliseconds(), errMsg: err.Error()}
	}
	if ch == nil {
		// Provider doesn't actually support streaming — fall back so the run
		// completes rather than error every problem.
		return r.callAndCost(ctx, prov, model, req)
	}

	var (
		buf      []byte
		ttftMS   int64
		toolUsed bool
		usage    *types.Usage
		streamErr string
	)
	for chunk := range ch {
		if chunk.Err != nil {
			streamErr = chunk.Err.Error()
			break
		}
		if chunk.Done || chunk.Data == nil {
			if chunk.Data != nil && chunk.Data.Usage != nil {
				usage = chunk.Data.Usage
			}
			continue
		}
		if chunk.Data.Usage != nil {
			usage = chunk.Data.Usage
		}
		for _, c := range chunk.Data.Choices {
			if c.Delta.Content != "" && ttftMS == 0 {
				ttftMS = time.Since(start).Milliseconds()
			}
			buf = append(buf, c.Delta.Content...)
			if len(c.Delta.ToolCalls) > 0 {
				toolUsed = true
				if ttftMS == 0 {
					ttftMS = time.Since(start).Milliseconds()
				}
			}
		}
	}
	latencyMS := time.Since(start).Milliseconds()
	out := callResult{
		output:    string(buf),
		latencyMS: latencyMS,
		ttftMS:    ttftMS,
		toolUsed:  toolUsed,
		errMsg:    streamErr,
	}
	if usage != nil {
		mc := r.ModelCosts[model]
		out.cost = float64(usage.PromptTokens)/1000*mc.InputPer1K +
			float64(usage.CompletionTokens)/1000*mc.OutputPer1K
	}
	return out
}

// searchTool returns the synthetic search tool definition the runner attaches
// to chat requests when a use-case bundle declares a search slot. The chat
// model decides whether to call it; we measure that decision as tool-use
// accuracy. The function arguments are intentionally minimal: the benchmark
// doesn't execute the search, only observes whether the model elected to
// invoke it.
func searchTool() types.Tool {
	return types.Tool{
		Type: "function",
		Function: types.ToolFunction{
			Name:        "search",
			Description: "Search the web for fresh, factual information. Call only when the answer requires up-to-date data the model is unlikely to know reliably.",
			Parameters:  []byte(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
		},
	}
}

func extractText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

func buildRequest(p Problem) *types.ChatCompletionRequest {
	var msgs []types.Message
	if p.System != "" {
		content, _ := json.Marshal(p.System)
		msgs = append(msgs, types.Message{Role: "system", Content: content})
	}
	content, _ := json.Marshal(p.Prompt)
	msgs = append(msgs, types.Message{Role: "user", Content: content})

	req := &types.ChatCompletionRequest{
		Model:    "auto",
		Messages: msgs,
	}
	if p.JSONMode {
		req.ResponseFormat = &types.ResponseFormat{Type: "json_object"}
	}
	if p.MaxTokens > 0 {
		mt := p.MaxTokens
		req.MaxTokens = &mt
	}
	return req
}
