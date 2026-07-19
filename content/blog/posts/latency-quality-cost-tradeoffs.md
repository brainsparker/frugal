---
title: "Latency, quality, cost: pick the two your product needs"
slug: latency-quality-cost-tradeoffs
date: 2026-07-02T15:55
description: "The LLM latency cost tradeoff is per-call, not per-product. How to route each call to the corner of the triangle it actually needs."
excerpt: "Autocomplete, overnight reports, and research tasks live in different corners of the same triangle. Products that pick one corner for everything overpay in all three."
cluster: model-selection
type: educational
keyword: "LLM latency cost tradeoff"
related:
  - latency-budgets-agent-tool-calls
  - small-models-first
  - how-to-choose-a-model-for-agents
---
Latency, quality, cost. For any single call, you can optimize two. The mistake almost everyone makes with the LLM latency cost tradeoff is treating it as a product decision — "we're a quality-first product" — when it's a per-call decision. Your product is not one call. It's thousands of calls with wildly different requirements, and picking one corner of the triangle for all of them means overpaying on most.

I'll make the case, then get concrete about which calls belong in which corner.

## The triangle is real, and it's priced

You can't cheat it. A bigger model gives better answers slower and dearer. A smaller model is fast and cheap and drops the hard cases. Paying for dedicated capacity buys latency at a premium. Every knob you have — model size, provider tier, retries, timeouts, parallel sampling — trades along one edge of this triangle. Parallel sampling, for instance, buys quality with money while holding latency roughly flat: five samples, pick the best, pay five times.

The same triangle governs tool calls, not just model calls. A free search rung answers in whatever time it answers, at $0, with real coverage gaps. A paid rung is faster and broader at $0.001 to $0.005 a call. Same shape, different market.

What you can do — the only move that actually beats the triangle — is stop buying the same corner for every call.

## Per-call, not per-product

Take one product: a research assistant. It makes a call to rewrite the user's query, a call to classify which sources look relevant, forty search and extract calls, and one long synthesis call at the end.

Are these "quality-first" calls? The synthesis is. Getting it wrong wastes everything upstream. But the query rewrite is a plumbing call — a small model does it indistinguishably, in a fifth of the time, at a fraction of the cost. The relevance filter runs forty times, so its latency and cost multiply by forty while its quality bar is "roughly right." One product, three different corners of the triangle, and if you route all of it to your best model you've paid frontier prices for plumbing. That's the argument I made in [small models first](/blog/small-models-first/), and it falls straight out of the triangle: each call has its own two-of-three, so choose per call.

The uncomfortable corollary: "which model should we use?" is a malformed question. The well-formed one is "which calls need which corner?"

## Interactive surfaces pay for latency

If a human is watching a spinner, latency is a quality attribute. Users don't experience your answer's brilliance separately from the four seconds it took; they experience one thing, and slow-but-smart usually loses to fast-but-decent in anything conversational. So interactive calls should spend money and, where necessary, quality to buy speed: smaller models, tighter timeouts, the fastest adequate provider even when it's not the cheapest.

This is where per-rung timeouts earn their keep. In an interactive flow I'll give a free search rung a short leash — if it hasn't answered inside the budget, fall through to a paid rung that will. The paid call costs $0.005; the user waiting an extra two seconds costs more. I've written before about [setting latency budgets for agent tool calls](/blog/latency-budgets-agent-tool-calls/); the budget is what makes "pay for latency" an engineering rule instead of a mood.

Note what's being sacrificed: cost, deliberately. Interactive traffic is usually a minority of total calls, so paying up here is cheap in absolute terms — *if* you're not also paying up everywhere else.

## Batch pipelines shouldn't pay for speed

Nobody is watching the overnight job. If your report generates at 3:14am instead of 3:02am, nothing happened. So paying for latency in a batch pipeline is buying the one thing in the triangle that has zero value to you — and it's a purchase most pipelines make by default, because they reuse the interactive path's configuration.

Batch is where you take the other two corners hard. Quality: use the big model, sample multiple times, let the chain walk every rung. Cost: free providers first with generous timeouts, aggressive caching, no premium tiers. A free search rung that takes three seconds is strictly better than a paid one that takes one second when the deadline is "before morning." Slow is free. Batch pipelines are the only place in your system where you can have quality *and* cost, because latency was never in the running.

The failure mode is leakage: batch workloads quietly running on interactive settings. Worth an audit. In my experience it's the most common single misallocation in agent systems, and fixing it is a config change.

## Concrete pairings

| Workload | Pays for | Sacrifices | Looks like |
|---|---|---|---|
| Autocomplete / inline suggest | Latency + cost | Quality | Small model, tight timeout, no fallback chain — miss fast and move on |
| Overnight report | Quality + cost | Latency | Big model, multi-sample, free-first tools, generous timeouts |
| User-facing research task | Latency + quality | Cost | Capable model, paid rungs early, parallel tool calls |

Autocomplete is the purest case: a suggestion that arrives in 400ms and is pretty good beats one that arrives in 2s and is excellent, because the user already typed past it. And it fires on every keystroke, so cost matters too. Quality is the corner you give up — which is fine, because a mediocre suggestion costs the user nothing to ignore.

The research task a user is actively waiting on is the opposite: they asked a hard question and they're watching. Spend. Route to the capable model, hit paid search rungs earlier than you would in batch, parallelize the tool calls. Cost is the sacrifice, and it's the right one — this is your product's showcase moment, and it's a small slice of traffic.

The overnight report takes quality and cost, as above. Three workloads, three different pairs. Any single global configuration is wrong for at least two of them.

## Routing is how you buy the right corners

Once you accept that the triangle is per-call, you need machinery that can send different calls to different corners — a router, in other words, for models and for tools. The decision inputs are the same ones I covered in [choosing a model for agent work](/blog/how-to-choose-a-model-for-agents/): what does this call need, what's the cheapest thing that delivers it, and how do I fall through when it doesn't. There's more in that vein under [model selection](/blog/topics/model-selection/).

Where to start: label every call site in your agent with the two corners it needs. Not the model it uses — the corners. The list is usually short, ten or fifteen call sites, and the mismatches jump out immediately: plumbing on frontier models, batch traffic on interactive timeouts, autocomplete waiting on a fallback chain. Fix the mismatches and you'll have bought back money and latency without giving up quality anywhere a user would notice. The triangle doesn't bend. But you get to choose where you stand on it, one call at a time.
