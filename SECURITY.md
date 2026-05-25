# Security Policy

Frugal is a local-first MCP server that handles provider API keys on
behalf of the operator. We take security reports seriously and aim to
respond within 72 hours.

## Reporting a vulnerability

**Please do not open a public GitHub issue for security-sensitive reports.**

Use GitHub's private vulnerability reporting to open a draft advisory against
this repository:

https://github.com/brainsparker/frugal/security/advisories/new

If you cannot use GitHub advisories, email **security@frugal.sh** with:

- A description of the vulnerability and its impact.
- Reproduction steps or a proof-of-concept.
- The affected version(s) and environment.

We will acknowledge receipt within 72 hours and coordinate a fix, disclosure
window, and CVE if applicable.

## Threat model

This section describes what Frugal sees, what it doesn't, what trust it
asks the operator to extend, and the assumptions the design rests on.
If a deployment violates one of the assumptions, the security properties
below do not hold.

### What Frugal sees

- **Provider API keys** read from process environment variables at startup
  (`SERPER_API_KEY`, `YDC_API_KEY`, `FIRECRAWL_API_KEY`, `BROWSERLESS_TOKEN`)
  and a self-hosted endpoint URL (`SEARXNG_URL`).
- **Tool-call arguments** sent by the MCP client — search queries, URLs to
  extract, URLs to browse. These are forwarded to the upstream provider
  Frugal selects for the call.
- **Upstream responses** — search hits, extracted page text, rendered HTML.
  Forwarded back to the MCP client unchanged except for the routing
  footer (`provider_used`, `cost_usd`, `latency_ms`).

### What Frugal does not see and does not retain

- **No model API keys.** Frugal does not call any LLM; the MCP client
  owns the model relationship. `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` /
  `GOOGLE_API_KEY` are never read by Frugal.
- **No request bodies are logged.** The Prometheus `/metrics` surface
  records counts, latencies, and cost — not query text, not URLs, not
  response content.
- **No persistent storage.** Frugal writes no database, no cache, no
  on-disk log of requests. Restarting the process discards in-memory
  metrics.
- **No phone-home telemetry.** Frugal makes no outbound network calls
  except those required to satisfy a tool call against a configured
  provider. Releases do not check for updates from the binary.
- **No control plane.** There is no `api.frugal.sh` Frugal is
  authenticated against; there is no account, no license check, no
  remote configuration. The binary is self-contained.

### Trust the operator extends

Running `frugal mcp serve` grants Frugal:

1. **Read access to env vars** at startup — same as any process spawned
   from the operator's shell. Set keys narrowly (per-process env, not
   global) when possible.
2. **Outbound HTTPS** to each configured provider's documented endpoint
   (e.g. `serper.dev`, `ydc-index.io`, `chrome.browserless.io`). The
   exact endpoints are visible in `config/models.yaml`.
3. **A local stdin/stdout pipe** to the parent MCP client when serving
   over stdio (the default), or a listening TCP socket when serving
   over `--http`.

### Auditing the routing decision before serving

`frugal mcp serve --dry-run` prints the full route plan — every
configured provider, its cost, whether it would be active at startup,
and the env var that gated it in or out — then exits without serving
anything or making any network call. Paranoid operators can run this
before enabling the server in an agent client to confirm exactly which
providers would receive traffic.

### HTTP transport boundary

`frugal mcp serve` over stdio has zero network surface — it can only be
spawned as a child process of an MCP client on the same machine.

`frugal mcp serve --http` opens a TCP listener and is intended for
trusted networks (a localhost-only bind, a VPC-internal address, or
behind a reverse proxy that terminates TLS). It defaults to requiring
`FRUGAL_AUTH_TOKEN` (bearer auth) and a per-IP rate limit; running with
`--allow-anon` is explicitly a foot-gun and refuses to start without
the flag. There is no built-in TLS — terminate it at a proxy.

### Out of scope (assumptions)

The following are the operator's responsibility; Frugal does not
defend against them:

- **A malicious MCP client.** If the agent client itself is compromised
  it can already exfiltrate the search results and rendered content
  Frugal hands it. Frugal does not sandbox its caller.
- **A compromised provider account.** If a provider's credentials are
  stolen and replayed elsewhere, Frugal cannot detect it. Rotate keys
  upstream when this is suspected.
- **A malicious URL passed to `frugal__browse` or `frugal__extract`.**
  Frugal forwards the URL to the configured provider; SSRF defence
  belongs at the provider boundary. For local browse drivers (not yet
  shipping) we will document URL allowlist controls.
- **Privilege escalation against the host OS.** Frugal runs with
  whatever privileges the operator launches it with. Run as a
  non-root user.

## Supply chain and release integrity

Releases are built by the public GitHub Actions workflow
[`.github/workflows/release.yml`](.github/workflows/release.yml) on tag
push. The workflow:

- Runs `go test ./...` against the tagged commit.
- Cross-compiles `frugal-{linux,darwin}-{amd64,arm64}` via `make
  release`.
- Generates `SHA256SUMS` over every release artifact.
- Signs each binary and `SHA256SUMS` with
  [cosign](https://docs.sigstore.dev/cosign/) in keyless mode using the
  workflow's GitHub OIDC identity (no private key is stored). The
  signing identity is verifiable as:
  `https://github.com/brainsparker/frugal/.github/workflows/release.yml@refs/tags/<tag>`
  issued by `https://token.actions.githubusercontent.com`.
- Generates a CycloneDX SBOM (`frugal.cdx.json`) and uploads it
  alongside the binaries.

The `install.sh` installer verifies the cosign signature when `cosign`
is on `$PATH` and the SHA256 checksum unconditionally before moving the
binary into place. To verify a downloaded binary manually:

```bash
# Assumes frugal-linux-amd64, SHA256SUMS, and SHA256SUMS.sig are in cwd.
cosign verify-blob \
  --bundle SHA256SUMS.sig \
  --certificate-identity-regexp \
    'https://github.com/brainsparker/frugal/\.github/workflows/release\.yml@refs/tags/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  SHA256SUMS

sha256sum -c SHA256SUMS  # macOS: shasum -a 256 -c SHA256SUMS
```

The Go build is reproducible: `make release` builds with `-trimpath`
(strip filesystem paths), `-buildvcs=false` (omit per-build VCS
metadata), `CGO_ENABLED=0`, and a fixed `-ldflags
"-X main.buildVersion=<tag>"`. Combined with a pinned Go toolchain
(`go` directive in `go.mod`), two builds of the same tag on the same
Go toolchain version produce byte-identical binaries; the SHA256SUMS
shipped in each release reflects that.

## Supported versions

Frugal is pre-1.0. Security fixes land on `main` and are cut into the next
tagged release. Only the latest release receives security updates; earlier
versions must upgrade.

## Scope

In-scope:

- The `frugal` binary (`cmd/frugal`) and all packages under `internal/`.
- The installer script `docs/install.sh` (supply-chain integrity).
- Default configuration shipped in `config/models.yaml`.
- The release workflow `.github/workflows/release.yml`.
- Docker image `Dockerfile` and Fly deployment `fly.toml`.

Out of scope:

- Third-party provider APIs (You.com, Serper, Firecrawl, Browserless,
  SearXNG instances) — report to those vendors directly.
- Vulnerabilities in direct dependencies — report upstream, but we will
  respond by bumping the dependency once patched.

## Hardening posture

Operational expectations for deployers:

- Keep provider API keys (`SERPER_API_KEY`, `YDC_API_KEY`,
  `FIRECRAWL_API_KEY`, `BROWSERLESS_TOKEN`) out of shell history,
  version control, and CI logs. Frugal reads them from the environment
  and forwards requests upstream.
- For `frugal mcp serve --http` deployments, run behind a reverse proxy
  that terminates TLS and enforces authentication — Frugal's MCP HTTP
  transport is intended for trusted networks (e.g., inside a VPC).
  The default `frugal mcp serve` over stdio has no network surface.
- Verify release artifacts with `cosign verify-blob` against the
  published `SHA256SUMS` file. The installer does this automatically
  when `cosign` is present; CI environments should do it explicitly.
- Audit the route plan with `frugal mcp serve --dry-run` before
  enabling the server in an agent client, especially after changing
  `models.yaml`.

## Disclosure

Once a fix is released, we will publish a GitHub Security Advisory with
the affected versions, the fix version, and — if applicable — a CVE ID.
