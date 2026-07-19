---
title: "Agent discovery: server cards, skills, and the .well-known web"
slug: agent-discovery-well-known
date: 2026-09-24T13:50
description: "Agent discovery is becoming the new SEO: server cards under .well-known, skills indexes, and robots.txt signals — what to publish and why it matters."
excerpt: "Agents can't use tools they can't find, and today they find tools through humans pasting configs. The machine-readable alternative is taking shape."
cluster: mcp-ecosystem
type: analysis
keyword: "agent discovery"
related:
  - mcp-ecosystem-2026
  - mcp-servers-new-cli-tools
  - evaluate-mcp-server-checklist
---
Every MCP server in production today was installed by a human pasting a config block. That's the current state of agent discovery: there isn't any. An agent's tool surface is decided at setup time, by a person, reading a README — and everything downstream, including which providers get paid, flows from that moment. I've written before about how [the provider that's easiest to wire in wins by default](/blog/mcp-ecosystem-2026/); discovery is the layer where that fight actually happens, and it's starting to grow real, machine-readable infrastructure.

The shape it's taking will look familiar to anyone who remembers how the human web solved the same problem: metadata files at predictable paths. The `.well-known` convention, skills indexes, crawler signals. I publish these surfaces for frugal.sh, so I'll use it as the concrete example — with the caveat, up front, that this is early. Conventions are unsettled and adoption is thin. What follows is what the surfaces are, why I bother, and why I think this becomes the new SEO whether or not the current file formats survive.

## The configuration wall

Start with the problem. An agent that needs a capability it doesn't have — say, structured extraction from a page — currently has no move. It can't search a registry, evaluate candidates, and connect; it improvises with the tools it was given or fails. The human operator, meanwhile, discovers tools the way humans discover anything: word of mouth, directories of wildly varying quality, a search that surfaces a GitHub README.

That wall shapes the whole ecosystem. Being *findable and legible to agents* is worth nothing today, so nobody optimizes for it, so the tooling stays human-mediated. But every platform shift has a moment where the audience changes — and [agents are becoming the primary consumers of a lot of infrastructure](/blog/mcp-servers-new-cli-tools/) that used to serve humans first. When your next user is a crawler working on behalf of an agent, your README is invisible. Your metadata is your storefront.

## Server cards: /.well-known as the front door

The `.well-known` path convention is twenty-plus years of web plumbing — it's how sites already publish security contacts, password-change URLs, and identity metadata at predictable locations. The emerging move is to do the same for MCP: a server card, a JSON document at a path like `/.well-known/mcp/server-card.json`, describing the server — what tools it exposes, what transports it speaks, how to authenticate, where the endpoint lives.

The point of a server card is that connection stops requiring a README. A client — or a directory, or a crawler building an index — can fetch one URL and know, mechanically, that frugal.sh serves `frugal__search`, `frugal__extract`, and `frugal__browse` over Streamable HTTP with bearer-token auth, and stdio if you run the binary yourself. Everything a human currently transcribes into a config block, published where software can read it.

Publishing one costs an afternoon. It's a static JSON file. There is no meaningful downside, which is why I find "adoption is thin" more an observation about incentives than about difficulty: the crawlers that would reward the effort are only starting to exist. The calculation is the same as early sitemap adoption — you publish before the consumer arrives, because the cost is trivial and the option value isn't.

## Skills indexes and robots.txt signals

Server cards describe connection. Two adjacent surfaces describe *use*.

An agent-skills index is documentation aimed at models instead of people: task-shaped guidance — here's how to do research cheaply, here's when to extract versus browse — published as structured files an agent can pull into context. The distinction from API docs matters. A server card says "there is a search tool with these parameters." A skill says "walk the ladder cheapest-first, and treat a paid provider's zero hits as the end of the chain." Judgment, not just schema. When I evaluate other people's servers, [docs written for the model](/blog/evaluate-mcp-server-checklist/) are one of the strongest quality signals, and skills indexes are that idea formalized.

And then there's `robots.txt` — the oldest agent-signaling file on the web, quietly getting a second life. It was always a message to non-human readers; the new wrinkle is content signals aimed at AI crawlers and agents specifically: what may be crawled, what may be used for what purpose, and pointers to the machine-readable surfaces above. The details are contested and the semantics are still being argued out, so I'd treat today's syntax as provisional. But directionally it's the same statement: this site expects non-human visitors and has opinions about serving them.

## Discovery metadata is the new SEO

Here's the analysis part. For two decades, "be findable" meant optimizing pages for search engines that ranked them for humans. The pipeline was crawl → index → rank → human clicks. Agents insert a new pipeline next to it: crawl → index → *connect*. The asset being ranked is no longer a page. It's a capability.

Every SEO dynamic gets an analogue. Structured metadata beats prose, the way schema markup beat keyword stuffing. There will be directories, then aggregators, then spam, then trust signals to fight the spam — reputation for tool servers is going to be a real problem, because a tool description is an instruction an agent tends to follow. Ranking will happen somewhere, and whoever operates the index will hold the same power over tool traffic that search engines held over web traffic. If anything, the stakes are sharper: an agent picks *one* server and calls it, rack rates and all. Position zero, but for infrastructure.

That's also the uncomfortable part. The open-web version of this future is servers publishing standard metadata that any client can read. The captured version is discovery happening inside a handful of proprietary registries. The `.well-known` approach is the open bet, and publishing to it is a small vote for that outcome.

## Early days — publish anyway

Being honest about where this stands as I write: no dominant convention, no killer crawler, no measurable traffic from any of these surfaces. I publish them for [frugal.sh](/) anyway, for three reasons. The files are static and cost nothing to maintain. Whatever format wins will be close enough to migrate to in an hour. And when discovery agents do arrive, they'll index what exists — being present in the first crawl of a new web has historically been worth a lot more than the effort it took.

The practical version, if you run an MCP server: put a server card at a `.well-known` path, write one skills file for your most common workflow, and state your crawling posture in robots.txt. A static afternoon's work, next to everything else in the [MCP ecosystem](/blog/topics/mcp-ecosystem/) you're already maintaining. The web spent twenty years learning that being legible to machines is a competitive position. The machines are different this time. The lesson isn't.
