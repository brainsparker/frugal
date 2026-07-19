---
title: "Search is most of your agent's bill"
slug: deep-research-cost-teardown
date: 2026-06-18T15:25
description: "An AI agent cost breakdown that surprises everyone: search, not the model, dominates research-agent spend. Why fan-out multiplies it, and what cuts it."
excerpt: "Everyone budgets for tokens. Then a teardown shows search at 54% of task cost. The math behind why research agents multiply search spend."
cluster: ai-costs
type: analysis
keyword: "AI agent cost breakdown"
related:
  - rack-rate-gap-web-search-costs
  - agent-cost-observability
  - cut-ai-agent-api-costs
---
Ask a team to guess their AI agent cost breakdown and they'll talk about tokens. Model pricing, context windows, maybe caching. Then someone actually itemizes a research-style agent's spend, and the model turns out to be the minority line. In one deep-research teardown I keep coming back to, search alone was 54% of task cost. Not the model. The searches.

That number rearranged how I think about agent economics, and I want to walk through why it isn't a fluke — why search *structurally* dominates the bill for any agent that researches, and why the usual cost-cutting instincts do nothing about it.

## The fan-out is the whole story

A chat completion is one task, one call. A research task is one task, *many* calls, and each call is billed separately. That's the entire mechanism.

Watch a deep-research agent work: it takes a question, decomposes it into subtopics, issues a handful of searches per subtopic, reads results, notices gaps, and issues follow-up searches to fill them. A single "compare these three frameworks" task can plausibly issue 40 or 60 search queries. Each one hits a metered API. At $0.005/call, 50 searches is $0.25 of search spend on one task — before extraction, before browsing, before a single output token.

The model bill doesn't fan out the same way. Synthesis calls are few and batched; the model reads what search retrieved and writes once. So as agents get more agentic — more autonomous, more thorough, more willing to go check — the tool side of the bill grows faster than the model side. Search dominance isn't an accident of pricing. It's the geometry of the workload: one task becomes N queries, and N is large and growing.

## The multiplication effect nobody budgets for

Here's where it compounds. Per-query search pricing looks like a rounding error — a fifth of a cent, half a cent. Nobody escalates a purchase decision over $0.005. But the agent multiplies that rounding error three times over.

First multiplication: queries per task. The 40–60 fan-out above. Second: tasks per day. A research agent that's actually useful gets used — dozens or hundreds of runs daily. Third: the retry-and-refine loop. Agents re-search when results look thin, rephrase queries that missed, and re-verify claims before writing. Every one of those is another billed call, invisible inside "the agent ran."

Multiply through: $0.005 × 50 queries × 200 tasks/day is $50/day — $1,500/month — from a line item priced to look negligible. And because tool calls rarely show up itemized anywhere a team looks, this grows quietly. I made the broader case for stamping every call in [agent cost observability](/blog/agent-cost-observability/); research agents are that argument at its most extreme, because they're the workload where per-call pricing meets the biggest N.

The [rack-rate gap](/blog/rack-rate-gap-web-search-costs/) sharpens it further: that $0.005 query has functional substitutes at $0.001 and at $0. The gap between the top and bottom of the ladder is 5× among the paid rungs alone, and infinite against the free rung. Multiply a 5× unit-price gap by a 50× fan-out and the routing decision on one tool swings the whole agent's economics.

## What actually cuts the search line

Three things move this number. All three attack the multiplication, not the prose.

**Routing.** Send each query down a price ladder — free indexes first, cheap paid next, premium last. Research fan-out is exactly the traffic mix where this shines: a big share of decomposed sub-queries are background lookups ("what is X," entity facts, documentation) that free rungs answer identically to premium ones. I laid out the full argument in [free-first](/blog/free-first-default-provider/). The queries that genuinely need a premium SERP still get one; they just stop dragging fifty siblings along at the same rate.

**Caching.** Research agents are repetitive by nature. Runs on adjacent topics issue overlapping queries; a single run often re-searches something it already retrieved. A results cache with even a short TTL turns those repeats into $0 hits. Freshness-sensitive queries can bypass it; most of the fan-out isn't freshness-sensitive.

**Dedupe.** The bluntest one. Log your queries per task and read them. Decomposition steps routinely emit near-duplicates — the same question phrased three ways, launched as three billed calls. Normalizing and deduplicating queries before dispatch cuts N directly, and cutting N cuts everything downstream: search spend, extraction spend, the tokens spent reading redundant results.

## What doesn't cut it

Prompt tweaks. The instinct, on seeing a big agent bill, is to compress the system prompt, trim the context, tighten output length. For a chat workload, reasonable. For a research agent, you're optimizing the 46%, and the lever is small even there — token costs per synthesis call are modest to begin with.

Worse, some prompt work *raises* the bill: instructions to "be thorough," "verify claims," "consider multiple perspectives" all translate directly into more fan-out, more billed queries. The prompt controls quality. The tool layer controls cost. Teams tune the prompt because it's the part they can see, which is exactly the trap — the [cost-cutting checklist](/blog/cut-ai-agent-api-costs/) I wrote earlier applies, but for research agents the ordering is stark: nothing on it matters until the search line is handled.

## Read your own breakdown first

The 54% figure is one teardown, not a law. Your number depends on your fan-out, your provider rates, and how chatty your agent's model side is. But that's the point: it's *your* number, and you can get it. Tag every tool call with its cost, aggregate by category for a week, and see what actually dominates — the [ai-costs](/blog/topics/ai-costs/) hub collects the techniques. If your agent researches, I'll make a prediction: search is bigger than you budgeted, the model is smaller, and the fix lives in routing, caching, and dedupe — not in another round of prompt surgery.
