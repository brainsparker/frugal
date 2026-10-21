package tools

import (
	"context"
	"errors"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/frugalsh/frugal/internal/routing"
)

// Attempt is one provider call made while answering a tool call, in
// order. It makes the receipt honest about failover: cost_usd stays the
// price of the answer (backward-compatible), while attempts[] and
// total_cost_usd show everything the call touched.
//
// cost_usd per attempt is the cost the driver reported for that attempt,
// i.e. the configured list price on a success (including a zero-hit
// success from a paid provider, which is billed). Failed attempts are
// recorded at $0: Frugal assumes a provider does not bill a request that
// errored, because no driver reports a charge on failure. In-driver
// retries of one provider are one attempt here.
type Attempt struct {
	Provider   string  `json:"provider"`
	Capability string  `json:"capability,omitempty"`
	Outcome    string  `json:"outcome"` // won | empty | error
	ErrorKind  string  `json:"error_kind,omitempty"`
	Error      string  `json:"error,omitempty"`
	LatencyMS  int64   `json:"latency_ms"`
	CostUSD    float64 `json:"cost_usd"`
}

type attemptLog struct {
	mu   sync.Mutex
	list []Attempt
}

type attemptLogKey struct{}

func withAttemptLog(ctx context.Context) (context.Context, *attemptLog) {
	l := &attemptLog{}
	return context.WithValue(ctx, attemptLogKey{}, l), l
}

// attemptLogFrom returns the call's log, or nil (whose wrap is a no-op).
func attemptLogFrom(ctx context.Context) *attemptLog {
	l, _ := ctx.Value(attemptLogKey{}).(*attemptLog)
	return l
}

// wrap returns a hook that records every attempt, then calls inner.
func (l *attemptLog) wrap(capability string, inner routing.AttemptHook) routing.AttemptHook {
	if l == nil {
		return inner
	}
	return func(provider string, latency time.Duration, costUSD float64, won bool, err error) {
		a := Attempt{Provider: provider, Capability: capability, LatencyMS: latency.Milliseconds(), CostUSD: costUSD}
		switch {
		case err != nil:
			a.Outcome = "error"
			a.Error = err.Error()
			var re *routing.Error
			if errors.As(err, &re) {
				a.ErrorKind = re.Kind.String()
			}
			a.CostUSD = 0
		case won:
			a.Outcome = "won"
		default:
			a.Outcome = "empty"
		}
		l.mu.Lock()
		l.list = append(l.list, a)
		l.mu.Unlock()
		if inner != nil {
			inner(provider, latency, costUSD, won, err)
		}
	}
}

func (l *attemptLog) snapshot() ([]Attempt, float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := append([]Attempt(nil), l.list...)
	total := 0.0
	for _, a := range out {
		total += a.CostUSD
	}
	return out, total
}

// withAttempts gives each call its own attempt log and, on success,
// copies attempts[] and total_cost_usd onto the output via set.
func withAttempts[I, O any](h func(context.Context, *sdkmcp.CallToolRequest, I) (*sdkmcp.CallToolResult, O, error), set func(*O, []Attempt, float64)) func(context.Context, *sdkmcp.CallToolRequest, I) (*sdkmcp.CallToolResult, O, error) {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest, in I) (*sdkmcp.CallToolResult, O, error) {
		ctx, log := withAttemptLog(ctx)
		r, out, err := h(ctx, req, in)
		if err == nil {
			a, total := log.snapshot()
			set(&out, a, total)
		}
		return r, out, err
	}
}
