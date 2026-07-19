---
title: "Inside Frugal's router: how a search call picks its provider"
slug: inside-frugals-router
date: 2026-07-07T13:40
description: "Search API routing, traced through one real call: Marginalia misses, Wikipedia answers at $0, and the response carries the receipt."
excerpt: "One captured run, walked rung by rung: 0 hits in 529ms, a fall-through, 3 hits at $0, and exactly what lands in the ledger afterward."
cluster: provider-routing
type: analysis
keyword: "search API routing"
related:
  - provider-fallback-chains
  - free-first-default-provider
  - zero-hits-empty-results
---
The best way I know to explain search API routing is to trace one real call through it. Not a diagram — an actual captured run from [Frugal](/), my MCP server, with the timings and the prices it logged. An agent asked `frugal__search` a question. 1.5 seconds later it had three results and a line in a ledger. Here's everything that happened in between, and why each rule exists.

## The chain the call walks

Frugal orders search providers as a ladder, cheapest first. In the configuration this run used, that meant:

| Rung | Provider | Rack rate |
|---|---|---|
| 1 | Marginalia | $0 |
| 2 | Wikipedia | $0 |
| 3 | Serper | $0.001/call |
| 4 | You.com | $0.005/call |

The ordering rule is price, and nothing else. Not perceived quality, not brand, not which SDK was easiest to wire in. The premise — the whole thesis of [free-first defaults](/blog/free-first-default-provider/) — is that a paid rung has to earn each call by the free rungs demonstrably failing first. The two paid rungs alone are 5× apart, so even *within* paid, order matters.

This particular chain was running in zero-key mode: no Serper key, no You.com key configured. Frugal is BYOK with no account, so rungs you haven't keyed simply don't exist in the chain. That matters for how this run ends.

## Rung 1: Marginalia, 0 hits, 529ms

The call hits Marginalia first. Marginalia is a small independent index that's genuinely good at essays, documentation, and the indie web — and weak on news and product pages. For this query it came back in 529ms with zero hits.

Now the interesting decision. Zero hits from a free provider means: fall through. The reasoning is asymmetric and worth spelling out. Asking Marginalia cost $0 and half a second. Its empty answer carries limited information, because a small index missing a query is unremarkable — the query may be fine and the coverage thin. When the price of a second opinion is zero, you take the second opinion.

Note what did *not* happen: no retry. Marginalia didn't error; it answered, and the answer was "nothing here." Retrying a successful empty response is one of the classic confusions I covered in [zero hits is an answer](/blog/zero-hits-empty-results/) — an empty result and a failed call are different events and route differently.

## Rung 2: Wikipedia, 3 hits, $0, 954ms

The chain falls through to Wikipedia, which returns 3 hits in 954ms. Hits above zero means the chain ends: the rung answered, the walk stops, nothing below it gets called. Serper and You.com — had they even been keyed — never see the query, and nobody gets paid.

The response that goes back to the agent carries the receipt inline:

```json
{"provider_used":"wikipedia","cost_usd":0,"latency_ms":954}
```

Those three fields are the contract. Every result says who answered, what it cost, and how long it took — stamped on the response itself, not tucked into a dashboard you'd have to go look at. The agent, or the human reading the transcript, sees the price of every lookup at the moment it happens.

Total wall-clock for the run: 529ms of miss plus 954ms of answer, call it 1.5 seconds for a free result. That's the fall-through tax, and it's real. If this had been a latency-critical interactive call, the right move would be a tighter per-rung timeout, not a pricier default — the chain mechanics are the same ones I walked through in [provider fallback chains](/blog/provider-fallback-chains/).

## The other zero-hit rule

Suppose Wikipedia had also returned zero hits. With no paid rungs keyed, the chain ends and the agent gets an honest empty result. But suppose Serper *was* keyed, got the query, spent $0.001, and found nothing. Then Frugal ends the chain — it does not escalate to You.com.

The rule: a free provider returning zero hits falls through; a paid provider returning zero hits ends the chain. A broad commercial index coming back empty is strong evidence the query has no hits. Paying five times more for a second paid opinion mostly buys you confirmation, at 5× the price, on exactly the queries that were worthless. Free rungs get the benefit of the doubt; paid rungs are believed.

## What lands in the ledger

After the response goes back, the run is appended to a local JSONL ledger at `~/.frugal/usage`. One line per call: provider used, cost, latency, hit count. It never leaves the machine — there's no control plane to send it to, which is the point.

`frugal stats` reads that file and prints a monthly receipt. One accounting rule worth knowing: only the call that produced the result earns rack credit. In this run, Wikipedia's line is the one that counts; Marginalia's miss is recorded but doesn't get credited as if it had answered. When you look at the month, you see what each provider actually delivered — not how often it was merely asked. Over time that's the data that justifies (or demotes) each rung's position, which is exactly the per-provider trend you need for the quieter [failure modes](/blog/agent-tool-chain-failure-modes/) that never throw errors.

## What this doesn't get you

Honesty section. Zero-key mode — Marginalia plus Wikipedia and nothing else — is real and useful, and it is not a Google-grade SERP. Wikipedia covers entities well. Marginalia covers the thoughtful corners of the web. Neither will find yesterday's news, a product pricing page, or most commercial queries. If your agent's traffic is heavy on those, the free rungs will fall through a lot, and without a paid rung underneath you'll get a lot of honest empties.

The router doesn't fix coverage. What it fixes is *defaults*: with the ladder in place, the paid call happens only when the free ones actually missed, and it happens with a price tag attached. For plenty of agent workloads — research over reference material, documentation lookups, entity questions — the free rungs answer a large share of queries, and every one of those is a call nobody billed.

## The whole trick

That's the entire router. Order rungs by price. Fall through on free zero-hits, stop on paid zero-hits, stop on any hit. Stamp every response with provider, cost, and latency. Append everything to a ledger you own.

None of it is clever, and that's deliberate — the value is in making the cheap path the default path and keeping receipts, not in routing intelligence. If you want the ideas without the tool, they're all in the [provider routing](/blog/topics/provider-routing/) archive, and the ladder is buildable in an afternoon with a config file and a for-loop. The captured run above is what it looks like when it works: a question answered in 1.5 seconds, for free, with proof.
