---
title: "Fallback chains: designing a provider ladder that doesn't page you"
slug: provider-fallback-chains
date: 2026-05-26T13:35
description: "An API fallback strategy only works if errors, timeouts, and zero hits get different handling. How I order, break, and test a provider ladder."
excerpt: "A backup provider you've never exercised is a config value, not a plan. The rules I use to order rungs, avoid paying twice, and break flapping providers out of the chain."
cluster: provider-routing
type: problem-solution
keyword: "API fallback strategy"
related:
  - provider-routing-for-ai-agents
  - ai-agents-provider-outages
  - what-is-an-llm-router
---
Most fallback chains get written twice. Once in the design doc, calm and orderly. Again at 2 a.m., after the primary went down and the "backup" turned out to be a config value nobody had exercised since it was added. An API fallback strategy that has never actually run is not a strategy. It's a comfort object.

I run Frugal, an MCP server whose entire job is walking agent tool calls down a ladder of providers, so I've had to get precise about what makes a chain hold up under real traffic. Not "have a fallback" — everyone has a fallback. The details are where the paging happens: how you order the rungs, how you classify what came back, and whether you ever pay twice for one answer. Here's the design that works for me, and there's more on [provider routing](/blog/topics/provider-routing/) if you want the broader argument.

## Order the rungs: cost first, then quality fit

The default ordering is boring and correct: cheapest first. For web search my ladder starts at $0 (Wikipedia, Marginalia, a self-hosted SearXNG), then $0.001/call, then $0.005/call. That's [the same query at a 5× gap between the two paid rungs alone](/blog/rack-rate-gap-web-search-costs/), so ordering by price is not a rounding error. It's most of the money.

But cost is only the first sort key. The second is quality fit, and it's per-query-shape, not global. Marginalia is genuinely good at essays, documentation, and the indie web — and weak on news and product pages. Wikipedia covers entities and not much else. A ladder that ignores this burns a rung on a query it was never going to answer. The fix isn't to promote the paid rung; it's to know which free rungs are plausible for which shapes and skip the implausible ones. Cheapest-plausible-first, not just cheapest-first. If that sounds like a router, it is — [that's what a router does](/blog/provider-routing-for-ai-agents/) once you write the logic down.

## An error, a timeout, and zero hits are three different signals

This is the mistake I see most: one catch block for everything. The provider "failed," so fall through. But the chain sees three distinct signals, and they deserve three distinct responses.

| Signal | What it means | What to do |
|---|---|---|
| Error (5xx, auth, malformed) | The rung is broken | Fall through now; count it against the rung's health |
| Timeout | The rung might be fine, just slow | Fall through; log it; don't immediately damn the rung |
| Zero hits | The rung worked and found nothing | Depends on whether the rung was free or paid |

Errors are the easy case. Fall through immediately and increment a failure counter — you'll want it later.

Timeouts are subtler because a timeout is a decision you made, not a fact about the provider. A rung that answers in 4 seconds against your 3-second budget isn't down; it's slower than you'll tolerate. Fall through, but keep the accounting separate from hard errors, and be honest that the number came from your latency budget, not theirs.

Zero hits is the one almost everyone gets wrong, because an empty result set arrives as a 200 and looks like success. It is success — the provider searched and found nothing. Whether you should fall through depends on why it found nothing: coverage gap, or genuinely no results. My rule of thumb: a free rung returning zero hits falls through (it cost nothing to ask, and free rungs have narrow coverage), while a paid rung returning zero hits usually ends the chain. That asymmetry deserves its own post; for now, just don't let empty and error share a code path.

## Don't pay for the same answer twice

Every double-pay bug I've found comes from one of three habits.

Hedged requests. Racing two providers and taking the fastest is a fine latency trick — across free rungs. Race two paid providers and you buy two answers to use one, every time, by design. Hedge free-vs-free, never paid-vs-paid.

Retrying slow success. You time out a paid call at 3 seconds, retry, and the retry succeeds — but the original call also succeeded at 4.1 seconds, on their side, on your invoice. Retry paid rungs on transport errors only. A slow paid call falls through to the next rung; it doesn't get a second paid attempt at the same rung.

Escalating past a paid zero. A paid provider with a serious index searched and found nothing. Paying a pricier provider to confirm the nothing is the purest form of double-pay: two bills, zero results.

## Circuit-break the flapping rung

A rung that's hard down is cheap — it errors fast and you move on. The expensive rung is the flapping one: failing one call in three, or eating your full timeout before dying. Without protection, every request donates a timeout's worth of latency to a provider that mostly can't answer. [Provider outages](/blog/ai-agents-provider-outages/) rarely look like a clean flatline; they look like this.

You don't need a resilience framework. A counter and a timestamp per rung will do: after N consecutive failures (I use 3), skip the rung entirely for a cooldown window, then let one probe request through. Probe succeeds, rung rejoins the ladder; probe fails, cooldown restarts. The point isn't sophistication — it's that a chain which keeps knocking on a dead door isn't a fallback chain, it's a latency tax with extra steps.

One wrinkle: zero hits never trips the breaker. The rung answered. Coverage is not health.

## Test the chain before the outage does

Chains fail at the seams, and the seams only get exercised when something upstream breaks. So break it yourself, on your schedule:

- Revoke the primary's key in staging and run real traffic. Does rung two actually answer, or does the chain throw?
- Inject a timeout on rung one. Does the total stay inside your latency budget, or do timeouts stack?
- Send a query you know has no results. Does the chain stop where your zero-hit rules say it should?
- Check the receipt. Every response should say which rung answered — a `provider_used` field, at minimum. If you can't see who answered, you can't see that your fallback fired at all, and plenty of "working" chains have been quietly serving 100% from the expensive rung for months.

Then put a canary on it: a scheduled synthetic query against each rung directly, so you learn a rung died from a dashboard instead of from a user.

The place to start is smaller than any of this. Open your config, find your fallback provider, and ask when it last served a real request. If the answer is "never" or "unknown," you don't have an API fallback strategy yet — you have rung one and a rumor. Kill rung one on purpose this week and watch what happens. Everything above is just what you'll end up building after you see it.
