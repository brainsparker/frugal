---
title: "The agent cost audit: 8 numbers to pull this quarter"
slug: agent-cost-audit
date: 2026-09-29T14:35
description: "An AI cost audit in 8 numbers: cost per task, paid-call share, fallback and zero-hit rates, retry spend — how to pull each one from your logs."
excerpt: "Not a dashboard project. Eight numbers you can compute from a call log in an afternoon, and the specific fix each bad number points at."
cluster: ai-costs
type: listicle
keyword: "AI cost audit"
related:
  - agent-cost-observability
  - receipt-for-every-agent-run
  - hidden-cost-of-retries
---
An AI cost audit doesn't need a vendor, a dashboard, or a quarter of engineering time. It needs a call log and eight queries. If you've been logging every external call with a provider, a price, a latency, and a task ID — the setup I described in [agent cost observability](/blog/agent-cost-observability/) — each number below is a few lines of scripting against a JSONL file. If you haven't, that's finding number zero, and the fix is a week of instrumentation before the audit can start.

Each number comes with two things: how to compute it, and what a bad value tells you to fix. That second half is the point. A metric that doesn't name its own remedy is decoration.

## 1. Cost per task

Sum `cost_usd` over every call sharing a task ID; average and distribution across tasks. This is the audit's anchor — the number that turns "our API bill went up" into "research tasks cost $0.31 and tripled since June."

Look at the distribution, not just the mean. A fat right tail — a few tasks costing 20× the median — usually means runaway loops or pathological inputs, and trimming the tail is cheaper than optimizing the middle. If you can't compute this number because calls don't carry task IDs, stop here and fix that first. Every remaining number is this one, sliced.

## 2. Cost per successful result

Same numerator, stricter denominator: total spend divided by tasks that *succeeded* — by whatever definition your product already uses. Failed tasks cost money too, and this number makes them show up in the unit economics instead of hiding in the average.

The gap between numbers 1 and 2 is the interesting part. If cost per task is $0.20 and cost per successful result is $0.55, you're not overpaying per call — you're paying full price for a 36% success rate, and the fix is reliability work, not rate shopping. This is the number I'd put in front of anyone pricing a feature, for reasons I covered in [a receipt for every agent run](/blog/receipt-for-every-agent-run/).

## 3. Paid-call share

Of all tool calls, what fraction had `cost_usd > 0`? Group by tool: paid share of searches, of extractions, of browses.

If you run a free-first ladder, this is the ladder's report card — my own searches resolve at $0 most of the time, because Wikipedia and Marginalia catch the long tail of entity and documentation lookups. A paid share near 100% means either your ladder has no free rungs (add them: the spread between free and premium search is $0 versus $0.005 per call) or the free rungs never answer (check that fall-through actually works — a misconfigured free rung that always errors makes the ladder a decoration).

## 4. Fallback rate

How often did the first rung fail to answer, forcing the chain to a later rung? Compute from `provider_used`: any result carrying a provider other than rung one is a fall-through. Split by reason if your logs allow it — error versus zero hits behave differently.

A high fallback rate isn't automatically bad; it's the ladder doing its job. But a *rising* one is drift: a free index degrading, a query mix shifting toward news the free rungs can't cover, a quota quietly shrinking upstream. Fallbacks also carry a latency toll — the chain pays for each rung it walks — so this number moves your tail latency too.

## 5. Zero-hit rate

What fraction of search calls returned nothing, per provider? Free rungs will show a healthy nonzero rate; that's their partial indexes talking, and fall-through handles it. The number to interrogate is paid zero hits, because those cost real money to learn "nothing."

A paid zero-hit rate climbing past a few percent usually means malformed queries — over-long conversational strings, hallucinated site: filters, dates that don't exist. The remedy typically lives in the prompt or the tool description, not the provider. And check the semantics while you're here: if your chain pays a *pricier* provider to confirm a cheaper paid provider's zero hits, you've found the purest waste in the whole system. Zero is an answer.

## 6. Retry share of spend

Of total `cost_usd`, what fraction was spent on calls that were retries of a previous call? Tag retries explicitly in the log; inferring them later from timing and matching arguments is miserable.

Single digits is normal weather. North of 10% means some provider had a bad month and your agents paid for it — go read [the hidden cost of retries](/blog/hidden-cost-of-retries/) — or your backoff is retrying things that can't succeed, like 4xx responses or paid zero hits. Cross-reference with number 4: a retry spike plus a fallback spike on the same dates is the signature of a stampede, where a rung's failure sent the whole fleet thundering into the next one.

## 7. p95 latency per rung

For each provider, the 95th percentile of `latency_ms`. Cost audits that ignore latency get vetoed by whoever owns the user experience, and they're right to veto: a rung that saves $0.001 and costs 3 seconds at p95 isn't cheap, it's slow with a discount.

This number is what makes ladder-ordering decisions honest. Median tells you the happy path; p95 tells you what the chain adds when it walks. My captured runs put Marginalia's misses around 529ms and Wikipedia's answers under a second — a fall-through that costs half a second and zero dollars is a fine trade for most workloads, but that's a decision to make with the number in hand. The case for logging it at all is one I've [made at length](/blog/why-log-latency-ms/).

## 8. The rack-rate counterfactual

Take every call that got answered, price it at your premium provider's list rate, and sum. That's what the same quarter would have cost with no ladder, no caching, no discipline — the counterfactual bill. My ledger computes this continuously; `frugal stats` prints it as a monthly receipt where only the rung that produced the result earns rack credit.

This is the audit's political number. "We spent $412" gets a shrug. "We spent $412 on traffic that lists at $2,650" gets the routing work funded for another year. And if the two numbers are nearly equal, the audit just told you where next quarter goes: every call is paying list price, which means the ladder — the thing every other number here measures — doesn't exist yet.

## Where to start

Pull numbers 1 and 3 this week; they take an hour each against any log with prices and task IDs, and together they tell you whether your problem is volume, rate, or reliability. Schedule the rest across the quarter. Then keep the queries — an audit you run once is archaeology, and one you run monthly is a control system. More in this vein on the [AI costs](/blog/topics/ai-costs/) hub, but the whole discipline compresses to one habit: stamp every call with what it cost, and the audit becomes eight greps instead of a project.
