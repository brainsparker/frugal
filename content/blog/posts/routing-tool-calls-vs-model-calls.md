---
title: "Routing tool calls vs routing model calls"
slug: routing-tool-calls-vs-model-calls
date: 2026-07-28T13:45
description: "AI request routing isn't one problem. Tool calls fail discretely, model calls degrade smoothly — and each needs its own fallback rules."
excerpt: "The same ladder idea, two different physics. Why a router that treats search results and model completions identically gets both of them wrong."
cluster: provider-routing
type: educational
keyword: "AI request routing"
related:
  - what-is-an-llm-router
  - provider-fallback-chains
  - zero-hits-empty-results
---
Routing a search call and routing a chat completion look like the same problem — walk a ladder of providers, cheapest first, fall back when a rung disappoints. I believed that for a while. Then I built the tool-call version and started sketching the model-call version, and the designs kept diverging. AI request routing is really two problems wearing one name, and routers that treat them as one problem get both wrong.

The difference isn't in the plumbing. It's in the physics of the responses. A tool call has a discrete outcome: it found the thing or it didn't. A model call degrades smoothly: every model returns *something*, and the something varies in quality along a continuum you can't cheaply measure. Everything else — health signals, fallback rules, accounting — flows from that one distinction.

## Discrete outcomes vs smooth degradation

When Frugal's router sends a search to Marginalia, the result is legible. Three hits or zero hits. HTTP 200 or HTTP 503. The router can inspect the response and know, mechanically, whether the rung answered. That legibility is what makes [fallback chains](/blog/provider-fallback-chains/) tractable for tools: the fall-through decision is a comparison against zero, not a judgment call.

Now send the same prompt to a small local model and a frontier model. Both return a completion. Both are grammatical. One is subtly wrong in a way that no router-level check can see, because evaluating the answer is as hard as producing it. There is no `hits: 0` field on a bad paragraph.

This is the core asymmetry. Tool routing gets its quality signal *in-band*, per call, for free. Model routing has to get its quality signal *out-of-band* — from evals run beforehand, from downstream task success, from users. Which means a model router can't actually route on response quality in real time. It routes on a prediction of quality, made earlier, about which classes of request a given rung can handle. The [LLM router](/blog/what-is-an-llm-router/) designs that work are all classifiers at heart: decide the difficulty tier before the call, because you can't grade the answer after it.

## Health means different things on each ladder

"Is this rung healthy?" also splits in two.

For tools, health is mostly liveness and yield. Error rates, timeouts, and hit rates tell you nearly everything. Marginalia being up but returning zero hits for news queries isn't unhealthy — it's a partial index doing what partial indexes do, which is exactly the [zero-hit semantics](/blog/zero-hits-empty-results/) problem: an empty result from a free rung means "not in my corner of the web," so you fall through. The signal is noisy per-query but perfectly observable.

For models, liveness is the boring part. A model provider is almost never *down*; it's slow, or it's quietly nerfed, or a silent version bump shifted behavior on your prompts. The health signals that matter — eval pass rates drifting, refusal rates creeping up, output-format compliance dropping — are statistical, delayed, and invisible to a per-request health check. A tool router can mark a rung unhealthy after three consecutive 503s. A model router marking a rung unhealthy needs something closer to a nightly eval suite and a changelog subscription.

Conflate the two and you get a router that health-checks its models with pings — green across the board while quality quietly rots.

## Fallback rules: when zero hits ends the chain, and why models have no equivalent

The tool-side fallback rule I keep coming back to has a beautiful asymmetry. Free rung returns zero hits: fall through, it costs nothing to ask the next index. Paid rung returns zero hits: end the chain — a full-index provider came back empty, so the query has no hits, and paying a pricier provider to confirm nothing is the worst spend available.

Try to port that rule to models and it dissolves, because there's no "zero hits." A model never comes back empty-handed; it comes back wrong, and you can't detect wrong. So model-side fallback triggers on the things you *can* detect: hard errors, timeouts, refusals, malformed output against a schema, context-length overflows. Those are real and worth escalating on. But notice how much narrower that list is. The tool router escalates on outcome. The model router escalates on mechanics, and handles outcome by classifying difficulty up front — easy requests start (and usually end) on the cheap rung, hard ones start higher.

There's a second divergence: retry semantics. Re-asking a second search index the same query is free of side effects and often productive. Re-asking a second model the same prompt after a *bad* answer you couldn't detect is something you never get to do, because you didn't know. Escalation on quality happens between deployments, not between requests.

## The accounting doesn't even use the same units

Tool calls price per call. A Serper search is $0.001 whether the query is three words or thirty. That makes the ledger trivial: stamp `cost_usd` on the result, sum the column. Frugal's whole receipt model leans on this — one call, one line, one price, and the rung that answered earns the rack credit.

Model calls price per token, in two directions, with the output half unknown until the response finishes. Cost isn't an attribute of the request; it's an outcome of it. Same prompt, different verbosity, 3× cost swing. Any router accounting for both needs two schemas: tools get `cost_usd` as a constant lookup, models get `cost_usd` computed from token counts after the fact — and budgets enforced on models need mid-flight controls (max output tokens) rather than pre-flight arithmetic.

This sounds like bookkeeping trivia. It isn't. A router that budgets model spend the way it budgets tool spend — fixed price per call — will be wrong by whatever your output-length variance is, which for agent workloads is a lot.

## One router, two rulebooks

So: same ladder silhouette, different semantics at every layer.

| | Tool calls | Model calls |
|---|---|---|
| Outcome | Discrete: found / didn't | Continuous: quality gradient |
| Quality signal | In-band, per call | Out-of-band, from evals |
| Health check | Liveness + yield | Statistical drift over days |
| Fallback trigger | Outcome (zero hits, errors) | Mechanics only (errors, schema, refusals) |
| Escalation timing | Mid-request | Mostly pre-request, via classification |
| Pricing unit | Per call, fixed | Per token, variable |

None of this means you need two routers. It means you need two rulebooks inside one, and the discipline not to let either vocabulary colonize the other. The failed designs I've seen all made one of two errors: treating model calls as discrete (ping-healthy, retry-on-bad — except you can't see bad), or treating tool calls as smooth (adding eval machinery to decide whether three search hits are "good enough" when the hit count already told you).

Frugal today routes tool calls, where the discrete rulebook applies cleanly; chat routing is on the roadmap, and getting this distinction right is most of that design work. If you're building your own, start by writing down, for each call type, one sentence: *how do I know this response was good?* If the answer is "the response tells me," you're routing tools. If the answer is "I found out last Tuesday from an eval," you're routing models — and you should go read more on [provider routing](/blog/topics/provider-routing/) with that lens, because nearly every rule changes depending on which sentence you wrote.
