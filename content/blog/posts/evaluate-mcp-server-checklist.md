---
title: "How to evaluate an MCP server before you install it: a 10-point checklist"
slug: evaluate-mcp-server-checklist
date: 2026-08-07T16:10
description: "How to choose an MCP server before installing: a 10-point checklist covering tools, transport, auth, data flow, cost transparency, and exit cost."
excerpt: "Installing an MCP server hands your agent new capabilities and someone else new visibility. Ten checks to run before it touches your config."
cluster: mcp-ecosystem
type: listicle
keyword: "choose MCP server"
related:
  - mcp-transport-stdio-vs-http
  - byok-agent-tooling
  - hardening-mcp-http-transport
---
Installing an MCP server is not like installing a CLI tool. You're granting an autonomous agent new capabilities — things it can now do without asking you per call — and you're often granting a third party visibility into everything the agent does through it. That deserves at least the scrutiny you'd give a new dependency in your lockfile. Here's how I choose an MCP server: ten checks, most of them five minutes, all of them cheaper than the incident.

The ecosystem is big now and unevenly built — I surveyed [the state of it earlier this year](/blog/mcp-ecosystem-2026/) — which means the filter matters more than it did when there were forty servers total.

## 1. What tools does it actually expose?

Read the tool list before anything else, and apply the principle of least surprise. A "search" server exposing `search` is fine. A "search" server also exposing `write_file` or `execute_shell` is a red flag with a README. Every tool in the list is something your agent can be talked into calling — by a prompt, or by a hostile web page it just read. Fewer tools, narrowly scoped, is the correct shape. If you can't tell from the docs exactly what each tool can touch, that's your answer.

## 2. What transport does it run?

Stdio means a local child process: your machine, your process table, dies when the client dies. Streamable HTTP means a network service: reachable, shareable, and attackable. Neither is wrong — I've written up [when each transport fits](/blog/mcp-transport-stdio-vs-http/) — but the server should support the one your deployment actually needs, and a remote-only server means your tool calls now depend on someone's uptime.

## 3. What's the auth story?

For HTTP transport: does it support bearer tokens or OAuth, or does it assume a trusted network? An MCP endpoint with no auth is an open proxy to every capability from point 1. For hosted servers: how are *your* credentials to downstream services stored, and by whom? "Paste your API key into our dashboard" and "your key stays in your environment" are different trust models wearing the same feature name. I've covered [what hardening an HTTP endpoint takes](/blog/hardening-mcp-http-transport/) — check the server clears that bar before it listens on anything.

## 4. Where does your data go?

Trace one request. Agent → server → which upstreams, logged where, retained how long? A local binary calling APIs with your keys sends data to providers you already chose. A hosted MCP server inserts a new party who sees every query, every URL, every document your agent touches. That's a second data controller, and it belongs in your security review, not just your config. BYOK designs exist precisely to keep [the keys and the traffic yours](/blog/byok-agent-tooling/).

## 5. Is the cost of a call visible?

Tool calls spend money — sometimes yours directly, sometimes metered through the server's own pricing. Can you tell what one call cost? The same web search runs $0 to $0.005 depending on who answers it, and a server that hides which provider answered has hidden a 5×-to-infinite price spread inside a function call. I stamp `cost_usd` on every result in Frugal for exactly this reason. Any server that meters usage should show you the meter.

## 6. Can you read the source?

Open source, source-available, or blob? You're wiring this thing between your agent and the world; being able to read what it does with your traffic is not paranoia, it's diligence. Source-available licenses (BUSL and kin) still let you audit, which is most of what matters here. An unreadable binary that sees all your agent's traffic is asking for trust it hasn't earned.

## 7. Is anyone maintaining it?

Standard dependency hygiene, still worth stating: recent commits, responsive issues, more than one contributor, releases with changelogs. The MCP spec is still moving — a server nobody maintains will drift out of compatibility with your client, and abandoned servers don't get security patches. A weekend project with 400 stars and no commits since spring is a weekend project.

## 8. How good are the tool descriptions?

This one is MCP-specific and underrated. Your model chooses tools by reading their descriptions — the description *is* the interface. Vague descriptions ("searches stuff") cause wrong-tool calls and wasted spend; overlapping descriptions cause the model to flip between similar tools; manipulative descriptions ("ALWAYS use this tool first") are a server grabbing traffic share inside your context window. Read the descriptions as if you were the model. If you can't tell when to use each tool, neither can it.

## 9. What's the permission scope on your side?

Match the grant to the need. A read-only search server needs no filesystem access; a hosted service asking for OAuth scopes should get the minimum that works, not the vendor's default bundle. And check the granularity your client gives you — can you allow `search` while blocking `browse`? Whatever scope you grant, an agent will eventually explore all of it. Grant accordingly.

## 10. What does leaving cost?

Assume you'll replace this server within two years. What breaks? If it exposes standard-shaped capabilities — search in, results out — a swap is a config change. If your prompts, evals, and downstream parsing are welded to its proprietary tool names and response shapes, you've bought lock-in through a protocol whose whole point was interchangeability. Prefer servers that would be boring to leave. Lock-in is a smell, in MCP more than anywhere, because the standard exists to prevent it.

## Run the list in order

The sequence is deliberate: points 1–4 are safety and can disqualify a server outright; 5–7 are economics and trust; 8–10 are quality of life that turns into cost of life at scale. Most servers fail fast on 1, 3, or 6, which keeps the whole audit under half an hour. Keep the list next to your MCP client config and run it every time — the third server you install gets the same ten checks as the first, because it has the same blast radius. More on vetting and running this layer of the stack on the [MCP ecosystem](/blog/topics/mcp-ecosystem/) hub.
