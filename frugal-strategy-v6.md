# Frugal product strategy (v6)

> Canonical positioning. Supersedes [`frugal-strategy-v5.md`](./frugal-strategy-v5.md).
> As of 2026-05-18.

## 1. Thesis

**Tool calls are the new tokens.**

For most agentic workloads in 2026, the tool bill exceeds the model bill.
A single Firecrawl scrape costs as much as the GPT-4o-mini call that
consumes its output. A Tavily search at $0.008/call is 8× a Serper search
at $0.001/call at list price; a self-hosted SearXNG returns the same
result for $0. An agent that loops on search → extract → reason × 10
iterations can run a $0.50 tool bill on top of a $0.005 model bill.

Frugal is **the cost-arbitrage layer for tool calls in agentic
workflows**. For every search, extraction, browse, code-exec, embedding,
or transcription request, Frugal picks the cheapest provider that
returns acceptable results — $0 / local providers tried first, paid
providers as fallback, premium providers only when explicitly
authorized. Works with any model: local OSS, cheap cloud
(gpt-4o-mini / haiku / flash / deepseek), or frontier.

**The router IS the benchmark.** The same per-recipe quality bar that
decides "did the tool return a usable result?" in CI is the rule that
runs inside `frugal mcp serve` on every live request.

## 2. The wedge

The AI infrastructure market splits cleanly:

- **Model routing** (OpenRouter, LiteLLM, Helicone, Portkey, Martian) is
  crowded and converging on a few API gateways with thin margins.
- **Tool providers** (Firecrawl, Tavily, Exa, Browserbase, Composio,
  Pipedream) compete on quality, recall, and latency — none of them
  compete on cost. Tool prices have not fallen the way model prices have.
- **Tool *routing*** is nearly empty. No one is routing tool calls across
  multiple providers and preferring free/local options.

Frugal's wedge: **be the only tool router whose optimization function is
cost, with free/local as a first-class provider rank.** That's
defensible because:

1. Tool prices are sticky (providers don't price-cut each other yet).
2. Free/local options are real and improving (SearXNG, Trafilatura,
   Playwright, nomic-embed, whisper.cpp, local Docker).
3. The cost-only thesis is opinionated — most products lie about it and
   say "we balance cost / quality / latency." Frugal optimizes cost;
   quality and latency are constraints, not goals.
4. The OSS router builds trust the closed-source alternatives can't
   match.

## 3. Audience

Three concentric rings, all served by the same product:

**Primary — cost-conscious agent builders running cheap cloud models.**
gpt-4o-mini, claude-haiku, gemini-flash, deepseek-v3 users whose model
bill is small and whose tool bill is the actual line item. Indie
hackers, OSS maintainers, small startups, internal-tooling teams. This
is the addressable market Frugal can serve and dogfood today.

**Showcase — local-model homelabbers and researchers.** Running Kimi K2,
Qwen 2.5, Llama 3.3 on M-series Macs with 64–128GB unified memory, or
GPU rigs with A100s / 4090s. Total marginal cost approaches $0 — model
is free, tools are free. Frugal's most extreme value prop lives here.
Smaller TAM than the primary ring; loudest community.

**Onramp — anyone running agentic workflows at scale.** Frontier-model
users (opus, gpt-4o) still save on the tool layer even if they don't
save on the model. The savings compound with loop count.

## 4. The rack-rate problem

The single strongest piece of evidence for the product is the price gap
between tool providers — and the gap between paid and free/local. This
table is the headline artifact on the homepage:

| Capability | Free / local ($0) | Cheap paid | Premium paid (avoid by default) |
|---|---|---|---|
| Search | **SearXNG** (self-host) | **Serper** ($0.001/call list) | **You.com** ($0.005), **Exa** ($0.007), **Tavily** ($0.008) |
| Extract | **Trafilatura**, **readability.js**, **Mercury** (self-host) | — | **Firecrawl** ($0.001–0.005/page) |
| Browse | **Playwright** + Chromium (local) | **Browserless** (~$0.002/unit, 30s) | **Browserbase** ($0.10/hr, ~$0.002/min) |
| Code-exec | **Local Docker** | **E2B** (~$0.10/hr, 2 vCPU) | Modal (~$0.14/hr CPU, +GPU rates) |
| Embeddings | **nomic-embed-text**, **bge-large** (local) | **text-embedding-3-small** ($0.02/1M tokens) | text-embedding-3-large, Voyage-3, Cohere |
| Transcription | **whisper.cpp** (local) | **Deepgram Nova-3** ($0.0043/min pre-recorded) | OpenAI Whisper API ($0.006/min), Speechmatics |
| Chat (small) | **Ollama / LM Studio** (local OSS) | gpt-4o-mini ($0.15/1M in, $0.60/1M out), haiku, gemini-flash | gpt-4o, claude-sonnet |
| Chat (large) | Kimi K2, Qwen 72B (local on serious HW) | — | gpt-4o, claude-opus, gemini-pro |

**Frugal picks the leftmost column that works for the recipe; you save
the difference.** The "premium paid" column isn't wrong — those
providers exist for a reason. But they're the wrong default for cost
optimization. Frugal makes them opt-in, not opt-out.

## 5. The routing rule

The core algorithm, formalized:

```
For each tool call:
  candidates = providers configured for this capability, ordered by cost (ascending)
  for provider in candidates:
    result = provider.call(args, timeout)
    if result.success and result.meets_recipe_quality_bar:
      return result
  raise NoAcceptableProviderError
```

The quality bar is per-recipe and intentionally simple in v1:

- **Search**: returned ≥ N results (recipe-defined, default 1).
- **Extract**: returned non-empty text body.
- **Browse**: page loaded without timeout, returned HTML.
- **Code-exec**: exit code 0 OR captured useful stderr.
- **Embeddings**: returned vector of expected dimensionality.
- **Transcription**: returned text with confidence above threshold.

Recipe authors can override the quality bar per step (e.g.,
`search: min_results: 5, max_age_days: 7`). When the bar is unmet,
fallback proceeds down the cost ranking. When all providers fail, the
tool returns an error to the agent (no silent fallback to premium
unless the recipe explicitly opts in).

**Free/local providers are first-class.** A configured SearXNG instance
ranks ahead of Serper because $0 < $0.001. The implementation already
handles cost ordering; the only new behavior is honoring `$0` as the
canonical floor.

## 6. The recipe model

Recipes are deterministic step lists, same as v5. The new emphasis is
on the providers each tool step can dispatch to.

Today's five shipping recipes carry forward:

- `fresh-facts` — search + small hosted model
- `research-synthesis` — long-context reasoner (search + rerank in Phase 2)
- `code-dev` — routed coder model
- `factual-qa` — cheapest mini-tier model
- `structured-extraction` — cheapest JSON-mode model

New recipes to ship in Phase 2 as the local providers land:

- `scrape-page` — local Playwright + Trafilatura, fallback to Firecrawl
- `transcribe-audio` — whisper.cpp local, fallback to Deepgram
- `code-task` — local Docker exec, fallback to E2B
- `web-research` — SearXNG → Playwright/Trafilatura → cheap chat

## 7. Product surfaces

Two primary surfaces share one routing engine.

| Surface | Status | Audience |
|---|---|---|
| **CLI** (`frugal run / route / compare`) | Shipping | Humans, scripts, CI, evaluation |
| **MCP server** (`frugal mcp serve` + `mcp install`) | Shipping | Agent stacks (Claude Code, Cursor, Claude Desktop, custom MCP hosts) |

The CLI is the demo, eval, and scripting surface. The MCP server is
where the product lives in production. `frugal mcp install` writes the
right config into each detected agent client in one command.

The OpenAI-compatible HTTP proxy from v0.x was removed in v1.0 and stays
removed — see v5 §10 for the rationale.

## 8. Component status

The architectural inversion in v6: **free/local providers are
first-class, not "Phase 3 planned."** Cost-only routing requires the
$0 tier to exist.

Vocabulary:
- **Shipping** — routes live traffic today.
- **Phase 2 (next)** — wired in the next iteration.
- **Planned** — on the roadmap.

| Component | Free / local | Cheap paid | Premium paid (gated) | Status |
|---|---|---|---|---|
| Search | SearXNG | Serper | Tavily, Exa, You.com | **Serper shipping; SearXNG Phase 2** |
| Extract | Trafilatura, readability.js | — | Firecrawl | **Phase 2** |
| Browse | local Playwright | Browserless | Browserbase | **Phase 2** |
| Hosted chat | local Ollama / LM Studio | mini / nano / flash | frontier | **Cloud chat shipping (internal); local Phase 3** |
| Code-exec | local Docker | E2B | Modal | **Planned** |
| Embeddings | nomic-embed, bge-large | text-embedding-3-small | text-embedding-3-large, Voyage | **Planned** |
| Transcription | whisper.cpp | Deepgram | OpenAI Whisper, Speechmatics | **Planned** |
| Semantic cache | sqlite + similarity (local) | — | — | **Planned** |
| Multi-step agent | local agent harness | — | — | **Planned (`frugal run --agent`)** |

## 9. OSS / paid split

Three tiers, distinct customer + revenue mechanics. Same shape as
PostHog / Sentry / Cockroach — OSS is the funnel; paid is the cash
register.

| Tier | Form | Customer | Revenue mechanic |
|---|---|---|---|
| **OSS — Tier 0** | Self-hosted CLI + MCP server, BUSL 1.1 → Apache 2.0 | Local-first builders, evaluators, audit-friendly orgs | $0. Sells nothing; markets everything. |
| **Frugal Cloud — Tier 1** *(planned)* | Hosted MCP endpoint at `api.frugal.sh`; Frugal-issued API key; one binary speaks both modes via `FRUGAL_CLOUD=1` | Users who want the routing without operating SearXNG / Playwright / Trafilatura themselves | At-or-below the cheap-paid column. Frugal keeps the volume discount + the routing decision edge. **Competes with Tavily / Firecrawl / Browserbase on price**, not with OpenRouter on aggregation. |
| **Enterprise ZDR — Tier 2** *(planned)* | Customer self-hosts routing + dashboard inside their VPC; Frugal-the-company never sees their traffic | Regulated industries (fintech, healthcare, gov) with strict no-data-leaves-VPC rules | License + support contract. Carries forward from v5. |

The Cloud tier deliberately is **not** the OpenRouter shape (one API key
for every model with a small markup). That market is crowded. Frugal
Cloud is a **managed cheap-tools service** — search, extract, browse,
embeddings, transcription routed across providers at list pricing,
beating Tavily and Firecrawl on cost because Frugal both takes volume
discounts and prefers free/local when configured.

The Cloud tier is also the only path to monetize the local-model
audience indirectly — they may not use Cloud themselves, but they bring
the credibility and word-of-mouth that drives the broader audience to
Cloud.

## 10. Telemetry data plane

Same as v5 §7. Opt-in aggregates per `(recipe, step, provider)` tuple.
No prompts, no tool inputs/outputs, no URLs, no provider keys, no
hostname. Free path has zero per-instance retention. Paid path retains
per-customer for 90 days.

The telemetry roll-up powers the public benchmark (eventually) and the
ZDR enterprise dashboard. It also feeds the Cloud routing decisions —
aggregate data on which provider returns which quality on which task
class is what makes routing decisions defensible over time.

## 11. Roadmap

Three threads. Each component ships only when the eval supports it.

### Phase 2 — local providers + $0-first routing (next)

The architectural unlock. Required for the v6 thesis to be real.

1. **`internal/provider/searxng/`** — driver against a user-configured
   SearXNG instance. Auto-detects via `SEARXNG_URL` env.
2. **`internal/provider/playwright/`** — local headless Chromium via
   a small Node sidecar or pure-Go Chrome DevTools Protocol client.
3. **`internal/provider/trafilatura/`** — Python sidecar or pure-Go
   readability port for URL → clean text.
4. **`internal/provider/firecrawl/`** + **`internal/provider/browserless/`**
   — the cheap-paid and premium-paid extract/browse drivers.
5. **`internal/search.RouteCheapest` → `RouteCheapestWithQuality`** —
   honor recipe-level quality bars; iterate down the cost ranking until
   one provider's result clears the bar.
6. **New recipes**: `scrape-page`, `web-research`.
7. **Rack-rates page section** — proof artifact on the homepage.

### Phase 3 — Embeddings, transcription, code-exec, local chat

- `internal/provider/nomic/` and `internal/provider/openai-embed/` for
  embeddings routing.
- `internal/provider/whisper-cpp/` and `internal/provider/deepgram/` for
  transcription.
- `internal/provider/docker-exec/` and `internal/provider/e2b/` for
  code execution.
- `internal/provider/ollama/` for local chat (the showcase audience's
  inference layer).

### Phase 4 — Frugal Cloud

- `api.frugal.sh` endpoint. Same Go binary at the edge, with auth +
  rate-limit + billing middleware ported from the (now deleted)
  `internal/proxy` middleware history.
- Stripe integration for usage-based billing.
- One-key API for users who don't want to operate the OSS tier.

### Phase 5 — Multi-step agent harness + enterprise dashboard

- `frugal run --agent` mode (cheap-model agent loop with Frugal tools).
- ZDR enterprise dashboard (carries forward from v5).

## 12. TBD

**Q1 — Frugal Cloud pricing.** Pure pass-through at list price + keep
the volume discount? Sub-list pricing on the most popular routes to
undercut Tavily / Firecrawl directly? Per-month subscription tiers with
included quota? Resolve before Cloud launch.

**Q2 — Quality bar formalization.** v6 §5 defines simple per-capability
bars. Real workloads probably need per-recipe overrides and may want a
"second-opinion" quality check (e.g., ask a cheap LLM to grade the
result). Defer until the local-provider integrations reveal the actual
quality variance.

**Q3 — Hardware for local-model dogfooding.** The "showcase" audience
runs models on serious hardware (M-series Mac Studio 64–128GB, A100s).
Frugal HQ does not. Options: (a) defer local-model end-to-end testing
to early users, (b) rent A100 time on Modal ($1.50/hr) for validation
runs, (c) procure hardware once Cloud revenue covers it. Defaulting to
(a) + (b) until (c) is justified.

---

*This document is the canonical positioning for Frugal as of 2026-05-18.
Changes go through revision (v6 → v7) rather than in-place edits, so
the conversation history stays auditable.*
