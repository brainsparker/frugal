package routing

import "time"

// AttemptHook is called once per provider attempt by a capability's
// fallback router. It lets callers (typically the metrics layer) observe
// every attempt — not just the winner — so error-path latency and
// per-provider error counts stay visible. Pass nil to skip.
//
// Signature is capability-neutral: provider name, latency, USD cost,
// whether this attempt produced the result the caller returned (won),
// and the error (nil on success). won distinguishes the attempt that
// actually served the tool call from fallback losers — including
// zero-hit successes the search router walks past — so downstream
// accounting (the usage ledger's savings counterfactual) credits only
// calls that did real work. Search / extract / browse routers all use
// this single shape.
type AttemptHook func(provider string, latency time.Duration, costUSD float64, won bool, err error)
