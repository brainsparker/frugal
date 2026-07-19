---
title: "The hidden cost of retries"
slug: hidden-cost-of-retries
date: 2026-08-20T14:45
description: "API retry cost is a multiplier on spend and tail latency. A playbook: retry budgets per task, when not to retry at all, jitter, and falling sideways."
excerpt: "Every retry is a repurchase at full price with worse odds than the first attempt. Here's the playbook: budgets per task, hard no-retry rules, and fallbacks."
cluster: ai-costs
type: problem-solution
keyword: "API retry cost"
related:
  - zero-hits-empty-results
  - provider-fallback-chains
  - ai-agents-provider-outages
---
A retry is a purchase. Same request, same rack rate, and strictly worse odds than the first attempt — because something already went wrong once. Most retry logic gets written in five minutes, wrapped around every call site, and never looked at again, which is how API retry cost becomes the quietest multiplier on an agent bill. Nobody decided to spend 3× on a failing provider. The loop decided.

The arithmetic is unglamorous. Three attempts per call at $0.005 is $0.015 for one result — you've turned the priciest rung of the search ladder into something 3× pricier than its own rack rate. And an agent makes the problem recursive: a research task that fans out into 40 tool calls, each wrapped in retry-3, has a worst case of 120 paid attempts. The task budget you thought you set was never the real budget. The real budget was the retry policy, and nobody reviewed it.

## Retries multiply spend and latency at the same time

The bill is only half the damage. A retry with backoff is a latency event too: attempt, wait, attempt, wait, attempt. Three tries with even modest backoff can turn a 2-second call into a 15-second one, and in a serial agent loop every downstream step inherits the delay. Your p50 won't show it. Your p99 is made of it.

This is the nasty property of retries: they concentrate their cost in exactly the moments the system is delivering the least value. When a provider is healthy, the retry logic is dormant and free. When the provider degrades, every call starts paying the full retry tax at once — the multiplier kicks in fleet-wide, spend spikes, latency spikes, and throughput drops, all during the window when your results are at their worst. [Provider outages become invoices](/blog/ai-agents-provider-outages/) precisely through this mechanism. The retry storm is the outage's billing arm.

## Budget retries per task, not per call

The standard fix — cap retries at N per call — is better than nothing and still wrong, because the call is the wrong unit of account. A task that makes 40 calls with a per-call cap of 2 still has a worst case of 80 extra attempts. The cap you actually care about is on the task.

So give the task a retry budget, the same way you give it a spend ceiling or a [latency budget](/blog/latency-budgets-agent-tool-calls/). Something like: this research run gets 5 retries, total, across all its tool calls. Each retry spends from the shared pool; when the pool is empty, failures become fallbacks or partial results instead of repurchases. Two properties fall out of this that per-call caps can't give you:

- The worst case is bounded at the level you actually pay at. 40 calls + 5 retries = 45 attempts, full stop, no multiplication.
- Retries get spent where they help. A transient blip early in the task can take two retries; a provider that's clearly down can't drain the pool one call site at a time, because the pool is visible to the whole task.

Implementation is small: a counter in the task context, decremented on every retry, checked before each one. The hard part is only the decision that a failed task is acceptable output — which it is, because the alternative is an unbounded bill for the same failure.

## When retrying is simply wrong

Before any backoff math, sort the failure. Retries are for transient faults. A surprising share of what agent loops retry is deterministic, and retrying a deterministic failure is paying to re-run a proof.

**Zero hits is not a failure.** A search that returns an empty result set succeeded — the answer is "nothing matched." Retrying it re-asks a question that was already answered. I've written a whole post on [zero-hit semantics](/blog/zero-hits-empty-results/); the retry-relevant rule is the one Frugal's router enforces: a *free* provider returning zero hits falls through to the next rung, but a *paid* provider returning zero hits ends the chain. Don't pay a pricier provider to confirm no.

**4xx is a message, not weather.** A 400 says your request is malformed; it will be malformed on attempt two. A 401/403 says your key is bad; it will still be bad. A 404 says the thing isn't there. None of these improve with repetition. The one 4xx worth special-casing is 429 — and even that isn't "retry," it's "wait as instructed," honoring Retry-After, spending from the retry budget while it waits.

**Deterministic failures generally.** Same input, same code path, same provider state, same result. A parse error on the provider's response, an over-length input, a schema the endpoint doesn't accept — retrying these buys you an identical failure at full price. The retryable set is small and specific: timeouts, connection resets, 5xx, 429. Everything else, fail fast and route around.

## If you retry, jitter — or you've built a stampede

For the failures that do merit a retry, immediate re-attempt is self-sabotage: you're hitting a struggling provider at the moment it's struggling. Exponential backoff fixes half of that. Jitter fixes the other half. Without it, every client that failed at 14:02:07 retries in lockstep at +1s, +2s, +4s — synchronized waves that re-create the overload that caused the failures. Full jitter (sleep a random duration between zero and the backoff ceiling) decorrelates the waves. It's one line, and it's the difference between backing off and taking turns stampeding.

## Falling sideways beats trying again

Here's the option most retry logic never considers: don't re-ask the same provider — ask a different one. Retry-same-provider re-rolls the dice on whatever condition just failed. Fallback-to-a-different-provider changes the condition. If Serper is timing out, the second attempt against Serper inherits the timeout risk; the first attempt against a different rung doesn't.

For agent tool calls this is usually strictly better, because the providers are substitutes: five ways to run a web search, several ways to extract a page. A [fallback chain](/blog/provider-fallback-chains/) turns "retry" into "next rung" — and when the ladder is ordered cheapest-first, the fallback path is often *cheaper* than the failing one, not just healthier. A reasonable composite policy: one same-provider retry for genuinely transient faults (it preserves any cache or ranking affinity), then sideways. Never three attempts at the same closed door with two open ones adjacent.

## Where to start

Read your retry config before touching code — every wrapper, SDK default, and hand-rolled loop. Most teams find at least one call path with nested retries: an SDK retrying 3× inside an application loop retrying 3×, a silent 9× multiplier that has been in production since day one. Then, in order: classify failures into retryable and not (kill retries on 4xx and paid-rung zero hits today), move the budget from per-call to per-task, add jitter, and put a fallback rung behind your most-retried provider.

And log every attempt — not just the winning one — with cost and latency attached, because attempts you can't see are [spend you can't audit](/blog/topics/ai-costs/). The gap between attempts logged and results delivered is your retry tax, printed as a number. Most teams have never seen theirs.
