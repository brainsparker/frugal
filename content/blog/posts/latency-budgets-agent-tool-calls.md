---
title: "Latency budgets for agent tool calls"
slug: latency-budgets-agent-tool-calls
date: 2026-05-19T14:50
description: "How to budget AI agent latency: end-to-end targets split per tool call, p50 vs p95, tail math on sequential chains, and per-rung timeouts."
excerpt: "An agent chains tool calls in sequence, and sequences are where tail latency compounds. How to set a budget, split it per call, and spend it on fallback rungs."
cluster: reliability
type: educational
keyword: "AI agent latency"
related:
  - ai-agents-provider-outages
  - provider-routing-for-ai-agents
  - ai-feature-launch-checklist
---
Nobody budgets agent latency, for the same reason nobody budgets agent cost: the total is an emergent property of a loop, and loops don't show up in anyone's design doc. A single tool call taking 900ms sounds fine. An agent making fourteen of them in sequence is a 13-second answer, and now your "fine" calls have produced a feature people abandon.

AI agent latency is budgetable, though. The math is old — services have run on latency budgets for decades — it just needs adapting to the two things agents do differently: they chain calls sequentially, and (if you're routing well) each call may try several providers before it succeeds. Here's how I think about both.

## Start end-to-end, then divide

A latency budget starts as a product number, not an engineering one: how long will a user wait for this interaction? Pick it deliberately — 5 seconds for an interactive answer, 60 for a "we'll notify you" job — and write it down, the same way you'd set the cost ceiling in a [feature launch checklist](/blog/ai-feature-launch-checklist/). Everything downstream is division.

Divide the end-to-end number across the steps a typical task takes. If a task is roughly two model calls and six tool calls, and the budget is 10 seconds, then after the model calls take their share you might have 4–5 seconds for tools — call it 700ms per tool call, budgeted. Suddenly every provider decision has a criterion. Is this rung fast enough? Compare its latency to the line item, not to your patience during testing.

The division also exposes infeasible designs early. Fourteen sequential tool calls at 700ms each cannot fit in a 5-second budget, no matter how you tune providers. That's not a latency problem; it's an architecture problem — parallelize the calls, cut the count, or change the product promise. Better to learn that from arithmetic than from churn.

## Budget the p95, not the p50

The p50 is the number providers put on marketing pages. The p95 is the number your users experience, because anyone who triggers more than a few tool calls per session samples the tail regularly.

Sequential chains make this brutal. Chain six calls and the odds that *at least one* hits its p95 are roughly 1 − 0.95⁶ ≈ 26%. A quarter of your tasks eat at least one tail latency. Chain fourteen calls and it's a coin flip. The chain doesn't average your latencies; it collects your slowest moments. This is the single most counterintuitive fact about agent latency: per-call percentiles that look excellent produce end-to-end percentiles that look terrible, and the degradation is a pure function of chain length.

So budget against p95 per call, and measure it yourself — from your machines, on your query mix. Which requires logging `latency_ms` on every tool call from day one. Frugal stamps it on every response next to `cost_usd` for exactly this reason: a latency budget without per-call measurements is a wish.

## Fallback chains spend the budget too

If your tool calls walk a provider ladder — free rungs first, paid as fallback, the pattern from [the provider routing guide](/blog/provider-routing-for-ai-agents/) — then one logical call can be several physical calls, and each rung spends from the same line item.

Here's a real captured run from my own ledger. A search hit Marginalia first: 0 hits in 529ms. The chain fell through to Wikipedia: 3 hits, $0, in 954ms. Total wall clock, about 1.5 seconds; the response carried `{"provider_used":"wikipedia","cost_usd":0,"latency_ms":954}`. The user-visible latency wasn't Wikipedia's 954ms — it was Wikipedia's 954ms *plus* the 529ms spent learning Marginalia had nothing. Fall-through isn't free; it's paid in milliseconds.

The design consequence: give every rung its own timeout, sized so the whole ladder fits the call's budget. If the line item is 2 seconds and the ladder is three rungs, the first rung cannot be allowed 2 seconds — a hang at rung one would consume the entire budget before your fallbacks get a turn. Cap early rungs tightly (a free rung that hasn't answered in 800ms should yield to the next rung), and leave the last rung the remainder. Per-rung timeouts are the difference between a ladder that degrades gracefully and one where the cheap rung's bad day becomes the whole call's bad day — the same failure shape I described in [how agents behave during provider outages](/blog/ai-agents-provider-outages/), just measured in milliseconds instead of error rates.

## When a slow free provider is fine — and when it isn't

The Marginalia-first ladder above costs ~500ms of added latency on queries it can't answer, in exchange for $0 on the queries it can. Whether that trade is good depends entirely on which budget the call lives in. Same provider, opposite verdicts:

| Context | Budget | Slow free rung first? |
|---|---|---|
| Interactive chat answer | 3–5s end-to-end | Only with a tight per-rung timeout |
| Interactive, streaming partials | 1s to first useful token | Usually no — go straight to the fastest rung |
| Background research job | Minutes | Yes, always |
| Overnight batch enrichment | Hours | Yes — try *every* free rung first |

Batch work is where free-first routing is unambiguous: nobody is watching, latency is nearly worthless, and every dollar saved is fully realized. Interactive work makes you choose — you can buy latency headroom by skipping to a fast paid rung, or spend headroom to try free rungs first. Both are defensible. What's not defensible is not knowing which one you're doing.

A practical pattern: run two ladders per capability. A "patient" ladder for batch paths — every free rung, generous timeouts. A "hurried" ladder for interactive paths — at most one fast free rung with a sub-second cap, then paid. Route each call to the ladder matching its budget. It's a few lines of config, and it resolves the false argument between "free-first is too slow" and "paying rack rate for batch jobs is waste." Both are true; they're about different ladders.

## Where to start

Log `latency_ms` per tool call this week if you aren't already — nothing else here is possible without it. Then compute your real chain length (tool calls per task, p50 and p95) and multiply. That one multiplication usually tells you where the budget actually goes: not the slow provider you suspected, but the sheer number of sequential calls, or one rung with no timeout quietly holding the whole chain hostage.

Latency budgets end up being the operational glue of agent design — they connect product promises to provider choices to timeout values, the same way cost ceilings connect invoices to routing. Both are covered in more depth across the [reliability](/blog/topics/reliability/) posts. The budget isn't the hard part. Deciding to have one is.
