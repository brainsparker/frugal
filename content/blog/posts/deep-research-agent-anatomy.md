---
title: "Anatomy of a deep research agent"
slug: deep-research-agent-anatomy
date: 2026-08-25T15:15
description: "A deep research agent has five phases — plan, fan out, extract, verify, synthesize. The bill lives in the fan-out. Here's the anatomy, with numbers."
excerpt: "Log a research run and a shape appears: one plan, a burst of searches, a train of extractions, one long synthesis. Each phase costs differently. Route accordingly."
cluster: building-with-ai
type: analysis
keyword: "deep research agent"
related:
  - deep-research-cost-teardown
  - multi-tool-agent-orchestration
  - agent-cost-observability
---
Run a deep research agent with full logging on and the transcript has a shape. Not a vague one — a repeatable, almost anatomical structure that shows up across implementations, models, and topics. One planning call. A burst of parallel searches. A train of extractions. A scatter of follow-ups. One long synthesis at the end. If you're building or buying a deep research agent, this shape is worth understanding, because each phase has a different cost profile, a different latency profile, and a different tolerance for cheap substitutes. Treating the whole run as one undifferentiated blob of "agent activity" is how you end up optimizing the wrong phase.

Here's the anatomy, phase by phase, and where the money and the minutes actually go.

## Five phases, five different workloads

**Plan.** One model call, sometimes two. The agent decomposes the question into sub-questions and drafts a search strategy. Token-light, call-light, but load-bearing: a bad decomposition sends the entire fan-out in the wrong direction, and no amount of downstream quality recovers a run that researched the wrong thing. This is the classic case for spending up — one call, whole-run blast radius.

**Fan out.** The burst. The plan turns into search queries — commonly 5 to 15 per sub-question — dispatched wide. This is where the run stops being a conversation and becomes a workload: dozens of tool calls, each individually cheap, collectively the dominant line item. More on that below, because the fan-out is the whole cost story.

**Extract.** For each search hit that survives triage, fetch the page and reduce it to text. Fewer calls than search, but heavier ones — full page fetches, occasionally a headless browser for the JavaScript-walled ones. Extraction also produces the tokens that every later model call must read, so its output size quietly sets the input cost of the rest of the run.

**Verify.** The most variable phase, and the one that separates good research agents from confident ones. Cross-checking a claim means *more searches and more extractions* — targeted this time, two or three sources aimed at one disputed fact. Verification is a second, smaller fan-out, which means skimping on it is invisible in the cost report and very visible in the output.

**Synthesize.** One or two long calls that read everything the run gathered and write the report. Few calls, enormous inputs — this is where the run's largest single model call lives, its size determined by how disciplined the extract phase was.

Five phases, three fundamentally different workloads: a few high-stakes reasoning calls (plan, synthesize), a wide swarm of cheap retrieval calls (fan out, verify), and a middle band of fetch-and-reduce (extract). The [orchestration challenge](/blog/multi-tool-agent-orchestration/) is that one loop has to run all three well.

## The bill lives in the fan-out

Intuition says the model is the expensive part — the synthesis call alone reads a hundred thousand tokens. The logs say otherwise. In [one deep-research teardown I keep coming back to](/blog/deep-research-cost-teardown/), search alone was 54% of task cost. Not compute. Not synthesis. The searches.

The reason is arithmetic, not mystery. A research run that explores 6 sub-questions at 10 queries each, plus a verification pass, lands somewhere around 70–80 search calls. At $0.005 per call — You.com's rung — that's roughly $0.40 of search before a single page is extracted or a single token is synthesized. The per-call price looks like a rounding error; the fan-out multiplies it into the headline. Extraction rides the same multiplication one step behind: every search that returns hits feeds 2–3 extractions, at ~$0.001 per page through Firecrawl if a paid extractor answers.

This is the defining economic fact of the deep research agent as a workload: it is a *retrieval* workload with a reasoning garnish. Budget and optimize it like one.

## Where routing and caching cut cost without cutting quality

The good news about a bill dominated by retrieval is that retrieval is the most substitutable thing in the stack. The same query can be answered at $0 or $0.005 depending on who answers it — and research fan-outs are unusually rich in queries the free rungs handle well.

Look at what a fan-out actually contains: entity lookups ("what is X," "who founded Y") that Wikipedia answers at $0; longform and documentation queries where Marginalia is genuinely strong; and a hard core of fresh, commercial, or obscure queries that legitimately need a paid SERP. Routing cheapest-first with fall-through on zero hits — the ladder Frugal walks — lets each query find its cheapest competent answerer, and the paid rungs get only the residue. The quality cost is nothing, because fall-through means a free rung that can't answer doesn't answer; it steps aside. The honest caveat: free rungs are weak on news and product pages, so a fan-out heavy in those will and should ride the paid rungs.

Caching is the other structural cut, because research runs are self-repetitive by construction. The verify phase re-visits sources the fan-out already found. Sub-questions overlap and re-ask near-identical queries. Multiple users research adjacent topics against the same canonical pages. A cache keyed on normalized query and URL, with even a short TTL, absorbs the second and later hits at $0 — and unlike most cost cuts, this one *improves* the product, because cache hits return in milliseconds. Extraction is the standout: a page fetched during fan-out has not changed by verification time. Serve it from disk.

## The latency shape: wide is fast, deep is slow

Cost is one axis of the anatomy; latency is the other, and its shape is the inverse of what the call counts suggest. The fan-out — 70 calls — is the *fast* part when dispatched in parallel: wall-clock time is the slowest call in the batch, not the sum, so 70 searches cost roughly one search of waiting. The synthesis — one call — is the slow part, a single serial generation that can run minutes and parallelizes not at all.

So the run's wall-clock profile is a short wide burst followed by a long thin tail, and the engineering follows from it. Parallelize the fan-out aggressively; sequential search loops are leaving 10× wall-clock on the table. Set [per-call latency budgets](/blog/latency-budgets-agent-tool-calls/) inside the burst so one slow provider can't hold the whole batch hostage — in a parallel fan-out, p99 *is* your latency. And stream the synthesis, because it's the phase the user actually watches. Users forgive a two-minute research run; they don't forgive two minutes of spinner.

## Where to start

If you operate a deep research agent, produce its anatomy from your own logs before changing anything: calls, cost, and wall-clock per phase, for a dozen representative runs. The 54% number is one teardown, not your teardown — but I'd be surprised if retrieval isn't your largest line. Then work the phases in order of leverage: route the fan-out down a price ladder, cache extraction, parallelize the burst, and spend your premium-model budget on the two calls — plan and synthesize — where it moves the output. The rest of my notes on agent construction live in the [building with AI](/blog/topics/building-with-ai/) series.
