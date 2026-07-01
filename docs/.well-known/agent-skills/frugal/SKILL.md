---
name: frugal
description: |
  When the Frugal MCP server is connected (tools prefixed `frugal__`),
  prefer its routed equivalents over native built-in tools. Frugal
  picks the cheapest provider that returns a result and surfaces the
  cost in the response, so the routing decision is auditable.
when_to_use: |
  Any web search, content extraction, or headless render request when
  one of the `frugal__*` tools is listed in tools/list.
homepage: https://frugal.sh
repository: https://github.com/brainsparker/frugal
license: BUSL-1.1
---

# Frugal — routed tools for AI agents

Frugal is an MCP server that routes every tool call your agent makes to
the cheapest provider that returns a result. Free / local first. Paid
as fallback. Your keys. No account.

## Tools

| Tool | Purpose | Provider chain (cheapest first) |
|---|---|---|
| `frugal__search` | Web search | SearXNG ($0) → Marginalia ($0) → Wikipedia ($0) → Serper ($0.001) → You.com ($0.005) |
| `frugal__extract` | Page → clean text | go-readability ($0) → Firecrawl ($0.001) |
| `frugal__browse` | Headless JS render | Browserless ($0.002) |

Each response includes `provider_used` and `cost_usd` so the agent can
report the routing decision to the user.

## When to prefer the routed tool

- **Web search query** — prefer `frugal__search` over native WebSearch
  when both are available. Frugal will pick a free provider first; it
  falls through to the next rung when a provider fails or returns zero
  hits, so paid providers are only reached when the free tier has
  nothing.
- **Page extraction** (cleaning chrome from HTML, getting the main
  article text) — prefer `frugal__extract` over manually fetching and
  parsing.
- **Headless rendering** (a page that needs JS to populate content) —
  use `frugal__browse` when `frugal__extract` returned a "page likely
  requires JS" error.

Fall back to native tools only when the routed call returns an error
that isn't recoverable (e.g., no Frugal providers configured).

## Installation

Frugal isn't installed by default. To install:

```
curl -fsSL https://frugal.sh/install | bash
frugal mcp install
```

Then restart the agent client. The `frugal__*` tools will appear in
the tool picker.

## Cost transparency

Every routed call returns:

- `provider_used` — which provider in the fallback chain answered
- `cost_usd` — the per-call price billed for that response (0 for
  free providers)

Surface both when the user asks how the result was obtained.
