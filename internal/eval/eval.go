// Package eval runs simulation-only evaluations of Frugal's routing decisions
// against a baseline model, so savings claims can be reproduced without spending
// real API budget.
package eval

import (
	"github.com/frugalsh/frugal/internal/classifier"
	"github.com/frugalsh/frugal/internal/router"
	"github.com/frugalsh/frugal/internal/types"
)

// Result is the outcome of routing one query through the eval harness.
type Result struct {
	Query        Query
	Decision     types.RoutingDecision
	BaselineCost float64
	FrugalCost   float64
	SavingsPct   float64
}

// Summary aggregates results across one workload run.
type Summary struct {
	Workload      string
	Quality       types.QualityThreshold
	BaselineModel string
	QueryCount    int
	TotalBaseline float64
	TotalFrugal   float64
	SavingsPct    float64
	Results       []Result
}

// Runner evaluates workloads against a router + classifier, compared to a
// single baseline model (e.g. "gpt-4o") that represents "without Frugal" cost.
type Runner struct {
	Router         *router.Router
	Classifier     classifier.Classifier
	BaselineModel  string
	BaselineCPKIn  float64
	BaselineCPKOut float64
}

// Run evaluates every query in the workload at the given quality threshold
// and returns a populated Summary.
func (r *Runner) Run(w Workload, quality types.QualityThreshold) Summary {
	s := Summary{
		Workload:      w.Name,
		Quality:       quality,
		BaselineModel: r.BaselineModel,
		QueryCount:    len(w.Queries),
	}
	for _, q := range w.Queries {
		features := r.Classifier.Classify(q.Request)
		decision := r.Router.Route(features, quality, nil)
		frugalCost := decision.EstimatedCost
		baselineCost := baselineCostFor(features, r.BaselineCPKIn, r.BaselineCPKOut)
		var savings float64
		if baselineCost > 0 {
			savings = (baselineCost - frugalCost) / baselineCost * 100
		}
		s.Results = append(s.Results, Result{
			Query:        q,
			Decision:     decision,
			BaselineCost: baselineCost,
			FrugalCost:   frugalCost,
			SavingsPct:   savings,
		})
		s.TotalBaseline += baselineCost
		s.TotalFrugal += frugalCost
	}
	if s.TotalBaseline > 0 {
		s.SavingsPct = (s.TotalBaseline - s.TotalFrugal) / s.TotalBaseline * 100
	}
	return s
}

func baselineCostFor(f types.QueryFeatures, cpkIn, cpkOut float64) float64 {
	return float64(f.EstimatedInputTokens)/1000*cpkIn + float64(f.EstimatedOutputTokens)/1000*cpkOut
}
