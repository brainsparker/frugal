---
title: "Why we log latency_ms on every response"
slug: why-log-latency-ms
date: 2026-09-01T16:20
description: "API latency monitoring usually means a dashboard of averages. Why stamping latency_ms on every agent response answers questions a chart can't."
excerpt: "One field on every tool response does two jobs: it routes the next call and it explains the last one. A worked example from a real 1.5-second search."
cluster: reliability
type: analysis
keyword: "API latency monitoring"
related:
  - latency-budgets-agent-tool-calls
  - instrumenting-ai-agents-logging
  - timeouts-product-decision
---
Every response Frugal returns carries three stamped fields: `provider_used`, `cost_usd`, and `latency_ms`. The cost field gets the attention — it's the one I built the whole router around — but `latency_ms` is the field that works hardest. Most API latency monitoring lives in a dashboard: a p95 chart, an alert threshold, an average that smears ten thousand calls into one line. That answers "is the service healthy." It does not answer the question you actually get asked, which is "why did this call take 1.5 seconds."

A per-response latency stamp answers it. That's the argument of this post: latency belongs on the result, not just in the aggregate, because one field does two different jobs — it's a routing signal for the next call and a debugging record for the last one.

## One number, two jobs

The routing job first. Frugal walks a price ladder cheapest-first: free providers, then $0.001, then $0.005. Price sets the default order, but price alone can't tell you what a rung *costs* your users, because a rung's true cost is its rate plus the seconds it charges everyone who passes through it. I've written about [setting latency budgets for tool calls](/blog/latency-budgets-agent-tool-calls/) — budgets are only enforceable if every response reports how much of the budget it spent. No stamp, no budget. Just vibes.

The debugging job is the one that pays off at 11pm. When an agent run feels slow, the question decomposes into: which tool call was slow, on which provider, at which rung of the chain, and did the slow part buy anything? Aggregate monitoring can't decompose that way. A trace of stamped responses can, mechanically.

## The 1.5-second call, explained by its own trace

Here's a run I captured from my own traffic. A search goes into the ladder. Marginalia answers first — it's free — and returns zero hits in 529ms. Zero hits from a free rung means fall through, not stop; I've covered [why empty results need explicit semantics](/blog/zero-hits-empty-results/) elsewhere. The chain moves to Wikipedia, which returns 3 hits in 954ms, also at $0. The response the agent sees:

```json
{"provider_used": "wikipedia", "cost_usd": 0, "latency_ms": 954}
```

Total wall clock: a little under 1.5 seconds. Now watch what the trace does to the question "why did this call take 1.5s."

Without per-response latency, the honest answer is guesswork. Maybe Wikipedia was slow. Maybe the network. Maybe the chain retried something. You'd try to reproduce it, fail, and file it under "search felt slow today."

With the trace, the answer is arithmetic. 529ms bought a documented miss on the first rung. 954ms bought the answer on the second. Nothing retried, nothing timed out, nothing misbehaved — the call took 1.5 seconds because two providers ran in sequence and both behaved exactly to spec. The latency wasn't a bug. It was the price of trying free first, and the trace itemizes it.

Whether that price is acceptable is a separate question — a product question. 1.5 seconds is fine inside a research agent that runs for minutes; it's disqualifying behind an autocomplete box. That's a call someone should make deliberately, which is why I think [timeouts are a product decision](/blog/timeouts-product-decision/), not an ops default. But you can't make the call until the trace shows you where the time went.

## Percentiles per provider, not per endpoint

The aggregate isn't useless — it's just bucketed wrong. Most teams compute latency percentiles per endpoint: "search p95 = 1.9s." But if search fans out across four providers, that number is a smoothie. Marginalia's misses, Wikipedia's lookups, and a paid API's tail latency all blended into one figure that describes none of them.

Bucket by provider and each rung develops a personality. You learn Marginalia's median for your query mix, and separately its median *when it misses* — the fall-through tax. You learn whether the premium rung's extra $0.004 buys a tighter tail or just a bigger invoice. You learn which rung degrades on weekends.

That last one matters more than it sounds. A provider that fails outright trips your fallback chain and shows up in an incident channel. A provider whose p95 drifts from 800ms to 2.5s over six weeks trips nothing. Per-provider percentiles are how slow degradation becomes visible before your users report it as "the agent got worse," and the raw material for those percentiles is the same `latency_ms` field, aggregated later instead of never.

## When latency data reorders the ladder

Cheapest-first is the right default order, but it isn't sacred, and latency data is what earns exceptions.

Take the captured run again. Marginalia's 529ms miss was worth it in general — it costs $0, and for essay and documentation queries Marginalia answers well. But suppose the ledger showed that for entity lookups specifically, Marginalia's zero-hit rate approached 100%. Then for that query class, the first rung is a pure tax: half a second, every call, buying nothing. Put Wikipedia first for entity-shaped queries and the same $0 answer arrives in roughly 950ms instead of 1.5s. Same cost. Same result. A third of the latency gone, and the decision was made by a log field instead of a hunch.

This is the part of API latency monitoring that dashboards never surface: latency isn't just something you watch, it's an input to the routing policy itself. Price orders the ladder; measured latency and measured hit rates re-order it per query class. Rung order stops being an opinion and becomes a claim you can check against last month's traces.

## Where to start

If you already [log your agent's tool calls](/blog/instrumenting-ai-agents-logging/), this is one field. Measure wall time around the provider call, stamp it on the response, write it to the same ledger line as the provider name and the cost. If you log nothing yet, start with those three fields together — provider, cost, latency — because every interesting question turns out to need at least two of them.

Then wait two weeks and ask the ledger something. Which rung is slowest when it misses? Which query class never gets answered by the first rung? Where did the 90th-percentile run spend its time? Every one of those has an answer sitting in a JSONL file, and none of them has an answer on a dashboard. More on making agent infrastructure hold up under real traffic lives on the [reliability](/blog/topics/reliability/) hub.
