# Security Policy

Frugal is a local-first AI toolchain router (CLI + MCP server) that handles
provider API keys on behalf of the operator. We take security reports
seriously and aim to respond within 72 hours.

## Reporting a vulnerability

**Please do not open a public GitHub issue for security-sensitive reports.**

Use GitHub's private vulnerability reporting to open a draft advisory against
this repository:

https://github.com/frugalsh/frugal/security/advisories/new

If you cannot use GitHub advisories, email **security@frugal.sh** with:

- A description of the vulnerability and its impact.
- Reproduction steps or a proof-of-concept.
- The affected version(s) and environment.

We will acknowledge receipt within 72 hours and coordinate a fix, disclosure
window, and CVE if applicable.

## Supported versions

Frugal is pre-1.0. Security fixes land on `main` and are cut into the next
tagged release. Only the latest release receives security updates; earlier
versions must upgrade.

## Scope

In-scope:

- The `frugal` binary (`cmd/frugal`) and all packages under `internal/`.
- The installer script `install.sh` (supply-chain integrity).
- Default configuration shipped in `config/models.yaml`.
- Docker image `Dockerfile` and Fly deployment `fly.toml`.

Out of scope:

- Third-party provider APIs (OpenAI, Anthropic, Google) — report to those
  vendors directly.
- Vulnerabilities in direct dependencies — report upstream, but we will
  respond by bumping the dependency once patched.

## Hardening posture

Operational expectations for deployers:

- Keep provider API keys (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`,
  `GOOGLE_API_KEY`, `TAVILY_API_KEY`, `SERPER_API_KEY`) out of shell
  history, version control, and CI logs. Frugal reads them from the
  environment and forwards requests upstream.
- For `frugal mcp serve --http` deployments, run behind a reverse proxy
  that terminates TLS and enforces authentication — Frugal's MCP HTTP
  transport is intended for trusted networks (e.g., inside a VPC).
  The default `frugal mcp serve` over stdio has no network surface.
- Verify release artifacts with `cosign verify-blob` against the published
  `SHA256SUMS` file. The installer does this automatically when `cosign`
  is present.

## Disclosure

Once a fix is released, we will publish a GitHub Security Advisory with
the affected versions, the fix version, and — if applicable — a CVE ID.
