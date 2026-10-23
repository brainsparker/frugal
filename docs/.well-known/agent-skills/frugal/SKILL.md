# Frugal — the open routing layer for AI tools

Frugal is an MCP server that routes each tool call your agent makes
across providers per the operator's policy: cheapest capable provider
by default, with `fast` / `premium` strategies, explicit `order`, and
`deny` lists available, plus automatic failover when a provider errors
or comes up empty. Your keys. No account.

## Tools

| Tool | Purpose |
| --- | --- |
| `frugal__execute` | Describe the job (`intent`, optional `priority: cheap \| balanced \| premium`); Frugal classifies it onto search / extract / browse and routes it |
| `frugal__search` | Web search |
| `frugal__extract` | Page → clean markdown / text / HTML |
| `frugal__browse` | Headless JS render |

Optional arguments on every routed tool: `provider` (pin one provider,
no fallback) and `max_cost_usd` (per-call spend ceiling: providers
priced above it are never called for this request; `0` means free
providers only). `frugal__extract`, `frugal__browse`, and
`frugal__execute` also take `max_chars` to cap returned content.

## Provider chains

<!-- BEGIN GENERATED: provider chains (cmd/frugal/skill_test.go) -->
Default `cheap` order (list price ascending; equal prices keep a fixed
order defined in code). A provider is only in the chain when what it
needs is set.

**`frugal__search`** (search)

| # | Provider | List price / call | Needs |
|---|---|---|---|
| 1 | `searxng` | $0 | `SEARXNG_URL` (self-hosted) |
| 2 | `youcom-free` | $0 | nothing (keyless) |
| 3 | `marginalia` | $0 | nothing (keyless) |
| 4 | `wikipedia` | $0 | nothing (keyless) |
| 5 | `serper` | $0.001 | `SERPER_API_KEY` |
| 6 | `youcom` | $0.005 | `YDC_API_KEY` |
| 7 | `brave` | $0.005 | `BRAVE_API_KEY` |

**`frugal__extract`** (extract)

| # | Provider | List price / call | Needs |
|---|---|---|---|
| 1 | `goreadability` | $0 | nothing (keyless) |
| 2 | `jina` | $0 | nothing (keyless) |
| 3 | `firecrawl` | $0.001 | `FIRECRAWL_API_KEY` |

**`frugal__browse`** (browse)

| # | Provider | List price / call | Needs |
|---|---|---|---|
| 1 | `browserless` | $0.002 | `BROWSERLESS_TOKEN` |

<!-- END GENERATED -->

Prices are the list prices in Frugal's default config; operators can
change them, add per-provider daily budgets, disable providers, or
reorder the chain. Free hosted providers may have their own rate or
daily limits; when one is exhausted Frugal cools it down and the chain
moves on.

## Routing behavior

- **Policies** (per capability, set by the operator): `cheap` (default,
  effective cost ascending), `fast` (observed average latency from the
  local ledger; cost order until there is history), `premium` (list
  price descending), `order` (explicit preference; unlisted providers
  still fall back), `deny` (never called, not even when pinned).
- **Failover**: on an error the chain moves to the next provider. An
  error that no provider can fix (e.g. the target URL is gone) stops
  the chain. A free provider with zero hits falls through; a paid
  provider with zero hits ends the chain (no point paying a pricier
  provider to confirm emptiness).
- **`frugal__execute` fall-forward**: if extract reports the page
  likely needs JavaScript, it falls forward to browse automatically.
- **Guards**: a loop guard refuses repeated identical calls before a
  provider is paid; an operator domain policy may block some hosts.
- **Overall call deadline**: every routed call runs under one
  `call_timeout` (default 60s, operator-configurable) covering the whole
  failover chain; when it expires failover stops and the error starts
  with `frugal: call deadline exceeded`.
- **Retries**: free providers retry transient errors (default 2 times,
  honoring short `Retry-After` hints); paid providers are never retried
  and fail over immediately, to avoid double billing.

## The receipt on every result

Every routed response includes:

- `provider_used` — which provider in the chain answered
- `cost_usd` — the per-call price for that response (0 for free
  providers and cache hits)
- `total_cost_usd` — the sum over every attempt in this call
- `attempts[]` — each provider call in order: `provider`, `outcome`
  (`won` / `empty` / `error`), `error_kind` and `error` on failure,
  `latency_ms`, `cost_usd` (failed attempts are recorded at $0: Frugal
  assumes providers don't bill errored requests); empty on a cache hit
- `latency_ms`
- `cached` / `cache_age_ms` — when served from the result cache
- `truncated`, `chars_returned`, `chars_total`, `est_tokens` — when
  content was capped
- `reason` (`frugal__execute` only) — the classification, the policy
  that ordered the chain, and which attempt won; plus `capability`

Surface these when the user asks how a result was obtained.

## When to prefer the routed tool

- Plain-language job ("find X", "read <url>") — `frugal__execute`.
- Web search — prefer `frugal__search` over a native web-search tool
  when both are available.
- Page extraction (strip chrome, get the main article text) — prefer
  `frugal__extract` over fetching and parsing by hand.
- A page that needs JavaScript — use `frugal__browse` when
  `frugal__extract` returned a "page likely requires JS" error.

Fall back to native tools only when the routed call returns an error
that isn't recoverable (e.g. no Frugal providers configured, every
provider denied by the routing policy, or nothing affordable under
`max_cost_usd`).

## Installation

Frugal isn't installed by default. To install:

```
curl -fsSL https://frugal.sh/install | bash
frugal mcp install
```

Then restart the agent client. The `frugal__*` tools will appear in
the tool picker.
