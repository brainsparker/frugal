# frugal

**The open routing layer for AI tools.**

Frugal is an MCP server that sits between your agent and its tool
providers. You describe the job — a search, an extraction, a render, or
just an intent — and Frugal decides how to complete it, routing each call
per policy: cheapest capable provider by default, fastest or premium when
you say so, pinned or denied when compliance says so, with automatic
failover when a provider errors or comes up empty. Every response carries
the decision — `provider_used`, `cost_usd`, and (via `frugal__execute`) a
one-line reason — so you can audit why each call went where it did.

Works with any model. One Go binary. Your keys. No account. Self-host
everything, no lock-in. Source-available (BUSL 1.1 → Apache 2.0).

Install to first intelligently routed tool call: under five minutes.

[frugal.sh](https://frugal.sh)

## Install

```bash
curl -fsSL https://frugal.sh/install | bash
frugal mcp install
```

The first command drops the binary in your `$PATH`. The second auto-detects
Claude Desktop, Cursor, AnythingLLM, and Claude Code and merges `frugal`
into each configured MCP server list.

### AnythingLLM

`frugal mcp install` finds AnythingLLM Desktop on its own and merges
`frugal` into `<storage>/plugins/anythingllm_mcp_servers.json`. For a
self-hosted or Docker AnythingLLM, point the installer at that instance's
storage directory (the same path its own `STORAGE_DIR` uses):

```bash
ANYTHINGLLM_STORAGE_DIR=/path/to/anythingllm/storage frugal mcp install --client anythingllm
```

Restart AnythingLLM, then look for **frugal** under Agent Skills → MCP
Servers. The tools load for agent sessions, so call them from an `@agent`
chat. In Docker, the `command` path must resolve *inside* the container —
mount the `frugal` binary in, or run `frugal mcp serve --http` on the host
and register it as a `streamable` server instead.

### Streamable HTTP and MCP 2026-07-28

`frugal mcp serve --http ADDR` serves Streamable HTTP for remote clients and
Docker setups. It speaks the current MCP revision, 2026-07-28, which removed
protocol-level sessions and the `initialize` handshake, as well as the
session-based revisions before it. Both kinds of client connect to the same
endpoint:

- A client speaking 2026-07-28 or newer is served statelessly: no
  `Mcp-Session-Id`, every POST self-contained, `server/discover` answered
  with the supported versions and capabilities.
- An older client (2025-11-25 and earlier) keeps its session and optional
  GET event stream, so nothing changes for the hosts you already run.

Pass `--stateless` to drop sessions for every client, which is what you want
when several `frugal` replicas sit behind a plain load balancer: any replica
can answer any request. Older clients still work in that mode; they only lose
the optional GET stream (declined with 405, as the spec allows).

Catalog results (`tools/list`, `server/discover`) carry a one-hour `ttlMs`
hint with `cacheScope: public`, so spec-current clients stop re-fetching the
tool list on every turn. The catalog is fixed for the life of the process, so
a restart with a new config is the only thing that changes it.

```bash
FRUGAL_AUTH_TOKEN=... frugal mcp serve --http 0.0.0.0:8765 --stateless
```

## Try it now (no keys)

Search and extract work out of the box: **You.com free tier**
(`youcom-free`: the same major web index as the keyed You.com provider,
reached through You.com's hosted MCP endpoint with no key and no account,
about 100 queries per day), **Marginalia** (free index of the indie /
non-commercial web), **Wikipedia** (free Wikimedia REST search),
**go-readability** (free, pure-Go local extractor), and **Jina Reader**
(free hosted render for pages that paint their content with JavaScript)
ship enabled with zero configuration. This is the default `cheap` policy
at work — free and local rungs first, failover when a provider comes up
empty. Captured from a live zero-key run:
```
frugal__search {"query": "AI agent framework comparison", "max_results": 3}

  result › {
    "provider_used": "youcom-free",
    "cost_usd": 0,
    "latency_ms": 177,
    "results": [
      {"title": "Top 12 AI Agent Frameworks Comparison", "url": "https://www.nimbleway.com/blog/top-ai-agent-frameworks-comparison", ...},
      {"title": "AI Agent Frameworks Compared: LangGraph, CrewAI, AutoGen", "url": "https://pecollective.com/blog/ai-agent-frameworks-compared/", ...},
      {"title": "Comparing Open-Source AI Agent Frameworks - Langfuse", "url": "https://langfuse.com/blog/2025-03-19-ai-agent-comparison", ...}
    ]
  }
```

Once the free tier's daily allowance is spent it rate-limits, the router
cools it down, and the chain keeps serving from the narrower free
indexes. The same query on that fallback path, also captured live:

```
frugal__search {"query": "AI agent framework comparison", "max_results": 3}

  stderr › search zero hits; falling back  provider=marginalia latency_ms=529
  result › {
    "provider_used": "wikipedia",
    "cost_usd": 0,
    "latency_ms": 954,
    "results": [
      {"title": "Gemini Enterprise Agent Platform", "url": "https://en.wikipedia.org/wiki/Gemini_Enterprise_Agent_Platform", ...},
      {"title": "Perplexity AI", "url": "https://en.wikipedia.org/wiki/Perplexity_AI", ...},
      {"title": "Agentic commerce", "url": "https://en.wikipedia.org/wiki/Agentic_commerce", ...}
    ]
  }
```

Honest limits: `youcom-free` is a general web index but is capped at
roughly 100 queries a day per IP; Marginalia is genuinely good for
essays, docs, blogs, and niche technical writing, and weak on mainstream
news and product pages; Wikipedia covers entities and reference topics.
Zero-key mode is a real workflow for research and local extraction, but
it is not unlimited. One env var changes that: `SEARXNG_URL` (free,
self-hosted), `SERPER_API_KEY` ($0.001/call), or `YDC_API_KEY` (the keyed
You.com tier, no daily cap) gives the chain a stronger rung to fall to.
Prefer to keep queries off You.com entirely? Add `youcom-free: {enabled:
false}` under `search_providers` and the chain is Marginalia and
Wikipedia again.

## Routing policies

Declare per capability how the provider chain is ordered, in
`~/.frugal/config/models.yaml`:

```yaml
routing:
  search:
    strategy: fast            # cheap (default) | fast | premium
    deny: [youcom]            # never called — not by fallback, not even pinned
  extract:
    order: [firecrawl, goreadability]  # explicit preference; unlisted providers still fall back
```

- **cheap** (default) — effective cost ascending, quota-aware; automatic
  failover up the ladder.
- **fast** — ordered by your machine's own observed latency (from the
  local usage ledger, successful calls only). Falls back to cost order
  until enough history exists. Not a live probe.
- **premium** — prefers your premium-priced providers, list price
  descending.
- **order** — an explicit preference list. It's a prefix, not a
  whitelist: unlisted providers still serve as fallback.
- **deny** — providers that must never be called. Enforced on pinned
  calls too; this is the honest privacy knob (e.g. deny every paid
  provider and nothing leaves your free/local rungs).

Every call chain logs which policy ran, and `frugal__execute` returns it
in the response.

## Spend caps and rate-limit cooldown

Two guardrails cap what a provider can cost you, enforced in the routing
layer alongside the policy above:

```yaml
search_providers:
  youcom:
    api_key_env: YDC_API_KEY
    cost_per_call: 0.005
    daily_budget_usd: 2.50   # skip youcom once it has spent $2.50 today

routing:
  cooldown: 90s              # after a 429, skip that provider for 90s (default 60s)
```

- **daily_budget_usd** (per provider, any of the three tables) — once a
  provider's spend for the current UTC day reaches this cap, the router
  skips it and falls through to the next provider in the chain; a call
  that pins it by name errors. Counters reset at UTC midnight. Zero or
  absent means no cap. The same provider name under two capability tables
  gets an independent budget.
- **cooldown** (top level under `routing:`) — when a provider returns a
  rate limit (HTTP 429), it is fenced off for this long so the chain
  stops hammering it. A Go duration string like `90s` or `2m`; an invalid
  value warns at startup and falls back to the 60s default. Applies to
  every provider, capped or not.

When a guardrail skips a provider it is noted in the routing trace
(`; budget: skipped youcom (...)`) and logged at Warn. If every provider
in a chain is over budget or cooling down, the call fails with a clear
message rather than silently doing nothing.

## Multi-attempt receipts: `attempts[]` and `total_cost_usd`

`cost_usd` remains the price of the answer you got. Every successful
routed result also carries `attempts[]` (each provider call in order,
with `provider`, `outcome` = `won` / `empty` / `error`, `error_kind`,
`error`, `latency_ms`, `cost_usd`) and `total_cost_usd` (their sum), so
a call that failed over shows every rung it touched. A failed attempt is
recorded at `$0`: Frugal assumes a provider does not bill a request that
errored (no driver reports a charge on failure). A paid provider's
zero-hit answer is billed and shows its list price. In-driver retries of
one provider count as one attempt. Cache hits have no attempts.

## Result size caps and the token receipt

Dollars are one cost of a tool call. The other is the tokens its result
occupies in your model's context for the rest of the session, and MCP
clients enforce that ceiling blind: Claude Code rejects MCP results over
25,000 tokens by default and spills anything past 50,000 characters to a
file with a 2 KB preview, so a long page either fails the call or
arrives cut off with nothing telling the model the tail is missing.

Frugal moves that decision server-side and puts it on the receipt. Every
`frugal__extract`, `frugal__browse`, and `frugal__execute` response
reports `chars_returned`, `chars_total`, `est_tokens`, and (when they
differ) `truncated: true`; `frugal__search` reports `est_tokens` for the
result list. To cap what comes back, pass `max_chars` on the call or set
a default in `models.yaml`:

```yaml
limits:
  max_chars: 40000   # default page-content budget per call (about 10k tokens)
```

```
frugal__extract {"url": "https://example.com/long-article", "max_chars": 8000}

  result › {
    "markdown": "...the first 8,000 characters, cut on a word boundary...
                 [frugal: output truncated to 7996 of 61230 chars; pass a larger max_chars to see more]",
    "provider_used": "goreadability",
    "cost_usd": 0,
    "chars_returned": 7996,
    "chars_total": 61230,
    "truncated": true,
    "est_tokens": 1999
  }
```

- The budget is shared across the content fields in priority order
  (markdown, then text, then html), so the readable rendering survives
  and raw HTML is the first thing to go.
- The cut is rune-safe and backs off to the nearest word boundary. The
  marker is appended to the shortened field so the truncation is
  unmistakable even in clients that flatten structured output to text.
- A per-call `max_chars` overrides the configured default in either
  direction. Zero or absent means no cap, which is also the default: an
  existing config returns results byte-for-byte as before.
- Search results are measured, never truncated. `max_results` is the
  size knob there, and `est_tokens` shows what a wide value costs.
- `est_tokens` is `chars / 4`, rounded up: a planning figure, not a
  bill. Real tokenizers run higher on dense HTML and lower on prose.

## Result cache

Off by default. When enabled, a repeated identical `frugal__search` or
`frugal__extract` call inside the TTL is answered from process memory
instead of a provider, at zero cost. Agents repeat themselves
constantly: retry loops, sibling subagents issuing the same query,
follow-up questions about the same page. With the cache on, only the
first call pays.

```yaml
cache:
  enabled: true
  search_ttl: 5m      # default 5m; "0" turns search caching off
  extract_ttl: 15m    # default 15m; "0" turns extract caching off
  max_entries: 512    # LRU eviction past this bound (default 512)
```

- Hits are labeled in the response: `cached: true`, `cache_age_ms`, and
  `cost_usd: 0`, with `provider_used` still naming the provider that
  produced the original result. Nothing is silently stale: the agent
  can always see it got a cached answer and how old it is.
- Exact-match by design: the key covers the query or URL plus every
  argument that changes what a provider would return (`max_results`,
  `freshness`, `formats`, a provider pin). The Phase 3 semantic cache
  builds on top of this layer; it does not replace it.
- `frugal__execute` shares entries with the direct tools, so
  `frugal__execute("search python docs")` is a hit after
  `frugal__search("python docs")` and vice versa. An explicit
  `priority: cheap` or `premium` on execute bypasses the cache, since
  the caller asked for a specific routing outcome.
- `frugal__browse` is never cached: rendering a page is exactly the
  case where the caller wants the live DOM.
- In-memory only, per process. Cached provider payloads never touch
  disk and never outlive the server.

## Domain policy: which hosts may be fetched

Routing policies decide which provider does a job. The domain policy
decides which hosts a job may touch at all, and it runs before any
provider is called, so a refused target costs nothing and never leaves
your machine:

```yaml
routing:
  domains:
    allow: [docs.python.org, github.com]   # only these hosts (and subdomains) may be fetched
    deny: [gist.github.com, "*.internal"]  # never fetched, even when allow would admit them
    deny_private: true                     # refuse loopback, LAN, link-local, and CGNAT targets
```

- **allow**, when set, is a whitelist: `frugal__extract`, `frugal__browse`,
  and the URL intents of `frugal__execute` refuse any host not under it.
  Absent means every host is fetchable, the historical behavior.
- **deny** always wins, including over a matching allow rule and over a
  call that pins a provider by name. The refusal names the rule.
- **deny_private** refuses loopback, RFC 1918, link-local (which covers
  cloud metadata endpoints such as 169.254.169.254), carrier-grade NAT,
  and unspecified IP literals, plus `localhost`, `.local`, `.internal`,
  and `.home.arpa` names. Meant for a `frugal mcp serve --http` instance
  that agents reach over the network: a page an agent was steered onto
  cannot use it to read your LAN.
- Rules are bare hosts, case-insensitive, no scheme, port, or path.
  `example.com` matches the apex and every subdomain; `*.example.com`
  matches subdomains only. A malformed rule fails the config load with
  the reason instead of silently matching nothing.
- `frugal__search` applies the same rule to its result list and reports
  what it removed in `warnings` (`domain policy dropped 2 results ...`),
  so an agent is never handed a link it will then be refused.

Every refusal is logged at Warn with the tool and URL. A burst of them is
the signal that an agent is being steered somewhere it should not go,
which is the point: fetched web content is the main prompt-injection
ingest in an agent stack, and this is the knob that fences it.

Honest limits: matching is by the URL as written. Hostnames are not
resolved, so a public name that points at a private address is not
caught by `deny_private`, and redirects a provider follows are not
re-checked. It is a policy rail, not a network sandbox.

## Loop guard

A third rail catches the failure the other two cannot see coming: an
agent stuck re-issuing the same tool call. Budgets only trip after the
money is spent, cooldowns only fire after a provider says 429, and the
free rungs never hit either. The MCP spec gives clients no backoff
convention, so the server has to be the one that says stop.

```yaml
routing:
  loop_limit: 5              # identical calls admitted per window (default 5; 0 turns the rail off)
  loop_window: 60s           # sliding window the limit counts within (default 60s)
```

On by default. A call is "identical" when the same tool receives the same
normalized arguments (whitespace trimmed, provider spelling folded, so a
loop that jitters its formatting still counts). The sixth identical
`frugal__search` inside a minute is refused before any provider is
reached, with a reason the agent can act on:

```
frugal__search: loop guard: this exact frugal__search call has been made 6 times
in the last 1m0s (limit 5); the result will not change, so change the arguments or wait 48s
```

Refused calls still count, so a client that keeps hammering stays refused
until it goes quiet for a full window. Each tool has its own fingerprint
space (an extract and a browse of the same URL are two calls, not one),
and a changed argument, `max_results`, `freshness`, `priority`, is a new
call. The guard holds only hashes of the arguments, in memory, in a
bounded map; nothing about the query is written anywhere. Trips are
logged at Warn next to the budget skips.
## Per-call cost ceilings: `max_cost_usd`

Daily budgets protect your wallet over a day. `max_cost_usd` lets the
agent decide, per call, what one question is worth. Every routed tool
(`frugal__search`, `frugal__extract`, `frugal__browse`, and
`frugal__execute`) accepts it:

```
frugal__search  {"query": "is this library still maintained", "max_cost_usd": 0.001}
frugal__execute {"intent": "read https://example.com/post", "max_cost_usd": 0}
```

- Providers whose effective per-call price is above the ceiling are
  removed from the chain before anything is called, so they are not
  reached as fallback rungs either. The rest of the chain routes under
  your policy as usual.
- `0` means free providers only. Effective price is quota-aware: a paid
  provider with free quota left this month counts as free.
- Pinning a provider priced above your own ceiling is refused before
  any spend, like pinning a denied or over-budget provider.
- On `frugal__execute`, the ceiling also governs the extract-to-browse
  fall-forward. A call capped at `0` never falls forward into a paid
  render; the empty extract comes back with a warning that says why.
- Skips are visible in the trace (`; ceiling: skipped youcom (youcom
  costs $0.0050 per call, above max_cost_usd $0.0010)`). If nothing
  fits, the error names the cheapest configured provider and its price
  so the agent can raise the ceiling or move on.

Leave it unset and routing is exactly what it was. The ceiling is a
request-time knob for the agent; the operator's `daily_budget_usd`,
`deny`, and `cooldown` still apply on top of it.

## Describe the job: `frugal__execute`

Instead of picking a tool, an agent can state the intent and a priority.
Frugal classifies it onto a capability (URL and keyword cues —
deterministic, no model call) and routes under your policy. Captured from
a live zero-key run:

```
frugal__execute {"intent": "search for MCP server security best practices"}

  result › {
    "capability": "search",
    "provider_used": "youcom-free",
    "cost_usd": 0,
    "latency_ms": 594,
    "reason": "routed to a web search; policy=cheap: effective cost ascending; provider=youcom-free won on attempt 1",
    "results": [
      {"title": "MCP Server Security Best Practices for Safe AI Deployments", "url": "https://www.truefoundry.com/blog/mcp-server-security-best-practices", ...},
      {"title": "GitHub - slowmist/MCP-Security-Checklist", "url": "https://github.com/slowmist/MCP-Security-Checklist", ...},
      ...
    ]
  }
```

`priority: "cheap" | "balanced" | "premium"` maps onto the policies above
(`balanced` defers to your configured strategy). A URL intent runs an
extract and falls forward to a headless render when the page needs JS,
with the costs summed. The `reason` field is the receipt: what was
decided, under which policy, and which provider won on which attempt.
The direct tools (`frugal__search`, `frugal__extract`, `frugal__browse`)
remain for callers that already know the capability.

## See what you kept

Cost is Frugal's flagship policy, and the receipt is its proof. Every
call lands in a local ledger (`~/.frugal/usage`, JSONL, never leaves
your machine; `FRUGAL_STATS=off` disables it). `frugal stats` prints the
month's receipt:

```
frugal receipt · July 2026 (UTC)
────────────────────────────────────────────────
tool      provider            calls         paid
search    marginalia              1      $0.0000
search    wikipedia               1      $0.0000
extract   goreadability           1      $0.0000
────────────────────────────────────────────────
total                             3      $0.0000

same calls at premium rack rate*         $0.0060
you paid                                 $0.0000
────────────────────────────────────────────────
you saved                      $0.0060   (100%)
────────────────────────────────────────────────
* rack rate = list price of each capability's premium
  provider, snapshotted at call time. failed calls excluded.
```

Only the call that actually produced your result earns rack credit —
fallback attempts and zero-hit whiffs don't inflate the number.

## Set your keys (optional)

Add keys to unlock stronger paid providers. Frugal reads them from your
environment and only registers tools whose providers are configured:

```bash
# Search — frugal__search
export SEARXNG_URL=...           # free, self-hosted (You.com free tier, Marginalia, Wikipedia need no key)
export SERPER_API_KEY=...        # cheap paid
export BRAVE_API_KEY=...         # independent index (Brave Search), $5/mo credit
export YDC_API_KEY=...           # premium paid (You.com)

# Extract — frugal__extract (goreadability + Jina Reader are free, no key)
export JINA_API_KEY=...          # optional: lifts Jina Reader from 20 to 500 requests/min
                                 # (also add api_key_env: JINA_API_KEY to the jina entry)
export FIRECRAWL_API_KEY=...     # premium paid (anti-bot pages, PDFs)

# Browse — frugal__browse
export BROWSERLESS_TOKEN=...     # headless render
```

That's it. Restart your agent. Only the tools whose providers are
configured get registered.

## The routing table

Tool prices haven't fallen the way model prices have. You.com at $0.005/call
is 5× Serper at $0.001/call. SearXNG, running on your own machine, is free.

| Capability | Free / local | Cheap paid | Premium paid | Status |
|---|---|---|---|---|
| Search | **SearXNG** · **You.com free tier** · **Marginalia** · **Wikipedia** | **Serper** $0.001/call | **You.com** $0.005/call · **Brave** $0.005/call | **shipping** |
| Extract | **go-readability** (local) · **Jina Reader** (hosted JS render) | — | **Firecrawl** $0.001/page | **shipping** |
| Browse | local Playwright *(deferred)* | **Browserless** $0.002/render | Browserbase *(planned)* | *partial* || Code exec | local Docker | E2B ~$0.10/hr (2 vCPU) | Modal | planned |
| Embeddings | nomic-embed-text, bge-large | text-embedding-3-small $0.02/1M tok | 3-large, Voyage-3, Cohere | planned |
| Transcription | whisper.cpp | Deepgram Nova $0.0043/min | OpenAI Whisper $0.006/min | planned |

Under the `cheap` policy Frugal walks the columns left to right and you
keep the gap; `fast` and `premium` reorder the walk; `deny` fences
columns off entirely. Cost is one policy among several — but it's the
one with a receipt.

## What ships today

One MCP server, four tools, nine providers:

- **`frugal__execute`** — **shipping**. Describe the job (`intent`,
  optional `priority`); heuristic classification onto a capability, then
  policy-routed. Returns the full routing trace (`capability`,
  `provider_used`, `cost_usd`, `reason`).
- **`frugal__search`** — **shipping**. Routed across **SearXNG** (free,
  self-hosted), **You.com free tier** (`youcom-free`: free, public,
  hosted MCP, ~100 queries/day), **Marginalia** (free, public),
  **Wikipedia** (free, public), **Serper** (`$0.001/call`),
  **You.com** (`$0.005/call`), and **Brave Search** (`$0.005/call`,
  Brave's own independent index; every plan carries $5 of monthly
  credit, so `daily_budget_usd: 0.16` keeps light use inside it).  When a free provider returns zero hits the chain falls through to the
  next rung; a paid provider returning zero hits ends the chain (the
  query has no hits — no point paying a pricier provider to confirm).
- **`frugal__extract`** — **shipping**. Routed across **go-readability**
  (free, pure-Go local Readability), **Jina Reader** (free, hosted
  headless render: 20 requests/min keyless, 500 with a free `JINA_API_KEY`),
  and **Firecrawl** (`~$0.001/page`, anti-bot defenses and PDFs). The
  local pass runs first, so a static page never leaves your machine;
  only a page that renders its content with JavaScript reaches the
  hosted rung, and only when that also comes up empty does the chain
  spend a paid scrape. Jina receives the target URL, nothing else; set
  `jina: {enabled: false}` under `extract_providers` to keep extraction
  fully local.
- **`frugal__browse`** — *partial*. **Browserless** (`~$0.002/render`,
  headless Chrome) shipping; local Playwright deferred.
- **Routing policies** — **shipping**. Per-capability `strategy`
  (cheap / fast / premium), explicit `order`, `deny` lists.
- **Domain policy** — **shipping**. `allow` / `deny` host lists and
  `deny_private` for every fetch, enforced before a provider is called
  and applied to search results.
- **`frugal stats`** — the local savings receipt (see above).
- Stdio + Streamable HTTP transports. HTTP serves MCP 2026-07-28
  (stateless) and the session-based revisions before it on one endpoint;
  `--stateless` for load-balanced replicas; `ttlMs` cache hints on the
  tool catalog.
- HTTP transport supports bearer-token auth (`FRUGAL_AUTH_TOKEN`),
  per-IP rate limiting, and a `/metrics` endpoint (Prometheus text:
  `frugal_calls_total{tool=,provider=}` etc.).
- `frugal mcp install` writes the right config into Claude Desktop,
  Cursor, AnythingLLM, and Claude Code.

## Roadmap

- **Phase 3** — embeddings, transcription, code execution, local chat
  models, semantic cache.
- **Phase 4 — Frugal Cloud** *(not shipped — waitlist open)*. The binary
  stays local and self-hostable; Cloud adds the team layer on top:
  - Hosted policy management (edit routing policies in a dashboard,
    deploy to every app)
  - Team workspaces and shared policy templates
  - Usage analytics and cost reporting across the org
  - Provider health monitoring and routing traces
  - Org-wide API key management

  Everything in Phase 4 is roadmap, not product. The open router never
  requires it — no lock-in.
  [Join the waitlist](mailto:sparker@hey.com?subject=Frugal%20Cloud%20waitlist)

## From source

```bash
git clone https://github.com/brainsparker/frugal.git && cd frugal && make build
```

## License

[BUSL 1.1](./LICENSE) — self-hosting and internal commercial use are
permitted. Each release converts to Apache 2.0 four years after publication.
Plain-English summary in [LICENSE-BUSL-FAQ.md](./LICENSE-BUSL-FAQ.md).

## Security

Private vulnerability reports via [GitHub Security
Advisories](https://github.com/brainsparker/frugal/security/advisories/new).
Full policy in [SECURITY.md](./SECURITY.md).