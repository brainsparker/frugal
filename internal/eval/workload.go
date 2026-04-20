package eval

import "github.com/frugalsh/frugal/internal/types"

// Query is a single prompt to route through the eval harness.
type Query struct {
	Label   string
	Request *types.ChatCompletionRequest
}

// Workload is a named collection of queries representing a realistic usage profile.
// Real workload definitions live in separate files (e.g. workloads_claude_code.go)
// and can be registered here as the benchmark set grows.
type Workload struct {
	Name        string
	Description string
	Queries     []Query
}
