---
title: "Benchmarks lie: evaluate models on your own traffic"
slug: evaluate-models-own-traffic
date: 2026-07-23T15:20
description: "LLM benchmarks vs production traffic: why leaderboard rank doesn't predict your workload, and how 50 real queries beat any public eval."
excerpt: "A model's leaderboard position tells you how it does on the leaderboard's distribution, not yours. Fifty queries from your own logs beat it every time."
cluster: model-selection
type: problem-solution
keyword: "LLM benchmarks vs production"
related:
  - evals-before-scale
  - latency-quality-cost-tradeoffs
  - switching-llm-providers-questions
---
A model's benchmark score tells you how it performs on the benchmark's distribution of inputs. Your product does not serve the benchmark's distribution of inputs. That's the whole problem with LLM benchmarks vs production reality, and it's why teams keep getting surprised: they pick the model that tops a public leaderboard, ship it, and discover it's mediocre at the one thing their users actually ask for.

I don't think benchmarks are fraudulent. Contamination is real — test sets leak into training data — but even a perfectly clean benchmark measures the wrong thing for you. MMLU-style scores measure broad knowledge recall. Coding leaderboards measure competitive-programming-shaped problems. Your workload is neither. It's "summarize this specific kind of support ticket" or "extract these six fields from this vendor's invoices," ten thousand times a day. No public number exists for that. You have to make one.

The good news: making one is a day of work, and it's mostly harvesting, not building.

## Your logs already contain the benchmark

Fifty real queries, pulled from production logs, beat any public eval for predicting how a model behaves on your traffic. Not five hundred. Fifty is enough to see the failure patterns, small enough to actually read every output, and cheap enough to re-run whenever you want.

Harvesting them is a sampling problem, and the mistake is grabbing the first fifty rows. Take a stratified sample instead: your most common query shapes weighted roughly by volume, plus deliberate over-sampling of the ugly tail — the longest inputs, the malformed ones, the ones that generated user complaints or retries. If you've been [logging your agent traffic properly](/blog/instrumenting-ai-agents-logging/), this is a grep and an hour of curation. If you haven't, that's the real finding, and it's fixable this week.

For each query, record what a good answer looks like. Sometimes that's an exact expected output (extraction, classification). Sometimes it's a rubric — three things the answer must contain, one thing it must not. Don't overthink the format. A JSONL file with `input`, `expected`, and `notes` fields is a benchmark. I've written before about [running evals before you scale](/blog/evals-before-scale/); this is the same discipline pointed at model selection instead of prompt changes.

## Score cost and latency in the same table as quality

Here's where most private evals still go wrong: they measure quality alone, declare a winner, and let cost and latency show up later as surprises. In production, the three are one decision. A model that scores 4% better and costs 6× more and adds 800ms is not better for most workloads — it's worse, expensively.

So the eval output should be one table, three columns minimum:

| Candidate | Quality (pass rate on 50) | p50 latency | Cost per 1K queries |
|---|---|---|---|
| Incumbent | 82% | 1.9s | $4.10 |
| Candidate A | 88% | 2.4s | $11.30 |
| Candidate B (small) | 79% | 0.8s | $0.70 |

Numbers illustrative — the point is the shape. Once you see all three columns together, the decision usually stops being about the quality column. Candidate B is three points worse and 83% cheaper; for an internal summarization pipeline, that's a landslide win. Candidate A is the pick only if those six extra points land on queries where failure is expensive. This is the same [latency-quality-cost triangle](/blog/latency-quality-cost-tradeoffs/) that governs every model decision; the private eval is just what makes the triangle measurable instead of vibes.

Quality scoring itself can be lightweight. Exact-match where outputs are structured. A rubric-following judge model where they're not — imperfect, but consistent, and consistency is what comparisons need. Spot-check the judge on a dozen cases by hand before you trust it.

## Re-run it on every candidate, every time

The eval isn't a report. It's infrastructure. The failure mode I keep seeing: a team builds a careful evaluation once, picks a model, and then eighteen months later that eval is a stale doc nobody can re-run — the scripts rotted, the test set reflects the product two pivots ago.

Treat the eval like a test suite instead. It runs on every candidate switch: a new model release, a price cut that makes a bigger model affordable, a latency regression that makes you shop around. New query shapes from production get folded into the fifty (and old ones retired) so the set tracks your actual traffic. The [questions you ask before switching providers](/blog/switching-llm-providers-questions/) all get easier when question one — "is it actually better on our traffic?" — takes an afternoon to answer instead of a quarter.

The re-run cost is trivial. Fifty queries against a candidate model is pocket change even at premium rates. The thing it replaces — shipping the wrong model and finding out from users — is not.

## What this turns model choice into

The payoff is bigger than any single decision. With a runnable private eval, model selection stops being an event and becomes a routine — closer to a dependency upgrade than a bet-the-product migration. A new model drops; you run the fifty; you look at three columns; you switch or you don't. Twenty minutes of attention instead of a two-week debate seeded by someone's leaderboard screenshot.

It also changes who wins arguments. Without your own numbers, the loudest opinion or the shiniest launch post picks your model. With them, the table does. I've watched the same dynamic play out in tool-provider selection — it's why I built cost stamping into Frugal rather than trusting invoices — and there's a whole [model selection hub](/blog/topics/model-selection/) of variations on the theme: measure on your traffic, decide from receipts.

Where to start, concretely: today, pull fifty real queries from your logs and write down what good looks like for each. Tomorrow, run your current model and one candidate over them, and put quality, p50 latency, and cost-per-thousand in one table. That's the entire method. The leaderboards can keep arguing about which model is smartest. You'll know which one is right — for the only distribution of inputs that pays your bills.
