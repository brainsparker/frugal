---
title: "Designing AI agents that survive provider outages"
slug: ai-agents-provider-outages
date: 2026-04-30T16:05
description: "An LLM provider outage will hit your agent eventually. A playbook: multi-provider fallback, failure taxonomy, drills, and budget alarms."
excerpt: "Your agent is a distributed system with a single point of failure per dependency. The playbook for staying up when a provider doesn't."
cluster: reliability
type: problem-solution
keyword: "LLM provider outage"
related:
  - provider-routing-for-ai-agents
  - how-to-choose-a-model-for-agents
---
Every provider your agent depends on will go down. Not might — will. An LLM provider outage takes your agent's brain offline; a search or extraction provider outage takes its hands. And most agents in production today respond to either one the same way: they throw, they retry the same dead endpoint, and they page a human who can't do anything about it.

This is strange, because we solved this class of problem years ago for every other dependency. Nobody ships a payment system with one processor and no failover. But agents get built fast, against whichever SDK was easiest to wire in, and the failure path gets written the week after the first outage instead of the week before. Here's the playbook for doing it in the right order.

## Multi-provider is a day-one decision, not a hardening task

The single most important property of an outage-resistant agent is boring: for every external capability — inference, search, extraction, browsing — there are at least two providers configured, and the code path that switches between them runs constantly, not just in emergencies.

That last clause is the part teams miss. A fallback that only executes during an outage is untested code deployed at the worst possible moment. The fix is to make fallback the normal mode of operation. I run my agents' tool calls through a price ladder — free providers first, paid as fallback — which I built for [cost reasons](/blog/provider-routing-for-ai-agents/), but the reliability payoff turned out to be just as large: the chain-walking code executes thousands of times a day on ordinary traffic. When a provider dies, nothing novel happens. The router falls through one rung earlier than usual.

Retrofitting multi-provider onto a mature agent is painful because provider assumptions leak into response parsing, prompts, and tests. Adding it on day one costs almost nothing: an ordered list instead of a constant.

## Order the chain before you need it

A fallback chain is a policy, and writing it during an incident produces a bad one. Decide the order now, per capability, using three inputs.

**Failure independence.** Two rungs that share infrastructure fail together. A local model, a self-hosted SearXNG, a local readability extractor — these share nothing with a cloud API and make ideal bottom rungs. My search chain ends in Wikipedia and Marginalia partly because their availability is uncorrelated with everyone else's bad day.

**Degraded is a real rung.** The chain doesn't have to preserve full quality; it has to preserve *function*. A weaker model that keeps the loop moving beats a strong model that's down. I covered how to think about model tiers in [how to choose a model for your agent workload](/blog/how-to-choose-a-model-for-agents/) — the same tiering doubles as your outage plan, walked in reverse.

**Cost, with eyes open.** Cheapest-first ordering means an outage pushes traffic *down* to paid rungs rather than starting there. That's the right default, but it has a billing consequence I'll get to below.

Write the resulting order into config, not code, so you can reorder it mid-incident without a deploy.

## Outage, timeout, empty: three failures, three verbs

Fallback logic lives or dies on its failure taxonomy. Collapse everything into "request failed" and you'll do the wrong thing in at least two of the three common cases.

**Hard failure** — connection refused, 500s, 429s that persist after backoff. The provider is unavailable. Fall through to the next rung immediately, and remember the failure for a while so the next hundred calls don't each pay a timeout to rediscover it.

**Timeout** — no answer inside your deadline. Ambiguous: the provider might be down or merely slow, and slow sometimes means "under the load of everyone else failing over to it." Fall through for this call, but track timeouts separately from hard failures. A rising timeout rate is your earliest outage signal, visible before any status page updates.

**Empty result** — a clean 200 with zero hits. Not a failure at all, and treating it as one burns money. My routing rule: a free provider returning zero hits falls through (its index is partial; absence there proves nothing), but a paid provider returning zero hits ends the chain — the query has no hits, and paying a pricier provider to confirm nothing is the worst spend available. During an outage this distinction matters more, not less, because more traffic is reaching the paid rungs.

Stamp every response with what actually happened — `provider_used`, `latency_ms`, and an error class when there is one. During an incident, that per-call record is the difference between knowing your blast radius and guessing at it.

## Test the failure path like you mean it

The failure path is code, and untested code doesn't work. Three drills, in increasing order of honesty.

First, unit-test the taxonomy: feed the router synthetic 500s, timeouts, and empty results, and assert the chain does what the policy says. Cheap, catches logic bugs, catches nothing about reality.

Second, kill providers in staging. Null-route the top rung's endpoint and run your normal eval traffic. You're checking two things: the agent completes tasks on lower rungs, and the *quality* on lower rungs is acceptable. This is where you discover that your prompts quietly assumed one provider's response format.

Third, occasionally do it in production, on a slice of traffic, on purpose. Force a small percentage of calls to skip rung one. If that sentence terrifies you, that's the signal it's worth doing — it means your fallback path is load-bearing and unexercised, which is the worst combination. Because my ladder exercises fallback on ordinary zero-hit traffic every day, this drill is less dramatic for me than it sounds; that's the position you want to be in.

## The bill is part of the blast radius

Here's the failure mode nobody puts in the runbook: the outage where everything works. Your free or cheap rung goes down, the chain does its job, traffic shifts to the paid rungs, and the agent hums along — at 5× or ∞× the usual cost per call. The incident review says "no user impact." The invoice disagrees.

So treat cost as an SLO. Alert on spend rate and on rung distribution: if the share of calls answered by paid rungs jumps, something upstream is failing even if every task is succeeding. This is the operational argument for per-call cost stamping — a `cost_usd` field on every result turns "the bill spiked" from a month-later surprise into a same-hour alarm. Decide in advance, per workload, what an acceptable failover spend rate is and what gets shed when you exceed it: batch jobs pause, background enrichment stops, user-facing paths keep paying.

Where to start: pick your single most-called capability, add one fallback rung behind it, and classify its failures into the three buckets above. Then break the primary on purpose and watch. One afternoon, and your next provider outage becomes a log line instead of an incident. More of this, including the taxonomy applied to whole tool chains, lives on the [reliability](/blog/topics/reliability/) hub.
