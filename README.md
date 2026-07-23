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
Claude Desktop, Cursor, and Claude Code and merges `frugal` into each
configured MCP server list.

## Try it now (no keys)

Search and extract work out of the box: **Marginalia** (free index of the
indie / non-commercial web), **Wikipedia** (free Wikimedia REST search),
and **go-readability** (free, pure-Go local extractor) ship enabled with
zero configuration. This is the default `cheap` policy at work — free and
local rungs first, failover when a provider comes up empty. Captured from
a live zero-key run:

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

Honest limits: Marginalia is genuinely good for essays, docs, blogs, and
niche technical writing, and weak on mainstream news and product pages;
Wikipedia covers entities and reference topics. Zero-key mode is a real
workflow for research and local extraction — it is not a Google-grade
SERP. One env var changes that: `SEARXNG_URL` (free, self-hosted) or
`SERPER_API_KEY` ($0.001/call) gives the chain a stronger rung to fall to.

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

## Describe the job: `frugal__execute`

Instead of picking a tool, an agent can state the intent and a priority.
Frugal classifies it onto a capability (URL and keyword cues —
deterministic, no model call) and routes under your policy. Captured from
a live zero-key run:

```
frugal__execute {"intent": "search for MCP server security best practices"}

  stderr › search zero hits; falling back  provider=marginalia latency_ms=273
  result › {
    "capability": "search",
    "provider_used": "wikipedia",
    "cost_usd": 0,
    "latency_ms": 650,
    "reason": "routed to a web search; policy=cheap: effective cost ascending; provider=wikipedia won on attempt 2",
    "results": [
      {"title": "ChatGPT", "url": "https://en.wikipedia.org/wiki/ChatGPT", ...},
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
export SEARXNG_URL=...           # free, self-hosted (Marginalia + Wikipedia need no key)
export SERPER_API_KEY=...        # cheap paid
export YDC_API_KEY=...           # premium paid (You.com)

# Extract — frugal__extract (goreadability is free, no key)
export FIRECRAWL_API_KEY=...     # premium paid (JS-rendered pages)

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
| Search | **SearXNG** · **Marginalia** · **Wikipedia** | **Serper** $0.001/call | **You.com** $0.005/call | **shipping** |
| Extract | **go-readability** (local) | — | **Firecrawl** $0.001/page | **shipping** |
| Browse | local Playwright *(deferred)* | **Browserless** $0.002/render | Browserbase *(planned)* | *partial* |
| Code exec | local Docker | E2B ~$0.10/hr (2 vCPU) | Modal | planned |
| Embeddings | nomic-embed-text, bge-large | text-embedding-3-small $0.02/1M tok | 3-large, Voyage-3, Cohere | planned |
| Transcription | whisper.cpp | Deepgram Nova $0.0043/min | OpenAI Whisper $0.006/min | planned |

Under the `cheap` policy Frugal walks the columns left to right and you
keep the gap; `fast` and `premium` reorder the walk; `deny` fences
columns off entirely. Cost is one policy among several — but it's the
one with a receipt.

## What ships today

One MCP server, four tools, eight providers:

- **`frugal__execute`** — **shipping**. Describe the job (`intent`,
  optional `priority`); heuristic classification onto a capability, then
  policy-routed. Returns the full routing trace (`capability`,
  `provider_used`, `cost_usd`, `reason`).
- **`frugal__search`** — **shipping**. Routed across **SearXNG** (free,
  self-hosted), **Marginalia** (free, public), **Wikipedia** (free,
  public), **Serper** (`$0.001/call`), and **You.com** (`$0.005/call`).
  When a free provider returns zero hits the chain falls through to the
  next rung; a paid provider returning zero hits ends the chain (the
  query has no hits — no point paying a pricier provider to confirm).
- **`frugal__extract`** — **shipping**. Routed across **go-readability**
  (free, pure-Go local Readability) and **Firecrawl** (`~$0.001/page`,
  JS-rendered).
- **`frugal__browse`** — *partial*. **Browserless** (`~$0.002/render`,
  headless Chrome) shipping; local Playwright deferred.
- **Routing policies** — **shipping**. Per-capability `strategy`
  (cheap / fast / premium), explicit `order`, `deny` lists.
- **`frugal stats`** — the local savings receipt (see above).
- Stdio + Streamable HTTP transports.
- HTTP transport supports bearer-token auth (`FRUGAL_AUTH_TOKEN`),
  per-IP rate limiting, and a `/metrics` endpoint (Prometheus text:
  `frugal_calls_total{tool=,provider=}` etc.).
- `frugal mcp install` writes the right config into Claude Desktop,
  Cursor, and Claude Code.

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
