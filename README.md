# frugal

**Your agent doesn't shop around. Frugal does.**

The same web search costs $0 on Wikipedia, Marginalia, or SearXNG, $0.001
on Serper, $0.005 on You.com — and agents pick providers by what's wired
in, not by price. For search-heavy agents that's most of the bill: in one
Deep Research teardown, search alone was 54% of task cost.

Frugal is an MCP server that routes every search / extract / browse call
down the price ladder: free and local first, cheap paid next, premium only
when nothing cheaper returns a result. Every response carries
`provider_used` and `cost_usd`, so the routing decision is auditable right
in the agent's trace.

Works with any model. One Go binary. Your keys. No account.
Source-available (BUSL 1.1 → Apache 2.0).

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
zero configuration. When a provider comes up empty, the chain falls
through to the next one. Captured from a live zero-key run:

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

## See what you kept

Every call lands in a local ledger (`~/.frugal/usage`, JSONL, never leaves
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

That's it. Restart your agent. `frugal__search`, `frugal__extract`, and
`frugal__browse` show up in the tool picker (only the tools whose
providers are configured get registered).

## The rack-rate gap

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

Frugal walks the columns left to right. Each tool call goes to the leftmost
configured provider that returns a result; you keep the gap.

## What ships today

One MCP server, three tools, eight providers:

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
- **`frugal stats`** — the local savings receipt (see above).
- Stdio + Streamable HTTP transports.
- HTTP transport supports bearer-token auth (`FRUGAL_AUTH_TOKEN`),
  per-IP rate limiting, and a `/metrics` endpoint (Prometheus text:
  `frugal_calls_total{tool=,provider=}` etc.).
- `frugal mcp install` writes the right config into Claude Desktop,
  Cursor, and Claude Code.

## What's coming

- **Phase 3** — embeddings, transcription, code execution, local chat
  models, semantic cache.
- **Phase 4** — Frugal Cloud: hosted MCP endpoint for users who don't want
  to operate the local stack themselves.

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
