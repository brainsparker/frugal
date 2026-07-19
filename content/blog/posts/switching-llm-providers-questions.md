---
title: "6 questions to ask before switching LLM providers"
slug: switching-llm-providers-questions
date: 2026-06-11T16:00
description: "Switching LLM providers should take an afternoon, not a quarter. Six questions that tell you whether you can actually move — before you must."
excerpt: "Most teams discover their switching cost the day a price hike or outage forces the move. Ask these six questions while the answer is still cheap."
cluster: model-selection
type: listicle
keyword: "switching LLM providers"
related:
  - how-to-choose-a-model-for-agents
  - evals-before-scale
  - what-is-an-llm-router
---
Switching LLM providers should be an afternoon of work. For most teams it's a quarter-long migration project, and the difference isn't the providers — it's decisions made months earlier, when nobody was thinking about leaving. You find out which kind of team you are on the worst possible day: the price hike, the deprecation notice, the outage that stretches past your patience.

I think about provider changes the way I think about [fallback chains](/blog/provider-fallback-chains/) for tool calls: the ability to move is a property you build, not a task you schedule. Here are the six questions I'd answer before switching — ideally before you need to.

## 1. How portable are your prompts, really?

Prompts fossilize around a provider's quirks. The system prompt that took three weeks to tune is tuned *for that model* — its formatting habits, its tool-calling style, its tolerance for long instructions. Move it verbatim to another provider and you'll get output that's subtly worse in ways that take days to notice.

The audit is straightforward: grep your prompts for provider-specific scaffolding. XML tags one model family prefers. Stop sequences. "You are"-preambles shaped by one vendor's docs. Response-format hacks that exist because model A ignored your JSON schema and model B won't. None of this blocks a switch. All of it is work you should size before you commit to a date, not after.

## 2. Can you prove the new provider is as good — on your traffic?

Benchmark scores don't transfer. The only eval that matters is one built from your own traffic: real inputs, graded outputs, run against both providers before you flip anything. If you followed the advice in [evals before scale](/blog/evals-before-scale/), you already have this harness and switching is a weekend experiment. If you didn't, build the eval first. Fifty representative cases beats zero, and zero is what most teams switch on.

The trap is grading the new provider on vibes for a week and calling it parity. Vibes detect catastrophes. They don't detect the 4% regression on your hardest category that shows up in support tickets a month later.

## 3. What does its latency look like under *your* traffic?

Published latency numbers are medians from someone else's workload. Your workload has its own shape: your prompt lengths, your output lengths, your concurrency spikes, your timeout budget. A provider that's fast at p50 and ugly at p99 will pass a demo and fail production — I wrote about why the tail is what matters in [latency budgets for agent tool calls](/blog/latency-budgets-agent-tool-calls/).

Run a shadow test: mirror a slice of real traffic to the candidate for a few days and log `latency_ms` per call. Compare distributions, not averages. Time-of-day matters too; some providers degrade exactly when your users are awake.

## 4. What are the rate limits, and what happens when you hit them?

Rate limits are the least glamorous line on the pricing page and the most common switch-killer. Your current tier was probably negotiated or grown into. The new provider starts you at defaults — requests per minute, tokens per minute, sometimes a daily cap — and those defaults may sit below your Tuesday peak.

Ask concretely: what tier do you land on day one, how fast can you raise it, and what does the provider return when you exceed it? A clean 429 with a retry-after header is workable. Silent queuing or connection drops are not. If the answer is "contact sales to raise limits," get that conversation done before migration day, not during it.

## 5. What shape is the pricing — not just the rate?

Per-token rates get all the attention, but the *shape* of pricing decides your bill. Per-token favors short outputs. Per-call pricing punishes chatty agents that make many small requests. Committed-spend discounts look great until they become the reason you can't leave — a discount contingent on volume is a soft lock-in with a nicer name.

Model the candidate's pricing against a real month of your usage: your actual token mix, your actual call counts, cached versus uncached. The same workload can rank two providers in opposite order depending on whether your traffic is input-heavy or output-heavy. The sticker rate and your effective rate are different numbers, and only the second one is real.

## 6. What will switching cost you *next* time?

The best moment to evaluate exit cost is on the way in. If this migration is painful, the pain will repeat, because you're not choosing a provider forever — models leapfrog each other every few months now. The question isn't "is this the best provider" but "does adopting it make the next switch easier or harder?"

Concretely: are you coding to the provider's proprietary SDK or to an abstraction you own? Are you adopting features with no equivalent elsewhere? Is your eval harness provider-neutral? Teams that answer these well stop having migration projects at all — provider choice becomes configuration. That's the end state an [LLM router](/blog/what-is-an-llm-router/) points at: the provider is a rung on a ladder, not a foundation you pour concrete on.

## Switching LLM providers should be routine

The pattern across all six questions: the cost of switching is mostly self-inflicted, and it's paid in advance. Provider-specific prompts, missing evals, unexamined pricing shapes, proprietary SDK coupling — each one is a small convenience today and a week of migration work later.

My rule is that anything I'd need on switch day should already exist on a normal day. The eval harness runs weekly whether or not I'm evaluating a candidate. Costs and latency get stamped on every call, so comparing providers is a query against a ledger, not a research project. There's more in this vein on the [model selection](/blog/topics/model-selection/) hub, but the short version fits in a sentence: treat every provider as the current answer, not the permanent one, and the day you have to move becomes an afternoon.
