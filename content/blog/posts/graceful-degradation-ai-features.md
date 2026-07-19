---
title: "Graceful degradation for AI features"
slug: graceful-degradation-ai-features
date: 2026-08-11T14:20
description: "Graceful degradation for AI features means designing the cheaper model, cached answer, and honest failure states on purpose — and testing them."
excerpt: "Every AI feature already degrades. The only question is whether you designed the degraded states or your users are discovering them for you."
cluster: reliability
type: educational
keyword: "graceful degradation"
related:
  - ai-agents-provider-outages
  - provider-fallback-chains
  - timeouts-product-decision
---
Every AI feature degrades. A provider has a bad afternoon, a rate limit trips, a query lands outside the index, a model call times out — and your feature returns *something*. The only question is whether that something was designed or improvised. Graceful degradation means the degraded states of your AI feature are product states you built on purpose, with their own UX, their own cost profile, and their own tests — not whatever your exception handler coughs up at p99.

Most teams design exactly one state: the happy path. Everything else is an error boundary and an apology. That's backwards, because for a feature built on other people's APIs, the unhappy paths aren't edge cases. They're weather.

## Failure is a spectrum, not a boolean

The instinct is to treat an AI feature as working or broken. But between "perfect answer in two seconds" and "500" there's a wide band of states your system can land in, and most of them are still worth something to the user:

- The premium provider is down, but a cheaper one is up.
- The fresh answer is unavailable, but you computed this answer yesterday.
- The full research task won't finish in budget, but three of five sub-questions did.
- Nothing found anything — which is itself information.

A feature that collapses all of these into one error state is throwing away value it already paid for. The engineering work of graceful degradation is mostly *naming* these intermediate states and deciding what each one shows the user.

## Design the degraded states on purpose

Four degraded states cover most AI features. Each is a real product state, so each deserves a real product decision.

**The cheaper rung.** When the preferred model or provider is unavailable or over budget, serve the request from a cheaper one. A smaller model's answer to "summarize this page" is usually fine; the user would rather have it in two seconds than your best model's version never. The decision to make on purpose: which requests tolerate the cheap rung and which don't. That's an eval question, not a guess.

**The cached or stale answer.** If you computed something yesterday, "here's yesterday's answer" beats a spinner that dies. Staleness is a product parameter — an hour-old company brief is fine, an hour-old stock quote is not — so set the TTL per feature and *label the state honestly* ("as of yesterday"). Users forgive stale. They don't forgive stale presented as fresh.

**The smaller scope.** When the full task can't complete — budget exhausted, providers flapping, timeout approaching — ship the part that did. Three answered sub-questions with a note about the missing two is a useful artifact. This requires your orchestration to produce partial results as first-class outputs rather than holding everything until the end, which is an architecture choice you make early or retrofit expensively.

**The honest miss.** Sometimes there is no answer. The degraded state for "we found nothing" is a designed empty state that says so plainly — not a hallucinated guess, and not a generic error that makes the user retry a query that will never hit. I've written about why [zero hits is an answer, not a failure](/blog/zero-hits-empty-results/); the UX corollary is that "couldn't find it" deserves its own screen. Honest misses build the trust that lets users forgive the other degraded states.

## Degradation ladders mirror provider ladders

If you route tool calls down a price ladder, you've already built half of this. A [fallback chain](/blog/provider-fallback-chains/) — try the free provider, fall through to cheap paid, end the chain when a paid rung says no hits — is a degradation ladder viewed from the infrastructure side. The product-side ladder is the same structure one level up: preferred experience, cheaper experience, cached experience, partial experience, honest miss.

The two ladders should be designed together, because each infrastructure rung implies a product state. When the router falls from the premium provider to a free one, does the product say anything? When the chain ends with zero hits, which empty state renders? A router that stamps its results — `provider_used`, `cost_usd`, `latency_ms` — gives the product layer exactly the signal it needs to pick the right state instead of guessing. And the ladder is your [outage strategy](/blog/ai-agents-provider-outages/) too: a routed feature degrades one rung when a provider dies, while a hardcoded one degrades to a blank screen.

Timeouts are where the ladders meet most concretely. Each rung needs its own time budget, and the sum has to fit inside what the user will wait — which makes [the timeout a product decision](/blog/timeouts-product-decision/), not a config default. A degradation ladder with one global 30-second timeout is a ladder with one rung.

## Test the degraded paths like real features

Here's where most graceful-degradation efforts quietly rot. The degraded states get designed, shipped — and never exercised again, because the happy path works in staging and nobody's provider is down during the demo. Six months later the premium API has an outage, the fallback path runs for the first time since March, and it turns out a schema change broke the cached-answer renderer in May. You degraded, un-gracefully, in front of everyone.

Degraded paths are code paths. Treat them like it:

- Unit-test each state's trigger: provider error, zero hits, budget exhausted, timeout.
- Force the states in staging on a schedule — kill the premium rung for an hour and watch what users would have seen. Chaos engineering, at the scale of one config flag.
- Put the degraded states in your evals. "Cheap-rung answer quality" is a metric; track it, or the cheap rung will drift below acceptable without anyone noticing.
- Alert on *rate*, not existence. Degradation is normal; the pager fires when the fallback rate jumps, because that means a rung above it is failing.

A useful forcing function: every degraded state should appear in your product spec with a screenshot, same as the happy path. If you can't screenshot it, you haven't designed it.

Start with an inventory. List your AI features, and for each one write down what the user sees when the provider is down, when the answer is stale, when the budget runs out, and when there's genuinely nothing to find. Most teams find blanks in that table — states that exist in production but were never designed. Fill the blanks in order of how often each state actually fires, which your ledger already knows. That's the whole practice; the rest is the same reliability discipline I keep returning to on the [reliability](/blog/topics/reliability/) hub — the unhappy paths are the product, most days.
