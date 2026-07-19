---
title: "Small models first: the case for escalation"
slug: small-models-first
date: 2026-05-21T15:30
description: "Why small language models should be the default rung, with escalation to big ones on defined signals — and honest answers to the objections."
excerpt: "Escalation isn't a cost hack, it's an architecture: the cheap model handles the bulk, the expensive one earns its rung. The same ladder logic that works for tool calls."
cluster: model-selection
type: strategic
keyword: "small language models"
related:
  - how-to-choose-a-model-for-agents
  - what-is-an-llm-router
  - provider-routing-for-ai-agents
---
The default architecture for an AI product in 2026 is still: pick the best model you can afford, send it everything. I think that default is backwards. The right default is a small model handling the bulk of calls, with escalation to a bigger one on defined signals — and I'd argue this even if the small rung saved no money at all, though it saves plenty.

Small language models are good now. Not "good for their size" — good enough that for a large share of the calls in a real system, the frontier model's advantage is unobservable in the output. That fact hasn't propagated into architectures yet, because the industry's mental model is still "the model" (singular) when the reality of any agent loop is a stream of wildly heterogeneous calls, most of them mundane.

## Most calls are plumbing

Watch what an agent actually does per task: rewrite a query, judge whether a search result is relevant, extract fields from a page, decide whether to keep looping, format an answer. Then, occasionally: synthesize twelve sources into a coherent recommendation, or reason through a genuinely ambiguous case.

These are different jobs. The plumbing calls dominate by count — and the plumbing is exactly where small models match big ones, because the tasks are narrow, the context is short, and success is verifiable. Sending "is this search result relevant, yes or no" to a frontier model is paying for reasoning capacity the task cannot express. The one-model default persists not because it's right but because it's *one decision*, made once, on day one, when the roadmap was more interesting than the invoice. I made the longer version of this argument in [how to choose a model for agent work](/blog/how-to-choose-a-model-for-agents/): "which model?" is a per-call question wearing a procurement costume.

Escalation makes that per-call answer systematic. Small model by default; big model when a signal fires. The signals should be written down, not vibes: the small model declines or hedges; its output fails a validation check (schema, citation present, self-consistency); the task class is on an escalation list your evals put there; retry-after-failure. Each escalation is logged with a reason. That last part matters more than it looks — an escalation log is an ongoing measurement of exactly where the small model's ceiling is, on your traffic, updated continuously for free.

## The same ladder, one layer up

If you've read anything I've written about [routing tool calls down a price ladder](/blog/provider-routing-for-ai-agents/), you've seen this shape before. Frugal routes a web search to free providers first — Wikipedia, Marginalia — and only falls through to paid rungs when the free rung returns nothing. The identical search costs $0 or $0.005 depending on who answers it, so the ladder exists to make the expensive rung *earn its place, call by call*.

Small-first model selection is the same architecture one layer up. The small model is the free-ish rung. The frontier model is the paid rung. Escalation signals are the fall-through rules. And the same accounting discipline applies: every response should say who answered and what it cost, or the ladder is faith-based. A [router](/blog/what-is-an-llm-router/) — whether for models or tools — is just this ladder plus health signals plus receipts.

The symmetry isn't cosmetic; the same objections and the same answers apply at both layers. One difference is worth respecting, though. A tool call's failure is usually cheap to detect — zero hits is zero hits. A model call's failure can be a confident wrong answer, which is why escalation signals need more care than fall-through rules do. Verifiable plumbing tasks escalate on validation failures, which are mechanical. Open-ended reasoning tasks are harder to gate, and it's legitimate to route them straight to the top rung. Escalation doesn't require that every call start small. It requires that starting big be a decision with a reason, instead of the absence of one.

## Objection one: quality anxiety

"The small model will be wrong sometimes, and we won't catch it." True. Here's what the objection quietly assumes: that the big model's errors are being caught. They aren't — big models are wrong less often, but with equal confidence, and a team that sends everything to the frontier model has usually stopped checking outputs at all, because the model is "the best we can get." The relevant comparison was never small-model errors versus zero errors. It's a system *designed around the possibility of being wrong* — validation checks, escalation paths, eval sets — versus a system that outsourced that worry to a bigger model and moved on.

The honest concession: escalation gates leak. A validation check catches malformed output, not subtly wrong output, and some fraction of calls that deserved the big model won't get it. If a single bad answer is catastrophic for you — medical, legal, safety-critical — then run the top model on everything and treat the spend as insurance; that's a defensible engineering decision. For the much larger class of products where an occasional mediocre answer costs a retry, quality anxiety is mostly loss aversion with a benchmark chart. Evals on your own traffic, not leaderboard deltas, tell you which class you're in.

## Objection two: added complexity

"Now I have two models, a routing layer, escalation rules, and a new failure mode." Also true, and I won't pretend the complexity is zero. It's real code with real bugs.

But compare it to the complexity you already accepted without flinching: retries, caching, rate-limit handling, fallback providers for when your primary has a bad day. Escalation is the same genus of scaffolding — a conditional and a second endpoint — and it's *less* complex than most of that list because it runs on the happy path where you can see it working. A first version is genuinely small: one validation function, one `if`, one log line with the escalation reason. You don't need a classifier predicting difficulty; fall-through on failure gets most of the value, exactly as it does for tool routing.

There's also a complexity credit people miss: an escalation architecture makes model swaps boring. When a better small model ships — and one ships every few months — you swap the bottom rung, run your evals, and watch the escalation rate. The one-model architecture makes every model change a migration; the ladder makes it a config edit. Some complexity buys optionality. This is that kind. And the cost side compounds quietly: in a loop where plumbing calls outnumber reasoning calls, the bottom rung's price sets the bill, which is why small-first shows up in every serious [cost-cutting list](/blog/cut-ai-agent-api-costs/) I've seen or written.

## Defaults are the strategy

Strip the details and the argument is one sentence: in any system that consumes a metered resource, the default rung should be the cheapest one that works, and the expensive rung should have to prove it's needed — per call, with receipts. That's free-first for tool calls. It's small-first for model calls. The stack that treats both as routing problems gets cheaper and *more* measured over time, because every escalation and every fall-through is a data point about where capability actually runs out.

If you want to try it, start embarrassingly small: pick your single highest-volume plumbing call, point a small model at it, validate the output, escalate on failure, and log the escalation rate for two weeks. That number — not a benchmark, not a hunch — tells you what the next rung down is worth. More in this vein under [model selection](/blog/topics/model-selection/), where most of the interesting questions turn out to be routing questions in disguise.
