---
title: "Hardening an MCP server's HTTP transport"
slug: hardening-mcp-http-transport
date: 2026-07-21T14:30
description: "MCP server security over HTTP, from operating one: bearer tokens, per-IP rate limits, bind-address discipline, and what /metrics leaks."
excerpt: "The moment an MCP server leaves stdio for HTTP, it becomes a network service with everything that implies. The hardening playbook I actually run."
cluster: mcp-ecosystem
type: problem-solution
keyword: "MCP server security"
related:
  - mcp-transport-stdio-vs-http
  - byok-agent-tooling
  - mcp-ecosystem-2026
---
The moment your MCP server leaves stdio for HTTP, it stops being a subprocess and becomes a network service. Everything that implies — authentication, rate limiting, exposure surface — arrives at once, whether you planned for it or not. Most of the writing about MCP server security is abstract. This is the concrete version: what I actually do to run Frugal's Streamable HTTP transport without lying awake about it.

Some context for why this matters more than it looks. An MCP server holding tool credentials is a credential proxy. Frugal is [BYOK](/blog/byok-agent-tooling/) — your Serper key, your You.com key, your Browserless key all live in its config. Anyone who can reach the HTTP endpoint and speak MCP can spend your money and exfiltrate whatever your tools can reach. That's the threat model. It's not exotic. It's "someone found your port."

## stdio sidesteps almost all of this

Worth saying first: the cheapest hardening is not exposing a listener at all. Over stdio, the MCP server is a child process of the client. The transport is a pipe. There is no port, no auth handshake to get wrong, no scanner traffic. The OS process boundary is the security boundary, and it's a good one.

If your agent and your MCP server run on the same machine — which describes most single-developer setups — use stdio and skip the rest of this post. I wrote up the full [stdio vs HTTP tradeoff](/blog/mcp-transport-stdio-vs-http/) separately; the short version is that HTTP earns its complexity when multiple clients or multiple machines need one server. That's a real need. It's just not the default need.

Everything below assumes you've decided HTTP is worth it.

## Bind address is the first decision, not the last

The classic failure: a server binds `0.0.0.0` because that's what the example config said, and now it's answering the whole network. On a laptop behind NAT that's sloppy. On a cloud VM with a public IP it's an incident with a delay timer.

Bind to `127.0.0.1` unless you have a specific reason not to. If other machines need access, prefer binding to a private interface — a VPC address, a WireGuard or Tailscale IP — over binding wide and filtering later. Firewalls drift. Security groups get edited under deadline. A listener that never accepts the packet beats a rule that's supposed to drop it.

The test is one line:

```
$ ss -tlnp | grep <port>
LISTEN 0 4096 127.0.0.1:8080 ...
```

If the third column says `0.0.0.0` or `[::]` and you didn't choose that deliberately, stop and fix it before reading further.

## Bearer tokens: boring, sufficient, non-optional

Frugal's HTTP transport supports bearer-token auth, and I treat it as mandatory the moment the bind address is anything but loopback. A static bearer token is not sophisticated. It's also exactly the right amount of machinery for a server with one operator: no identity provider, no OAuth dance, no token refresh bugs. One secret, checked on every request, rotated when you feel like it.

Three rules make it actually work. Generate the token, don't invent it — 32 bytes from `/dev/urandom`, not a word you can remember. Deliver it out of band — environment variable or secrets manager, never in the client config you commit. And treat any unauthenticated request as a signal, not just a 401 — if something is probing your MCP port with bad tokens, you want to know.

The objection I hear is "it's on a private network, why bother." Because private networks stop being private in boring ways: a misconfigured proxy, a compromised dependency on a neighboring box, a too-broad VPN. Auth on the server means the network is a layer, not the layer.

## Per-IP rate limiting is about spend, not just abuse

Rate limiting an MCP server looks like standard DoS hygiene, but there's a second reason specific to tool servers: every request can cost money. A misbehaving client — a retry loop, an agent stuck re-searching the same query, a colleague's script gone quadratic — can burn through paid-rung calls at whatever rate the transport accepts them. At $0.005 per premium search, a tight loop doesn't need to be malicious to be expensive.

Frugal ships per-IP rate limiting on the HTTP transport for exactly this. The limit isn't tuned to stop attackers; it's tuned to cap the blast radius of a bug. Think about what one legitimate client plausibly needs per minute, multiply by a small safety factor, and set that. When the limiter trips, you've converted a surprise invoice into a log line — the same philosophy as the [cost observability](/blog/agent-cost-observability/) argument: make spend visible and bounded before it makes itself visible.

## /metrics is a leak you configured on purpose

Prometheus metrics are genuinely useful — Frugal exposes `/metrics` on the HTTP transport and I watch it. But a metrics endpoint is a map of your system offered to anyone who can GET it: request rates, provider names, error counts, latency histograms. None of it is a credential. All of it is reconnaissance. An attacker who can read your per-provider call rates knows which paid APIs you hold keys for and how hard you use them.

So `/metrics` gets the same discipline as the main endpoint, with one extra twist: scrapers usually can't do custom auth headers as easily as MCP clients can, which tempts people into exempting the path. Don't. Either your Prometheus can send the bearer token (it can — `authorization` in the scrape config), or you put the metrics listener on a separate loopback-only port and let a local agent relay it. What you never do is leave the one unauthenticated path on an otherwise authenticated server.

## The checklist

Run this against any MCP server you expose over HTTP — mine or anyone's. There's more on evaluating servers generally on the [MCP ecosystem hub](/blog/topics/mcp-ecosystem/).

- Could this be stdio instead? If yes, make it stdio and stop.
- Bind address: loopback or a private interface, chosen deliberately. Verify with `ss -tlnp`.
- Bearer-token auth on, token generated not invented, delivered out of band.
- Per-IP rate limit set from expected legitimate traffic, not left at default-off.
- `/metrics` behind the same auth or on a separate loopback listener — never the one open path.
- TLS if traffic crosses anything you don't control; a reverse proxy in front is the standard, unexciting answer.
- Unauthenticated and rate-limited requests logged and actually looked at.

None of this is novel. That's the point. An MCP server over HTTP is a network service, and forty years of network-service hygiene apply unmodified. The failure mode isn't that the hardening is hard — it's that the transport upgrade happens in an afternoon and the hardening gets scheduled for later. Do them in the same afternoon. The checklist takes less time than reading about the incident would.
