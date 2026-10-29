# Changelog

All notable changes to Frugal. Versions follow the git tags.

## [Unreleased]

### Added
- `frugal doctor`: a preflight command that reports every configured
  provider's registration state, key hygiene (whitespace, quotes, a
  pasted `Bearer ` prefix, placeholders, set-but-empty env vars), list
  price, and today's spend against `daily_budget_usd` from the local
  ledger, alongside the resolved `limits`, cooldown, loop guard, cache,
  and domain policy and which MCP clients on the machine have frugal
  configured. `--probe` makes one live request per free provider
  through the real drivers and classifies the outcome (auth and billing
  rejections fail, provider-side timeouts and 5xx warn); paid providers
  are only called with `--probe-paid`, with the spend printed. `--json`
  for CI, `--strict` to fail on warnings; exit 0 / 1 / 2.

## [0.2.0] - 2026-10-21

First release since v0.1.1 (2026-07-23).

### Upgrade notes
- **Go 1.26 required to build from source.** `go.mod` now declares
  `go 1.26.0` / `toolchain go1.26.10` (clears all govulncheck findings in
  the Go 1.25.0 standard library). Release binaries are unaffected; with
  the default `GOTOOLCHAIN=auto`, older Go downloads 1.26 automatically.
- **Paid providers no longer retry.** Previously every provider retried
  transient errors (429, 408, 5xx, timeouts) twice in-driver. Now only
  free providers (`cost_per_call: 0`) retry; paid providers
  (`cost_per_call > 0`, by default Serper, You.com, Brave, Firecrawl,
  Browserless) fail over immediately so a possibly-billed request is never
  re-sent. `limits.retries` (default 2) tunes the free-provider retries.
- **New default 60s overall deadline per tool call** (`limits.call_timeout`
  / `--call-timeout`; `0s` disables). Long failover chains that used to run
  for minutes now stop at 60s with `frugal: call deadline exceeded`.

### Providers
- **You.com free tier (`youcom-free`)**: keyless major-index search rung.
- **Jina Reader (`jina`)**: free, keyless JS-rendering extract rung between
  go-readability and Firecrawl.
- **Brave Search (`brave`)**: independent-index search provider.

### Routing, limits, and safety
- Per-call cost ceiling: `max_cost_usd` on every routed tool.
- Per-provider daily spend caps and a rate-limit cooldown circuit breaker.
- Domain policy: allow / deny host lists and private-network blocking for
  routed fetches.
- Loop guard: refuses repeated identical tool calls before a provider is paid.
- Opt-in exact-match result cache for search and extract.
- `max_chars` result caps with a token receipt on every routed read.
- Overall per-call deadline (`call_timeout`, default 60s) across the
  whole failover chain, including execute's extract-to-browse fall-forward.
- Retries honor `Retry-After` (waits up to 5s, longer hints fail over),
  never sleep past the call deadline; a 429 cooldown extends to the hint
  (capped at 10 minutes).

### Receipts
- `attempts[]` and `total_cost_usd` on every routed result: each provider
  call in order with outcome, error kind, latency, and cost. `cost_usd` is
  unchanged. Failed attempts are recorded at $0 (Frugal assumes errored
  requests are not billed).

### Transport and install
- MCP 2026-07-28 support for `mcp serve --http`: stateless clients,
  `--stateless`, tool-catalog cache hints.
- `frugal mcp install` supports AnythingLLM.

### Docs and site
- Agent skill (`/.well-known/agent-skills/frugal/SKILL.md`) synced with the
  code: provider chains are generated from the routing table and a test
  fails on drift (including the index.json sha256).
- Landing page refresh.

### Tests and CI
- govulncheck green (Go 1.26.10).
- `cmd/frugal` coverage 20.1% -> 78.1%, including an MCP stdio
  end-to-end smoke test with fake providers.
- Fuzz targets for the HTML stripper, result cap, domain policy,
  Retry-After parsing, and intent classifier; a short fuzz job in CI.
  Fuzzing found and fixed a `Retry-After` overflow (huge values parsed as
  a negative wait).
- Nightly opt-in live provider smoke tests (`.github/workflows/nightly-live.yml`).

### Other
- Router-model synthetic data pipeline and frozen eval set (`pipeline/`).

## [0.1.1] - 2026-07-23

See the [v0.1.1 release](https://github.com/brainsparker/frugal/releases/tag/v0.1.1).
