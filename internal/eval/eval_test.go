package eval

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frugalsh/frugal/internal/classifier"
	"github.com/frugalsh/frugal/internal/router"
	"github.com/frugalsh/frugal/internal/types"
)

func TestRunnerReportsSavingsAgainstExpensiveBaseline(t *testing.T) {
	models := []router.ModelEntry{
		{
			Name: "cheap", Provider: "x",
			CostPer1KInput: 0.001, CostPer1KOutput: 0.002,
			Reasoning: 0.5, Coding: 0.5, Creative: 0.5, InstructFollowing: 0.5,
			MaxContext: 100000,
		},
		{
			Name: "expensive", Provider: "x",
			CostPer1KInput: 0.01, CostPer1KOutput: 0.03,
			Reasoning: 0.95, Coding: 0.95, Creative: 0.95, InstructFollowing: 0.95,
			MaxContext: 100000,
		},
	}
	thresholds := map[string]router.Threshold{
		"cost":     {MinReasoning: 0.3, MinCoding: 0.3, MinCreative: 0.3, MinInstructFollowing: 0.3},
		"balanced": {MinReasoning: 0.6, MinCoding: 0.6, MinCreative: 0.6, MinInstructFollowing: 0.6},
	}

	r := &Runner{
		Router:         router.New(models, thresholds),
		Classifier:     classifier.NewRuleBased(),
		BaselineModel:  "expensive",
		BaselineCPKIn:  0.01,
		BaselineCPKOut: 0.03,
	}

	w := Workload{
		Name: "smoke",
		Queries: []Query{
			{
				Label: "trivial",
				Request: &types.ChatCompletionRequest{
					Model:    "auto",
					Messages: []types.Message{{Role: "user", Content: json.RawMessage(`"hi there"`)}},
				},
			},
		},
	}

	s := r.Run(w, types.QualityCost)

	if s.QueryCount != 1 || len(s.Results) != 1 {
		t.Fatalf("expected 1 query/result, got count=%d results=%d", s.QueryCount, len(s.Results))
	}
	if s.TotalBaseline <= 0 {
		t.Fatalf("expected baseline > 0, got %f", s.TotalBaseline)
	}
	if s.TotalFrugal >= s.TotalBaseline {
		t.Fatalf("expected Frugal < baseline on trivial query; frugal=%f baseline=%f", s.TotalFrugal, s.TotalBaseline)
	}
	if s.SavingsPct <= 0 {
		t.Fatalf("expected positive savings, got %.2f%%", s.SavingsPct)
	}

	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, s); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Workload: smoke") {
		t.Fatalf("report missing workload header: %s", out)
	}
	if !strings.Contains(out, "savings") {
		t.Fatalf("report missing aggregate line: %s", out)
	}
}
