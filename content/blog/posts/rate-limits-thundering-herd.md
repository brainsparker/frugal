---
title: "Rate limits, retries, and the thundering herd"
slug: rate-limits-thundering-herd
date: 2026-09-17T14:25
description: "Rate limit handling for agent fleets: jittered backoff, per-provider concurrency budgets, and why a 429 deserves its own code path."
excerpt: "When one rung fails, every agent in your fleet fails over to the same next rung in the same second. The stampede is self-inflicted. Here's how to break it up."
cluster: reliability
type: problem-solution
keyword: "rate limit handling"
related:
  - provider-fallback-chains
  - hidden-cost-of-retries
  - ai-agents-provider-outages
---
One agent with a fallback chain is resilient. A hundred agents with the *same* fallback chain are a stampede waiting for a trigger. The day your first rung starts throwing 429s, all hundred fail over to the second rung within the same few seconds — and the second rung, which normally sees a trickle of overflow traffic, takes the full herd at once. So it rate-limits too. Then the retries kick in. Rate limit handling for a fleet is mostly the art of not building this machine, and almost every agent stack builds it by default.

I run my own agents through a router with exactly this shape — ordered rungs, cheapest first — so I've had plenty of chances to watch a healthy fallback design turn into a synchronized battering ram. The fix isn't one trick. It's three, layered: treat rate limits as their own signal, desynchronize the retries, and budget concurrency per provider instead of per agent.

## A 429 is a third kind of signal

There are three ways a provider can give you nothing, and they mean three different things.

An **error** — timeout, 500, connection refused — means the provider is having a bad time. Fail over to the next rung. This is the case [fallback chains](/blog/provider-fallback-chains/) were built for.

**Zero hits** means the provider is healthy and the query has no results there. What you do depends on the rung — free rungs fall through, paid rungs end the chain. I wrote up [the zero-hit semantics](/blog/zero-hits-empty-results/) separately, because getting them wrong is expensive in its own way.

A **429** means something neither of the others means: the provider is healthy, your traffic is not welcome at this rate. It is not an outage — marking the provider dead and failing the whole fleet over is an overreaction that manufactures the herd. It is not empty results — there's no information about your query in it at all. It's a flow-control message, and it usually arrives with instructions: a `Retry-After` header or a rate-limit-reset timestamp. A handler that lumps 429s in with errors throws those instructions away and replaces them with panic.

So the first fix is boring: give rate-limit responses their own branch. Honor `Retry-After` when it's present. Slow down against *that* provider without abandoning it. And record the 429 in your logs as its own event type, because "how often are we throttled, by whom" is a number you'll want later.

## How a chain becomes a battering ram

Here's the failure in sequence. Your fleet runs 100 concurrent tasks against rung one. Rung one starts throttling — maybe you grew, maybe they shrank your quota. Within seconds, 100 agents see failures. If your handler treats a 429 as "provider down," 100 agents fail over to rung two simultaneously. Rung two was sized for the occasional fall-through; it now takes 100 requests in a burst, and throttles. Now both rungs are throwing 429s, and your retry logic — three attempts each, say — turns 100 requests into 300 or 400, all landing in the same narrow windows because every agent computed the same backoff delay from the same failure time.

Nothing in that story requires a provider outage. The providers were fine. The fleet DDoS'd itself, one rung at a time, and paid retry costs for the privilege — retries are already [where bills go to multiply](/blog/hidden-cost-of-retries/) even without a stampede synchronizing them.

## Jitter, because synchronized retries are the herd

Exponential backoff without jitter doesn't disperse a herd. It schedules reunions. Every agent that failed at t=0 retries at t=1s, then t=2s, then t=4s — together, forever. The load arrives in pulses, each pulse re-triggers the throttle, and the backoff dutifully schedules the next pulse.

The standard fix is full jitter: instead of sleeping `base × 2^attempt`, sleep a uniformly random duration between zero and that value.

```
sleep = random(0, min(cap, base * 2^attempt))
```

That one line turns pulses into a smear. A hundred agents that failed together now return spread across the whole window, and the provider sees a ramp instead of a wave. Cap the ceiling (30–60 seconds is plenty for tool calls), always let `Retry-After` override your computed delay, and cap total attempts — one or two retries, never unbounded. A retry should also spend from the same per-task budget as the original call, or a throttled afternoon quietly doubles your invoice.

## Budget concurrency per provider, not per agent

Jitter fixes the timing. It doesn't fix the volume. If your fleet can generate 100 concurrent requests and a rung can comfortably take 20, no amount of jitter makes 100 into 20.

The structural fix is a concurrency budget per provider, enforced at the fleet level — a semaphore, a token bucket, a counter in Redis, whatever your stack makes cheap. Each provider gets a ceiling on in-flight requests across *all* agents. An agent that can't get a slot doesn't hammer; it waits briefly or falls through to a rung with capacity.

Per-agent limits don't do this. "Each agent makes at most 2 concurrent calls" sounds disciplined and still delivers 200 concurrent calls at fleet scale. The provider doesn't experience your agents individually. It experiences your IP block. Budget at the level the provider sees. (This is also the server side of the same idea — Frugal's HTTP transport ships per-IP rate limiting for exactly this reason. When you operate the tool server, you're the rung somebody's fleet fails over to.)

## Spread the failover itself

One more layer, for fleets big enough to matter: don't send every agent to the same next rung. If rungs two and three are both healthy, hash the task ID across them so a rung-one failure splits the herd instead of relocating it intact. And when rung one recovers, don't return everyone at once — readmit a fraction of traffic, watch for 429s, then ramp. A recovering provider greeted by the entire herd goes right back down, and now you're oscillating.

None of this changes the ladder's economics. Cheapest-first still holds; you're spreading *within* the set of acceptable rungs, not paying premium rates for calm. The ladder decides who's eligible. The herd controls decide how fast you arrive.

## Where to start

In order of effort. First, split 429s from errors in your handler and honor `Retry-After` — an hour of work. Second, add full jitter to whatever backoff you already have — one line. Third, put a fleet-level concurrency cap in front of each paid provider, sized well under whatever limit has bitten you before. Spreading failover across rungs and ramped recovery can wait until your fleet is large enough to be its own weather system.

The theme, like most of what I write about [reliability](/blog/topics/reliability/), is that agents fail collectively even when they're built individually. A single agent's retry loop is fine. A thousand copies of it, triggered by the same event, computing the same delays, are an outage generator you deployed yourself. Desynchronize them and the "provider problems" mostly stop being problems.
