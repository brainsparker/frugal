---
title: "Free-first: why your default provider should cost $0"
slug: free-first-default-provider
date: 2026-06-16T14:40
description: "Free AI tools for agents aren't a compromise — they're the correct default. Why the $0 rung should answer first, and how a receipt proves it works."
excerpt: "Paid providers should be the fallback, not the default. The case for making your agent's first call free, with the failure modes stated honestly."
cluster: provider-routing
type: strategic
keyword: "free AI tools for agents"
related:
  - provider-routing-for-ai-agents
  - rack-rate-gap-web-search-costs
  - small-models-first
---
Every tool call your agent makes has a default provider, and for most agents that default is the most expensive option in the stack. Not because someone compared prices and chose it — because it had the best SDK the week the agent got built. I think that default is backwards. Free AI tools for agents — Wikipedia's API, Marginalia, a self-hosted SearXNG, a local readability extractor — should be the first rung, and every paid provider should have to earn its place per call.

This is the core belief behind everything I've built into Frugal, so I want to argue it properly: the strongest version of the case, and then the honest objections, because the objections are real.

## The default is where the money goes

Defaults compound. A deliberate choice happens once; a default happens on every call, forever, without anyone looking at it. If your agent makes 3,000 searches a month and the default rung is $0.005/call, the default costs $15/month. If the default is $0 and the paid rung only fires when the free rung comes up empty, the paid rung bills for its actual contribution — and nothing else.

The [rack-rate gap](/blog/rack-rate-gap-web-search-costs/) makes this concrete: the identical web search costs anywhere from $0 to $0.005 depending on who answers. That's not a spread between good and bad results. For a large class of queries — entity lookups, background facts, documentation, "what is X" — the free answer and the $0.005 answer are the same answer. Paying for those calls buys nothing. The only reason it keeps happening is that the paid provider sits in the default slot.

So invert the slot. Free first, paid as fallback. The paid rung stops being a tax on every call and becomes what it should have been all along: a specialist you call when the generalist comes up empty.

## "But the free rung is worse" — yes, and here's exactly how

I'm not going to pretend the $0 rung is a Google-grade SERP. It isn't, and the argument doesn't need it to be.

Marginalia is genuinely good at essays, documentation, and the indie web — and weak on news and product pages. Wikipedia covers entities and settled facts; ask it about a startup founded last quarter and you'll get zero hits. A self-hosted SearXNG is broader but you're operating it yourself. These are partial indexes with real gaps, and anyone who tells you otherwise is selling something.

Here's why that's fine: free-first doesn't ask the free rung to answer everything. It asks the free rung to answer *first*, and to fail cheaply when it can't. The semantics matter — a free provider returning zero hits falls through to the next rung, because its empty result means "not in my corner of the web," not "this doesn't exist." I went deep on this distinction in [what zero hits actually means](/blog/zero-hits-empty-results/). The design absorbs the free rung's weakness. A gap in Marginalia's index costs you one fall-through, not one wrong answer.

Latency is the sharper objection. A fall-through isn't free in time: a real run from my own ledger shows Marginalia returning 0 hits in 529ms before Wikipedia answered with 3 hits at $0 in 954ms. That's a half-second of ladder-walking a hardcoded premium call wouldn't have spent. For a latency-critical interactive path, that can be disqualifying — so start that path a rung higher, deliberately. Free-first is a default, not a dogma. The point is that skipping the free rung should be a decision you write down, not an accident of which SDK got installed.

Reliability cuts the other way than people expect. Yes, a volunteer-run index has less uptime engineering behind it than a funded API. But free-first architectures are fallback architectures by construction — when any rung has a bad day, the chain routes around it. The hardcoded-premium agent is the one with a single point of failure, as anyone who has [ridden out a provider outage](/blog/ai-agents-provider-outages/) can confirm.

## Paid must earn its place — per call, not per quarter

The usual way teams justify a paid provider is annually and in aggregate: "we need real search, we pay for real search." Free-first replaces that with a per-call test. Every dollar in the ledger corresponds to a query the free rung actually failed, which means the paid spend is, by construction, the useful spend.

This does something subtle to procurement logic. You stop asking "is this provider worth $X/month?" — a question nobody can answer — and start asking "on the calls where it fired, did it deliver hits the free rung couldn't?" That question has an answer in your logs. It's the same discipline as [small models first](/blog/small-models-first/) applied to tools instead of models: the expensive option isn't banned, it's audited.

## The receipt is the proof

A policy you can't verify is a vibe. The reason I trust free-first isn't the argument above — it's that every result in my stack comes back stamped with `provider_used`, `cost_usd`, and `latency_ms`, appended to a JSONL ledger that never leaves my machine. `frugal stats` turns that into a monthly receipt: which rungs answered, what the traffic would have cost at rack rate, what it actually cost. Only the call that produced the result earns rack credit, so the savings number isn't inflated by fall-throughs.

When the receipt says most of the month's searches were answered at $0, that's not a projection from a pricing page. It's a count of things that happened. And when the receipt says the paid rung fired often — that's information too. Maybe your workload is news-heavy and the free rung genuinely can't carry it. Fine: now you know, from evidence, and you can restructure the ladder instead of arguing about it. The receipt keeps the policy honest in both directions.

## Where free-first breaks, and where to start

Free-first is wrong for some workloads, and it's worth saying which. Hard real-time paths where 500ms of fall-through blows the budget. Query mixes dominated by fresh news and commercial pages, where the free rungs' hit rate is low enough that the ladder is mostly ceremony. Compliance environments where every upstream provider needs a signed agreement. In those cases, start the ladder at the cheap-paid rung and keep the receipts anyway.

For everyone else: pick one tool — search is the highest-volume candidate — and put one free provider in front of your current one, with fall-through on errors and on free-rung zero hits. Stamp the cost on every result. Run it for a month and read the receipt. If the free rung carried real traffic, you've stopped paying for calls that were always free. If it didn't, you've spent nothing to learn that. There's more on the ladder mechanics across the [provider routing](/blog/topics/provider-routing/) hub — but the receipt from your own traffic will be more persuasive than anything I write here.
