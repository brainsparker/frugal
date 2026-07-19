---
title: "Zero hits is not an error"
slug: zero-hits-empty-results
date: 2026-06-09T13:20
description: "Handling empty API results starts here: zero hits is a signal, not a failure. Why free and paid providers deserve opposite fall-through rules."
excerpt: "An empty result set arrives as a 200 and gets treated like a 500. The provider didn't fail — it answered. What you do next should depend on whether that answer cost money."
cluster: reliability
type: educational
keyword: "handling empty API results"
related:
  - provider-fallback-chains
  - ai-agents-provider-outages
  - latency-budgets-agent-tool-calls
---
The most misread response in agent tooling is an empty array. A search returns zero hits, and somewhere a retry loop treats it like a 500 — retrying, escalating, alerting — as if the provider had failed. It didn't fail. It answered. The answer was "nothing." Handling empty API results correctly starts with taking that answer seriously, because everything downstream — your fallback logic, your retry policy, your bill — depends on what you decide an empty response means.

## Empty is an answer, not a fault

A zero-hit response is a completed, successful operation. The provider received your query, searched its index, and reported the truthful result: no matches. HTTP 200, well-formed body, empty list. Nothing about that resembles a timeout or an exception, and code that routes all three into one `catch` block is making a category error with consequences.

I've written before about [the three signals a provider chain has to distinguish](/blog/provider-fallback-chains/) — error, timeout, zero hits — and why each needs its own handling. The short version: an error means the rung is broken, a timeout means the rung blew your budget, and zero hits means the rung worked. Collapse them and you get pathologies like circuit breakers tripping on providers that are perfectly healthy, or [outage dashboards](/blog/ai-agents-provider-outages/) lighting up because a niche query legitimately had no results. Coverage is not health. A provider that finds nothing is not a provider that's down.

The subtle part is that "the provider worked" doesn't yet tell you what to do next. For that you need to ask a second question.

## "No results exist" vs. "this provider can't see them"

An empty result set is ambiguous between two very different facts:

1. There are no results. The thing you searched for doesn't exist, isn't indexed anywhere, or your query is malformed in a way no index can save.
2. This provider can't see the results. They exist — just not in the slice of the world this particular index covers.

Which one you're looking at depends almost entirely on who answered. Every index has a shape. Marginalia is genuinely good at essays, documentation, and the indie web, and weak on news and product pages — so Marginalia returning zero hits on a query about a product launch tells you about Marginalia's coverage, not about the web. Wikipedia covers entities; ask it something that isn't an entity and the empty response was foreordained. A zero from a narrow index is weak evidence. A zero from a broad one is strong evidence.

Here's a run I captured from my own ladder. Marginalia returned 0 hits in 529ms. The chain fell through to the next free rung, and Wikipedia came back with 3 hits at $0 in 954ms:

```json
{"provider_used": "wikipedia", "cost_usd": 0, "latency_ms": 954}
```

The first zero wasn't a failure and wasn't final — it was a 529ms, zero-dollar probe that eliminated one lens on the web and triggered the next. That's the correct life of a free zero: cheap evidence, then movement.

## The free/paid asymmetry

Which brings me to the rule I'd defend hardest, because it's where empty-result semantics turn into money.

A free provider returning zero hits should fall through. The probe cost nothing but latency, free indexes are narrow by nature, and the odds that the next rung sees what this one couldn't are decent. You spent 529ms to learn something. Keep walking.

A paid provider returning zero hits should usually end the chain. A paid rung has a serious, broad index — that's what the money buys. When it searches and finds nothing, the likeliest explanation is that there is nothing. Falling through means paying a pricier provider to confirm an empty set: if the $0.001 rung found nothing, spending $0.005 to hear "nothing" again is buying the same silence at five times the price. Two line items, zero results, and an agent no better informed than it was two calls ago.

This asymmetry is baked into how Frugal walks its ladder — free zeros fall through, paid zeros stop the chain — but you don't need my router to apply it. It's one conditional in whatever fallback code you already have, and it's the difference between a chain that spends on evidence and one that spends on reassurance. The signals your [chain's rungs](/blog/provider-fallback-chains/) emit are only as useful as the policy that reads them.

One honest caveat: "usually." If you know the paid rung is weak on this query's shape — a general index asked about something deep in a specialty corpus — a fall-through can be justified. But that's a decision you make deliberately, from coverage knowledge, not a default your error handler stumbles into.

## What this does to your retry logic

Once zero hits is a signal instead of a failure, several retry habits stop making sense.

Never retry a zero against the same provider. Retries exist for transient faults — a network blip, a dropped connection, a momentary 503. An empty result is not transient. The index didn't have it 200ms ago and won't have it now. A same-provider retry on zero hits has roughly zero upside and, on a paid rung, a guaranteed cost.

Falling through is not retrying, and the distinction earns its own budget line. A retry asks the same question of the same source and hopes for different luck. A fall-through asks a different source with different coverage — it's new evidence, not repetition. Your [latency budget](/blog/latency-budgets-agent-tool-calls/) should treat them differently too: retries deserve backoff because you're waiting out a fault; fall-throughs can fire immediately because nothing is wrong.

Surface the zero to the agent. When the chain ends on a legitimate empty — a paid rung found nothing — say so explicitly in the result rather than raising an exception or, worse, returning something vaguely error-shaped. "No results for this query" is information a model can act on: reformulate, broaden, tell the user the thing doesn't appear to exist. An exception teaches the agent that search is flaky. An honest empty teaches it that the world sometimes contains nothing, which happens to be true.

The takeaway fits in a sentence: error means broken, timeout means slow, empty means true — and the price of the rung that said "empty" determines whether you keep asking. Grep your codebase for where empty results land. If they share a path with exceptions, that's the bug, and it's costing you either money (paid confirmations of nothing) or coverage (chains that give up where they should have fallen through). More on making agent tooling behave under [reliability](/blog/topics/reliability/).
