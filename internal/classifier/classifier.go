package classifier

import "github.com/frugalsh/frugal/internal/types"

// Classifier analyzes a request and extracts features for routing.
type Classifier interface {
	Classify(req *types.ChatCompletionRequest) types.QueryFeatures
}

// RuleBased implements Classifier using heuristic rules.
type RuleBased struct{}

func NewRuleBased() *RuleBased {
	return &RuleBased{}
}

func (c *RuleBased) Classify(req *types.ChatCompletionRequest) types.QueryFeatures {
	return extractFeatures(req)
}
