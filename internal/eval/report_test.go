package eval

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frugalsh/frugal/internal/types"
)

func TestWriteLiveMarkdown_HasFourSectionsAndUseCaseHeader(t *testing.T) {
	jr := JudgeResult{Pass: true, Score: 0.85, Reason: "ok", CostUSD: 0.001}
	s := LiveSummary{
		Workload:                "starter",
		UseCase:                 "factual-qa",
		Quality:                 types.QualityBalanced,
		Baseline:                "gpt-4o",
		ProblemCount:            2,
		FrugalPassRate:          100,
		BaselinePassRate:        100,
		FrugalCostUSD:           0.0010,
		BaselineCostUSD:         0.0500,
		SavingsPct:              98,
		QualityDeltaPP:          0,
		FrugalLatencyP50MS:      150,
		FrugalLatencyP95MS:      400,
		BaselineLatencyP50MS:    300,
		BaselineLatencyP95MS:    900,
		FrugalToolUseAccuracy:   100,
		BaselineToolUseAccuracy: 50,
		FrugalJudgeCostUSD:      0.001,
		BaselineJudgeCostUSD:    0.001,
		ModelBreakdown:          map[string]int{"gpt-4o-mini": 2},
		CategoryStats: map[string]CategoryStat{
			"factual": {Count: 1, FrugalPassRate: 100, BaselinePassRate: 100, FrugalCostUSD: 0.0005, BaselineCostUSD: 0.025, FrugalLatencyMS: 150, BaselineLatencyMS: 300},
			"hybrid":  {Count: 1, FrugalPassRate: 100, BaselinePassRate: 100, FrugalCostUSD: 0.0005, BaselineCostUSD: 0.025, FrugalLatencyMS: 150, BaselineLatencyMS: 300},
		},
		Results: []LiveProblemResult{
			{ProblemID: "p1", Category: "factual", ToolUseExpected: ToolUseForbidden, FrugalModel: "gpt-4o-mini", FrugalPass: true, BaselinePass: true, FrugalToolUsePass: true, BaselineToolUsePass: true, FrugalJudge: &jr},
			{ProblemID: "p2", Category: "hybrid", ToolUseExpected: ToolUseOptional, FrugalModel: "gpt-4o-mini", FrugalPass: true, BaselinePass: true, FrugalToolUsePass: true, BaselineToolUsePass: true},
		},
	}

	var buf bytes.Buffer
	if err := WriteLiveMarkdown(&buf, s); err != nil {
		t.Fatalf("WriteLiveMarkdown: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"use case: factual-qa",
		"## Headline",
		"Latency p50/p95",
		"Tool-use accuracy",
		"## By category",
		"| factual |",
		"## Model selection",
		"## Per-problem results",
		"Judge cost: frugal $0.0010",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected report to contain %q, got:\n%s", want, out)
		}
	}
}

func TestWriteLiveMarkdown_StreamingAddsTTFTColumn(t *testing.T) {
	s := LiveSummary{
		Workload: "x", Quality: types.QualityCost, Baseline: "b", ProblemCount: 1,
		StreamingUsed:     true,
		FrugalTTFTP50MS:   80, FrugalTTFTP95MS: 120,
		BaselineTTFTP50MS: 200, BaselineTTFTP95MS: 350,
		ModelBreakdown: map[string]int{"m": 1},
		Results:        []LiveProblemResult{{ProblemID: "p", Category: "factual", FrugalModel: "m", ToolUseExpected: ToolUseOptional}},
	}
	var buf bytes.Buffer
	if err := WriteLiveMarkdown(&buf, s); err != nil {
		t.Fatalf("WriteLiveMarkdown: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "TTFT p50/p95") {
		t.Errorf("expected TTFT column when streaming used:\n%s", out)
	}
	if !strings.Contains(out, "80ms / 120ms") {
		t.Errorf("expected frugal TTFT row:\n%s", out)
	}
}

func TestWriteLiveMarkdown_DecisionAndHallucinationColumnsConditional(t *testing.T) {
	// Decision-scored only.
	s := LiveSummary{
		Workload: "x", Quality: types.QualityCost, Baseline: "b", ProblemCount: 1,
		DecisionScored:           1,
		FrugalDecisionAccuracy:   100,
		BaselineDecisionAccuracy: 50,
		ModelBreakdown:           map[string]int{"m": 1},
		Results: []LiveProblemResult{{
			ProblemID: "p", Category: "factual", FrugalModel: "m",
			ToolUseExpected: ToolUseOptional, ExpectedMinTier: "cost", FrugalDecisionPass: true,
		}},
	}
	var buf bytes.Buffer
	if err := WriteLiveMarkdown(&buf, s); err != nil {
		t.Fatalf("WriteLiveMarkdown: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Decision ✓") {
		t.Errorf("expected Decision column when DecisionScored>0:\n%s", out)
	}
	if strings.Contains(out, "Halluc.") {
		t.Errorf("hallucination column should not render when JudgeScored=0:\n%s", out)
	}

	// Judge-scored only.
	buf.Reset()
	s2 := LiveSummary{
		Workload: "x", Quality: types.QualityCost, Baseline: "b", ProblemCount: 1,
		JudgeScored:               1,
		FrugalHallucinationRate:   0,
		BaselineHallucinationRate: 100,
		ModelBreakdown:            map[string]int{"m": 1},
		Results: []LiveProblemResult{{
			ProblemID: "p", Category: "factual", FrugalModel: "m",
			ToolUseExpected: ToolUseOptional,
		}},
	}
	if err := WriteLiveMarkdown(&buf, s2); err != nil {
		t.Fatalf("WriteLiveMarkdown: %v", err)
	}
	out = buf.String()
	if !strings.Contains(out, "Halluc. rate") {
		t.Errorf("expected Hallucination column when JudgeScored>0:\n%s", out)
	}
	if strings.Contains(out, "Decision ✓") {
		t.Errorf("decision column should not render when DecisionScored=0:\n%s", out)
	}
}

func TestWriteLiveMarkdown_OmitsJudgeFooterWhenZero(t *testing.T) {
	s := LiveSummary{
		Workload:       "x",
		Quality:        types.QualityCost,
		Baseline:       "b",
		ProblemCount:   1,
		ModelBreakdown: map[string]int{"m": 1},
		Results: []LiveProblemResult{
			{ProblemID: "p", Category: "factual", ToolUseExpected: ToolUseOptional, FrugalModel: "m"},
		},
	}
	var buf bytes.Buffer
	if err := WriteLiveMarkdown(&buf, s); err != nil {
		t.Fatalf("WriteLiveMarkdown: %v", err)
	}
	if strings.Contains(buf.String(), "Judge cost:") {
		t.Errorf("expected no judge footer when both legs have zero judge cost, got:\n%s", buf.String())
	}
}
