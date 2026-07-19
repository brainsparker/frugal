---
title: "stdio or Streamable HTTP? Choosing an MCP transport"
slug: mcp-transport-stdio-vs-http
date: 2026-06-02T14:05
description: "stdio or Streamable HTTP? How to choose an MCP transport: local vs shared use, auth, rate limiting, latency, ops burden, and a plain recommendation."
excerpt: "One transport is a subprocess and a pipe. The other is a service you now operate. Which MCP transport you pick is really a question about who connects, from where."
cluster: mcp-ecosystem
type: educational
keyword: "MCP transport"
related:
  - mcp-ecosystem-2026
  - latency-budgets-agent-tool-calls
---
Every MCP server forces one decision before it serves a single tool call: which transport. The spec gives you two — stdio and Streamable HTTP — and the choice looks technical but is really organizational. It comes down to one question: who connects to this server, and from where? Answer that and the MCP transport picks itself. I ship Frugal with both, so I've watched people choose wrong in both directions, and the failure modes are instructive.

## What the two transports actually are

Under either transport, MCP is the same thing: JSON-RPC messages between a client and a server. The transport is just the pipe those messages travel through.

With stdio, the client launches the server as a subprocess and talks to it over stdin and stdout. There's no port, no URL, no network. The server's lifetime is the session's lifetime — client exits, server dies. Configuration is a command line in your client's config file.

With Streamable HTTP, the server is a long-running process listening on an HTTP endpoint. Clients POST JSON-RPC messages to it, and the server can stream responses back over SSE when a call produces incremental output. It has an address, which means anything that can reach the address is a potential client — and a potential problem.

That's the whole difference: a subprocess versus a service. Everything else — auth, latency, ops — follows from it.

## stdio: the default for a single user on one machine

For the common case — you, your agent, your laptop — stdio is correct and it isn't close.

There's nothing to deploy. No port to choose, no TLS to terminate, no process manager. The client starts the server on demand and reaps it when done. Your API keys sit in the environment of a process running as you, on your machine, reachable by nothing that isn't already running as you. The attack surface is your own user account, which was already the attack surface.

Latency is a pipe write between two local processes — microseconds, not milliseconds. That never matters next to the tool call itself (a web search runs hundreds of milliseconds), but it's one less line in your [latency budget](/blog/latency-budgets-agent-tool-calls/) either way.

The limitation is exactly the design: one client, one machine. A subprocess can't be shared with a teammate, can't run near a private data source across the network, and can't outlive the session to hold long-lived state.

## Streamable HTTP: for shared and remote servers

The moment a second person, a second machine, or a remote environment enters the picture, you want Streamable HTTP.

The wins are real. One server, many clients — a team shares a single deployment instead of five laptops running five copies with five sets of keys. Keys live in one place, which makes rotation and spend accounting sane. The server runs where it needs to run: next to a private database, inside a VPC, on a box with the GPU. And a persistent process can hold caches, connection pools, and rate-limit state that a per-session subprocess rebuilds from zero every time.

The cost is equally real: you now operate a service. Uptime, TLS, restarts, monitoring, logs. None of it is hard, but none of it is optional, and all of it is work stdio never asks of you.

## Auth is where the choice gets serious

Here's the part people skip. stdio needs no auth story because the OS already provides one — if you can talk to the process, you already own the account it runs as. Streamable HTTP has no such inheritance. An HTTP endpoint answers whoever connects, and an MCP server is a particularly bad thing to leave open: it executes tool calls, often with your paid API keys behind them. An unauthenticated MCP endpoint is an open proxy where every request spends your money.

So the floor for any HTTP deployment, including on a "private" network:

- Bearer-token auth on every request. No token, no tool call.
- Per-IP rate limiting, so one misbehaving client — or one leaked URL — can't drain a key or a budget.
- TLS if the traffic leaves localhost.
- And don't bind to `0.0.0.0` "temporarily" while you set the rest up. Temporary has a way of shipping.

This is how Frugal's HTTP transport works: bearer-token auth, per-IP rate limiting, and a Prometheus `/metrics` endpoint, because a server other people depend on should be observable by the person operating it. Whatever server you run, treat that as the minimum bar — [the MCP ecosystem is still young](/blog/mcp-ecosystem-2026/), and plenty of servers ship HTTP support with none of it.

## Latency and ops, honestly

The latency difference is smaller than the discourse suggests. HTTP adds a network round trip — sub-millisecond on localhost, single-digit milliseconds within a region — on top of tool calls that run hundreds of milliseconds anyway. You will not feel the transport. What you will feel is operational: an stdio server that crashes takes out one session and restarts on the next launch; an HTTP server that crashes takes out everyone until something restarts it. The transport choice is an availability choice wearing a protocol costume.

| | stdio | Streamable HTTP |
|---|---|---|
| Clients | one, local | many, anywhere |
| Auth | inherited from the OS | yours to build (bearer tokens, minimum) |
| Keys live | your env | the server's env |
| Ops burden | none | a service to run |
| Added latency | microseconds | a network RTT |

## The plain recommendation

Single user, own machine: stdio. Stop deliberating. It's simpler, safer by construction, and every capability you'd get from HTTP is capability you don't need yet. Don't run a local HTTP daemon to be "future-proof" — you're taking on a service's ops burden and auth surface to serve one client through a loopback interface.

More than one client, or any client not on the server's machine: Streamable HTTP, with bearer tokens and rate limiting from the first request, not retrofitted after an incident.

And if you're shipping a server rather than running one, ship both and let the deployment decide — the JSON-RPC layer doesn't care, so the transport can be a flag instead of a fork. That's the approach I took, and it's why this whole decision can stay boring: pick the pipe that matches who's connecting, secure it accordingly, and go back to the tools, which are the part that matters. More on the protocol and its surroundings under [MCP ecosystem](/blog/topics/mcp-ecosystem/).
