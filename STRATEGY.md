# Frugal product strategy

> Canonical positioning. As of 2026-05-18.

## 1. Thesis

**Tool calls are the new tokens.**

For most agentic workloads in 2026, the tool bill exceeds the model bill.
A single Firecrawl scrape costs as much as the gpt-4o-mini call that
consumes its output. A Tavily search at $0.008/call is 8× a Serper search
at $0.001/call; a self-hosted SearXNG returns the same result for $0.
An agent that loops on search → extract → reason × 10 iterations can run
a $0.50 tool bill on top of a $0.005 model bill.

Frugal is **the cost-arbitrage layer for tool calls in agentic
workflows**. For every search, extraction, browse, code-exec, embedding,
or transcription request, Frugal picks the cheapest provider that
returns acceptable results — $0 / local providers tried first, paid
providers as fallback, premium providers only when explicitly
authorized. Works with any model: local OSS, cheap cloud
(gpt-4o-mini / haiku / flash / deepseek), or frontier.

## 2. The wedge

The AI infrastructure market splits cleanly:

- **Model routing** (OpenRouter, LiteLLM, Helicone, Portkey) is crowded
  and converging on a few API gateways with thin margins.
- **Tool providers** (Firecrawl, Tavily, Exa, Browserbase, You.com,
  Composio, Pipedream) compete on quality, recall, and latency — none
  compete on cost. Tool prices haven't fallen the way model prices have.
- **Tool *routing*** is nearly empty. No one is routing tool calls
  across multiple providers and preferring free/local first.

Frugal's wedge: **be the only tool router whose optimization function
is cost, with free/local as a first-class provider rank.** Defensible
because tool prices are sticky, free/local options are real and
improving (SearXNG, Trafilatura, Playwright, nomic-embed, whisper.cpp),
and the OSS router builds trust the closed-source alternatives can't
match.

## 3. Audience

Three concentric rings, all served by the same product:

**Primary — cost-conscious agent builders running cheap cloud models.**
gpt-4o-mini, claude-haiku, gemini-flash, deepseek-v3 users whose model
bill is small and whose tool bill is the actual line item.

**Showcase — local-model homelabbers and researchers.** Running Kimi K2,
Qwen 2.5, Llama 3.3 on serious hardware. Total marginal cost approaches
$0. Loudest community.

**Onramp — anyone running agentic workflows at scale.** Frontier-model
users still save on the tool layer.

## 4. The rack-rate gap

The headline artifact. Frugal picks the leftmost column that's
configured; users keep the gap.

| Capability | Free / local ($0) | Cheap paid | Premium paid (avoid by default) |
|---|---|---|---|
| Search | **SearXNG** (self-host) | **Serper** ($0.001/call list) | **You.com** ($0.005), **Exa** ($0.007), **Tavily** ($0.008) |
| Extract | **Trafilatura**, **readability.js**, **Mercury** (self-host) | — | **Firecrawl** ($0.001–0.005/page) |
| Browse | **Playwright** + Chromium (local) | **Browserless** (~$0.002/30s) | **Browserbase** ($0.10/hr, ~$0.002/min) |
| Code-exec | **Local Docker** | **E2B** (~$0.10/hr, 2 vCPU) | Modal (~$0.14/hr CPU, +GPU rates) |
| Embeddings | **nomic-embed-text**, **bge-large** (local) | **text-embedding-3-small** ($0.02/1M tokens) | text-embedding-3-large, Voyage-3, Cohere |
| Transcription | **whisper.cpp** (local) | **Deepgram Nova-3** ($0.0043/min pre-recorded) | OpenAI Whisper API ($0.006/min), Speechmatics |

The "premium paid" column isn't wrong — those providers exist for a
reason. They're the wrong default for cost optimization. Frugal makes
them opt-in, not opt-out.

## 5. The routing rule

```
For each tool call:
  candidates = providers configured for this capability, ordered by cost (ascending)
  for provider in candidates:
    result = provider.call(args, timeout)
    if result.success:
      return result
  raise NoAcceptableProviderError
```

Free/local providers are first-class. A configured SearXNG instance
ranks ahead of Serper because $0 < $0.001. The MCP tool handler
implements this against the `internal/search.Searcher` interface;
future tool handlers (extract, browse, …) follow the same pattern.

## 6. Product surface

**One surface: the MCP server.** Frugal exposes routed tools
(`frugal__search` today; `frugal__extract`, `frugal__browse`,
`frugal__chat`, … coming) over the
[Model Context Protocol](https://modelcontextprotocol.io). Agent stacks
— Claude Desktop, Cursor, Claude Code, custom MCP hosts — call those
tools; the routing decision happens server-side inside each
`tools/call`. Stdio is default; Streamable HTTP behind `--http :PORT`.

The CLI has exactly two verbs: `frugal mcp install` (writes config into
detected agent clients) and `frugal mcp serve` (runs the server).
Nothing else. v1.0 deliberately ships no `frugal run`, `frugal route`,
`frugal compare`, `frugal bench` — those carried recipe-layer
machinery from the v3-v5 proxy era and didn't earn their place under
the tool-routing thesis.

## 7. Component status

| Component | Free / local | Cheap paid | Premium paid | Status |
|---|---|---|---|---|
| Search | SearXNG | Serper | You.com, Exa, Tavily | **Serper + Tavily shipping; SearXNG Phase 2** |
| Extract | Trafilatura, readability.js | — | Firecrawl | **Phase 2** |
| Browse | local Playwright | Browserless | Browserbase | **Phase 2** |
| Embeddings | nomic-embed, bge-large | text-embedding-3-small | 3-large, Voyage | **Phase 3** |
| Transcription | whisper.cpp | Deepgram | OpenAI Whisper, Speechmatics | **Phase 3** |
| Code-exec | local Docker | E2B | Modal | **Phase 3** |
| Semantic cache | sqlite + similarity (local) | — | — | **Phase 3** |
| Chat | local Ollama / LM Studio | mini / nano / flash | frontier | **Phase 3 (`frugal__chat`)** |

## 8. OSS / paid split

Same shape as PostHog / Sentry / Cockroach — OSS is the funnel; paid is
the cash register.

| Tier | Form | Customer | Revenue mechanic |
|---|---|---|---|
| **OSS — Tier 0** | Self-hosted MCP server, BUSL 1.1 → Apache 2.0 | Local-first builders, evaluators, audit-friendly orgs | $0. Sells nothing; markets everything. |
| **Frugal Cloud — Tier 1** *(planned)* | Hosted MCP endpoint at `api.frugal.sh`; Frugal-issued API key | Users who want the routing without operating SearXNG / Playwright / Trafilatura themselves | At-or-below the cheap-paid column. Frugal keeps the volume discount + the routing decision edge. **Competes with Tavily / Firecrawl / Browserbase on price**, not with OpenRouter on aggregation. |
| **Enterprise ZDR — Tier 2** *(planned)* | Customer self-hosts routing + dashboard inside their VPC; Frugal-the-company never sees their traffic | Regulated industries (fintech, healthcare, gov) | License + support contract. |

The Cloud tier is deliberately *not* the OpenRouter shape. That market
is crowded. Frugal Cloud is a **managed cheap-tools service** — search,
extract, browse, embeddings, transcription routed across providers at
list pricing, beating Tavily and Firecrawl on cost because Frugal both
takes volume discounts and prefers free/local when configured.

## 9. Roadmap

### Phase 2 — local providers + free/local-first routing (next)

The architectural unlock. Required for the bold $0 claim to be real.

- `internal/provider/searxng` — driver against a user-configured SearXNG.
- `internal/provider/playwright` — local headless Chromium.
- `internal/provider/trafilatura` — URL → clean text.
- `internal/provider/firecrawl` + `internal/provider/browserless` — the
  cheap-paid and premium-paid extract/browse drivers.
- `frugal__extract` and `frugal__browse` MCP tools.

### Phase 3 — Embeddings, transcription, code-exec, chat

- `frugal__embed`, `frugal__transcribe`, `frugal__exec`, `frugal__chat`.
- Local drivers (nomic-embed, whisper.cpp, local Docker, Ollama) and
  the cheap-paid + premium-paid tiers per the rack-rate table.
- Semantic cache as a separate `frugal__cache_lookup` tool that
  precedes any other tool call.

### Phase 4 — Frugal Cloud

- `api.frugal.sh` endpoint. Same Go binary at the edge with auth +
  rate-limit + Stripe usage-based billing.
- One-key API for users who don't want to operate the OSS tier.

### Phase 5 — Enterprise ZDR

- Customer-hosted routing + dashboard. No data leaves their VPC.

## 10. TBD

**Frugal Cloud pricing.** Pure pass-through at list price + keep the
volume discount? Sub-list to undercut Tavily / Firecrawl directly?
Per-month subscription tiers? Resolve before Cloud launch.

**Hardware for local-model dogfooding.** The showcase audience runs
models on serious hardware (M-series 64–128GB, A100s). Frugal HQ does
not. Defaulting to (a) ship for cloud-model users first, (b) find a
homelab dogfooder for the local-model demo runs, (c) procure hardware
once Cloud revenue justifies it.
