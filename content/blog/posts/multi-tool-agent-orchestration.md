---
title: "Orchestration patterns for multi-tool agents"
slug: multi-tool-agent-orchestration
date: 2026-06-25T16:15
description: "Four agent orchestration patterns — pipeline, fan-out, escalation, verify loop — and where each one quietly hides its cost and latency."
excerpt: "Pipeline, fan-out/fan-in, escalation ladder, verify loop. Each pattern has a signature failure: a place the bill or the latency hides until production."
cluster: building-with-ai
type: educational
keyword: "agent orchestration patterns"
related:
  - latency-budgets-agent-tool-calls
  - provider-fallback-chains
  - deep-research-cost-teardown
---
Once an agent has more than one tool, someone has to decide how the calls compose — in sequence, in parallel, with retries, with a judge at the end. Most teams make that decision implicitly, by whatever the framework does by default, and then wonder where the latency and the bill came from. Agent orchestration patterns are worth learning by name for one reason: each pattern hides its costs in a different place, and you can't budget for what you haven't named.

There are four patterns that cover nearly everything I've built or read. For each: what it is, where the cost hides, where the latency hides, and when it's the right shape.

## Pipeline: the honest workhorse

Search → extract → summarize. Each step's output feeds the next; nothing runs in parallel. This is the pattern most multi-tool agents start as, and for tasks that are genuinely sequential — you can't extract a page you haven't found — it's correct.

**Where latency hides:** in the sum. Pipelines are additive end to end: a 900ms search plus a 1.2s extraction plus a 3s model call is a 5-second floor before anything goes wrong. Every step you append moves the floor, which is why pipelines need a [latency budget per step](/blog/latency-budgets-agent-tool-calls/), not one number for the whole run.

**Where cost hides:** in cascade waste. When step four fails, you've paid for steps one through three and shipped nothing. A pipeline that dies at 80% completion has the cost profile of a finished run and the value of an error message. The mitigations are ordering and checkpoints: put the cheap, failure-prone steps early, the expensive steps late, and make intermediate outputs resumable so a retry doesn't re-buy the whole chain.

## Fan-out/fan-in: parallel and proud of it

Split a task into independent subtasks, run them concurrently, merge the results. Research agents live here: decompose a question into six subtopics, search all six at once, synthesize.

**Where latency hides:** in the straggler. Fan-out's wall-clock time is the *max* of the branches, not the mean. Nineteen branches finish in a second; the twentieth hits a slow provider and the user waits on it alone. Fan-in needs a policy for this — proceed at N-of-M, or a per-branch timeout — or your p99 belongs to your worst branch every time.

**Where cost hides:** in the multiplication. This is the pattern that turns a rounding-error unit price into a budget line: one task becomes 40 or 60 billed queries, and every "be more thorough" instruction widens the fan. I did the full arithmetic in [the deep-research teardown](/blog/deep-research-cost-teardown/) — search hitting half of task cost is a fan-out phenomenon, full stop. Deduplicating branches before dispatch and routing each one down a price ladder attack it directly; trimming the synthesis prompt does not.

## Escalation ladder: cheap first, expensive on failure

Try the cheap option; escalate only if it fails or underperforms. Free search rung before paid rung, small model before frontier model, cached answer before live call. Readers of this blog will recognize it as the shape [fallback chains](/blog/provider-fallback-chains/) and free-first routing share — orchestration and routing are the same idea at different altitudes.

**Where cost hides:** in two places. Escalations that *always* happen — if the cheap rung's success rate on your traffic is 10%, the ladder is ceremony and you're paying its overhead to end up at the expensive rung anyway. And escalation criteria that misfire: judge "failure" too strictly and you escalate constantly; too loosely and bad cheap answers ship. The zero-hit rule from routing generalizes here — a partial free rung coming up empty means fall through; a full-coverage paid rung coming up empty means stop, because escalating past it just pays more to confirm nothing.

**Where latency hides:** in the climb. Every failed rung adds its latency before the successful rung starts. A three-rung ladder where each rung takes a second delivers three-second answers to exactly the queries the cheap rungs couldn't handle. If a class of request predictably escalates, let it start higher — the ladder should have an express elevator for known-hard traffic.

## Verify/judge loop: quality on the meter

Generate, then check — a second model call grades the first, or a checker validates tool output against the source, looping until it passes or a retry cap trips.

**Where cost hides:** in plain sight, which somehow still surprises people. One verification pass doubles the model calls for that step. A generate-judge-regenerate loop can triple them. The failure mode is applying it uniformly: verifying every output, including the trivial ones, because the loop was easy to wrap around everything. Verification should be *targeted* — high-stakes outputs, historically error-prone steps — and its pass/fail rates should be measured, which makes it a cousin of the [evals-before-scale](/blog/evals-before-scale/) discipline: an unexamined judge is just double spend with a confident tone.

**Where latency hides:** in serialization. The judge can't start until generation finishes, so the loop is a pipeline in disguise — additive, and unbounded if you forget the retry cap. Set the cap low. If output fails verification twice, a third generation attempt rarely saves it; escalating to a better model or a human does.

## Picking a pattern per task shape

The patterns aren't ranked; they're matched. The task's structure tells you which one fits:

| Task shape | Pattern | Watch for |
|---|---|---|
| Steps depend on each other | Pipeline | Additive latency, cascade waste |
| Independent subtasks | Fan-out/fan-in | Straggler p99, multiplied spend |
| Same task, options at different prices | Escalation ladder | Ladders that always escalate |
| Errors are costly, checkable | Verify loop | Doubled calls, uncapped retries |

Real agents compose them — a fan-out whose branches are pipelines, an escalation ladder whose top rung includes a verify loop. Composition multiplies the hidden costs too: a verify loop inside a 50-branch fan-out is 50 extra model calls per task, a number worth computing before you ship it rather than after.

The meta-rule: for whatever pattern you pick, write down where it hides cost and where it hides latency, then instrument exactly those places — per-branch `latency_ms` for fan-outs, escalation rates for ladders, judge pass-rates for loops. Orchestration debugging is unpleasant precisely because the structure spreads the evidence across many calls; stamping every call is how you get it back. More on the craft of building these systems lives on the [building-with-ai](/blog/topics/building-with-ai/) hub. Start with the simplest pattern the task shape allows — it's the one whose hiding places you'll actually check.
