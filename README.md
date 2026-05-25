# frugal

**Tool calls are the new tokens.**

For most agentic workloads the tool bill exceeds the model bill. Frugal is
an MCP server that routes every tool call your agent makes to the cheapest
provider that returns a result — free / local first, paid only as fallback,
premium only when you opt in.

Works with any model. One signed Go binary. Your keys. No account.
Source-available (BUSL 1.1 → Apache 2.0).

[frugal.sh](https://frugal.sh) · [Strategy](./STRATEGY.md)

## Install

```bash
curl -fsSL https://frugal.sh/install | bash
frugal mcp install
```

The first command drops the binary in your `$PATH`. The second auto-detects
Claude Desktop, Cursor, and Claude Code and merges `frugal` into each
configured MCP server list.

### Verified, signed, reproducible

Releases are signed with [cosign](https://docs.sigstore.dev/cosign/)
in keyless mode by the GitHub Actions workflow that builds them, and
the binary is built reproducibly (`-trimpath`, `-buildvcs=false`,
`CGO_ENABLED=0`, pinned Go toolchain). Two builds of the same tag
produce byte-identical binaries — you can rebuild any release yourself
and check the hash matches.

The installer verifies the signature and the SHA-256 checksum before
moving the binary into place; it skips the cosign step (loudly) only
if `cosign` isn't on your `PATH`. To verify manually:

```bash
cosign verify-blob \
  --bundle SHA256SUMS.sig \
  --certificate-identity-regexp \
    'https://github.com/brainsparker/frugal/\.github/workflows/release\.yml@refs/tags/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  SHA256SUMS

sha256sum -c SHA256SUMS  # macOS: shasum -a 256 -c SHA256SUMS
```

### Audit before you serve

Want to see exactly which providers `frugal` would route to (and which
env vars gated each in or out) before it talks to a single network?

```bash
frugal mcp serve --dry-run
```

Prints the full route plan — capability, provider, cost, reason — then
exits. No drivers are instantiated, no requests are served, no network
is reached.

## Set your keys

BYOK. Frugal reads provider credentials from your environment:

```bash
# Search — frugal__search
export SEARXNG_URL=...           # free, self-hosted (also Marginalia free, no key)
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
| Search | **SearXNG** · **Marginalia** | **Serper** $0.001/call | **You.com** $0.005/call | shipping |
| Extract | **go-readability** (local) | — | **Firecrawl** $0.001/page | shipping |
| Browse | local Playwright *(deferred)* | **Browserless** $0.002/render | Browserbase | partial |
| Code exec | local Docker | E2B ~$0.10/hr (2 vCPU) | Modal | planned |
| Embeddings | nomic-embed-text, bge-large | text-embedding-3-small $0.02/1M tok | 3-large, Voyage-3, Cohere | planned |
| Transcription | whisper.cpp | Deepgram Nova $0.0043/min | OpenAI Whisper $0.006/min | planned |

Frugal walks the columns left to right. Each tool call goes to the leftmost
configured provider that returns a result; you keep the gap.

## What ships today

One MCP server, three tools, seven providers:

- **`frugal__search`** — routed across **SearXNG** (free, self-hosted),
  **Marginalia** (free, public), **Serper** (`$0.001/call`), and
  **You.com** (`$0.005/call`).
- **`frugal__extract`** — routed across **go-readability** (free, pure-Go
  local Readability) and **Firecrawl** (`~$0.001/page`, JS-rendered).
- **`frugal__browse`** — **Browserless** (`~$0.002/render`, headless
  Chrome). Local Playwright deferred.
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

Roadmap and rationale in [STRATEGY.md](./STRATEGY.md).

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
