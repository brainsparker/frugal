---
title: "Provider routing for AI agents: the complete guide"
slug: provider-routing-for-ai-agents
date: 2026-04-21T14:10
description: "Provider routing for AI agents, explained: the free-first price ladder, fallback semantics, cost stamping, and how to start without a rewrite."
excerpt: "Agents pick providers by what got wired in, not by price. Here's how a routing layer walks every tool call down a ladder — free first, paid as fallback."
cluster: provider-routing
type: educational
keyword: "provider routing"
related: []
---
Your agent doesn't shop around. When it needs a web search, it calls whatever provider got wired in during the first week of the project, at whatever that provider charges, forever. The same search costs $0 from Wikipedia or Marginalia and $0.005 from a premium API — a gap that provider routing exists to close, and that almost nobody closes.

This is the guide I wish had existed when I started building agents. It covers what provider routing is, why agents default to the expensive path, and how to fix it without rewriting anything.

## What provider routing is

Provider routing is a layer between your agent and its tool providers. The agent asks for a capability — search the web, extract this page, run this in a browser — and the router decides who actually answers, based on price, availability, and fit.

The comparison that makes it click: your agent's tool calls today are like an HTTP client hardcoded to one CDN region. It works. It also means you eat that region's price and that region's outages, and you never find out whether a cheaper answer existed.

A router replaces the hardcoded choice with an ordered list. For search, mine looks like: Wikipedia and Marginalia (free), then SearXNG if you self-host one (free), then Serper ($0.001/call), then You.com ($0.005/call). The agent doesn't know or care. It asked for search results. It got search results. The routing happened underneath.

## Why agents don't shop around

Nobody decides to overpay. It happens structurally.

An agent framework needs a search tool, so someone grabs the SDK for whichever provider has the best docs, pastes an API key, and ships. The provider was chosen by what was easy to wire in — availability, not price. Then the choice fossilizes. The provider name is in the tool definition, the response shape is assumed downstream, and swapping it means touching code that works.

There's also no pressure to revisit it, because the costs are invisible. Tool calls don't show up itemized anywhere your team looks. They're a line on a monthly invoice, aggregated past the point of usefulness. In one deep-research teardown I keep coming back to, search alone was 54% of task cost. Most teams running similar agents couldn't tell you their number, because nothing in their stack produces it.

So the default persists: premium provider, every call, including the thousands of calls a free index would have answered identically.

## The three-rung price ladder

Every routing setup I've built converges on the same shape. Three rungs.

**Rung one: free and local.** Wikipedia's API, Marginalia, a self-hosted SearXNG instance. For extraction, a local readability library instead of a paid scraping API. These cost $0 per call and answer more queries than you'd guess — entity lookups, background facts, documentation, the long tail of "what is X." They have honest limits: Marginalia is great for essays and the indie web, weak on news and product pages. Wikipedia covers entities, not everything. Free is not Google-grade. It doesn't need to be — it needs to catch the calls that don't need Google-grade.

**Rung two: cheap paid.** Serper at $0.001/call. A real SERP, real freshness, at a fifth the price of the premium rung. This is the workhorse for queries the free rung can't handle.

**Rung three: premium.** You.com at $0.005/call, or whichever full-service API you trust most. This rung earns its place on the queries that genuinely need it. As a default for every call, it's a 5× overpay against rung two and an infinite overpay against rung one.

The principle: walk the ladder cheapest-first, and make each paid rung justify itself per call. There's more on this way of thinking on the [provider routing](/blog/topics/provider-routing/) hub.

## Fallback semantics: the part everyone gets wrong

"Try the cheap one first, fall back if it fails" sounds simple until you define *fails*. An error is easy. Zero hits is the interesting case, and the rule differs by rung.

A **free** provider returning zero hits means fall through. Free rungs have partial indexes; an empty result usually means "not in my corner of the web," not "this doesn't exist." Falling through costs nothing, so you always do it.

A **paid** provider returning zero hits means end the chain. A full-index provider came back empty; the query genuinely has no hits. Paying a *pricier* provider to confirm an empty result is the worst spend in the whole system — you'd be buying nothing, twice.

Here's a real captured run from my own ledger. Marginalia returned 0 hits in 529ms, the chain fell through, and Wikipedia returned 3 hits at $0 in 954ms:

```json
{"provider_used":"wikipedia","cost_usd":0,"latency_ms":954}
```

Total cost of the fallback: zero dollars and half a second. That half-second matters for some workloads and not others, which is a routing decision too — a latency-critical path can start one rung higher. But it should be a decision, not an accident of which SDK got installed first.

## Stamp the cost on every result

The second half of routing is receipts. Every response that comes back through the router should carry three fields: which provider answered, what it cost, and how long it took. `provider_used`, `cost_usd`, `latency_ms`.

This sounds like bookkeeping. It's actually the enforcement mechanism. Once every result is stamped, the question "what does this agent cost to run?" has an answer you can grep for, per call, instead of a shrug and an invoice. Aggregate the stamps into a monthly view and you get something like a receipt: which rungs answered, what the same traffic would have cost at rack rate, what you actually paid.

I built this into Frugal — the MCP server I run my own agents through — as a JSONL ledger that never leaves the machine. But the idea is bigger than any one tool: if a system spends money per call, every call should say what it spent. Dashboards summarize. Receipts prove.

## When routing matters most

Routing pays off in proportion to tool-call volume, and nothing drives volume like search-heavy workloads. A deep-research agent might issue 40 or 60 searches per task. At $0.005 each, that's $0.20–0.30 per task before the model bill; at scale, search quietly becomes a top-line cost. That teardown number — search at 54% of task cost — came from exactly this kind of workload.

If your agent makes three tool calls a day, skip all of this. If it makes three thousand, the ladder is the difference between a rounding error and a budget line.

There's a second payoff that has nothing to do with money: a fallback chain is also an availability strategy. When a provider has a bad day, a routed agent degrades to the next rung. A hardcoded agent goes down with it.

## Where to start

Don't rewrite your agent. Do this instead, in order. First, log what you're spending now — even crude counting of tool calls by provider will surprise you. Second, put one free rung in front of your current provider for one tool, with fall-through on errors and on free-rung zero hits. Third, stamp `cost_usd` on every result so the savings are visible instead of theoretical. That's a routing layer. Everything past that — more rungs, more tools, smarter chain-ending rules — is refinement.

If you'd rather not build it, [Frugal](/) is my version: one Go binary, your keys, no account. But build or borrow, the default should be the same. Free first. Paid rungs earn their place.
