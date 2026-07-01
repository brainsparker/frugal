# frugal-mcp

> Cost-optimized MCP server. Routes every tool call to the cheapest provider that returns a result — free / local first, paid as fallback.

```bash
npx frugal-mcp mcp install
```

That installs Frugal as an MCP server in any agent client present on this machine — Claude Desktop, Cursor, or Claude Code — and wires it idempotently.

This npm package is a thin Node wrapper. On first run it downloads the matching Go binary from the [GitHub release](https://github.com/brainsparker/frugal/releases) (darwin/linux, arm64/amd64), verifies its sha256 against the `SHA256SUMS` file fetched over TLS from the same release, and caches it under `~/.cache/frugal-mcp/<version>/`. Subsequent invocations exec the cached binary directly. Release artifacts are also cosign-signed, but this wrapper does not verify signatures — use the [curl installer](https://github.com/brainsparker/frugal#install) if you want that.

## What ships

- `frugal__search` — routed across SearXNG, Marginalia (both free), Serper, and You.com.
- `frugal__extract` — routed across go-readability (free, local) and Firecrawl.
- `frugal__browse` — Browserless.

Free providers work without any API keys. Add `SERPER_API_KEY`, `YDC_API_KEY`, `FIRECRAWL_API_KEY`, or `BROWSERLESS_TOKEN` in your shell to enable the paid fallbacks.

Source, full docs, and the rack-rate gap: <https://github.com/brainsparker/frugal> · <https://frugal.sh>

## License

BUSL 1.1 — self-hosting and internal commercial use are permitted; each release converts to Apache 2.0 four years after publication.
