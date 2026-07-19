---
title: "The rack-rate gap: why the same web search costs $0 or $0.005"
slug: rack-rate-gap-web-search-costs
date: 2026-04-23T15:40
description: "Web search API pricing spans $0 to $0.005 for the same call. Where the gap comes from, why it persists, and why you should pick per call."
excerpt: "Five providers, one capability, a price range from free to half a cent. An analysis of what you're actually paying for when your agent searches the web."
cluster: ai-costs
type: analysis
keyword: "web search API pricing"
related:
  - provider-routing-for-ai-agents
---
Ask five providers for the same ten blue links and you'll get quotes from $0 to $0.005 per call. Web search API pricing is the strangest rate table in agent infrastructure: an identical capability, delivered over an identical HTTP request, priced across a range where the top end is infinitely more expensive than the bottom and 5× more expensive than the middle.

I keep the current rates taped to the wall, more or less literally:

| Provider | Price per search | What it is |
|---|---|---|
| Wikipedia | $0 | Entity and reference lookups via public API |
| Marginalia | $0 | Independent index; essays, docs, indie web |
| SearXNG (self-hosted) | $0 | Metasearch you run yourself |
| Serper | $0.001 | Google SERP results via API |
| You.com | $0.005 | Full-service search API |

Same input — a query string. Same output — ranked results. A gap of five thousandths of a dollar that compounds into real money the moment an agent starts issuing searches by the thousand. In one deep-research teardown I keep coming back to, search alone was 54% of task cost. At these volumes, the rate table above is not trivia. It's the bill.

So where does the gap come from, and why hasn't it closed?

## What the price actually buys

The range makes sense once you stop reading it as "price of a search" and start reading it as "price of a position in the supply chain."

**Index ownership is the big one.** Building and refreshing a web-scale index is one of the most expensive ongoing operations in software — crawling, storage, ranking, freshness, spam warfare. A provider that owns a full index has to recover that cost on every call. A provider like Serper doesn't own an index; it arbitrages access to one, which is why it can price at $0.001. And the free rungs sidestep the problem entirely: Wikipedia's corpus is community-built, Marginalia's index is deliberately small and curated, and SearXNG shifts the cost to your own server.

**Infrastructure is the part you can't see.** The premium rung isn't just an index — it's the SLA around it. Redundancy, rate capacity, latency guarantees, an on-call rotation. You pay for the calls that would have failed elsewhere. Whether *your* traffic contains those calls is exactly the question most buyers never ask.

**Margin and the buyer complete the picture.** Search APIs are increasingly sold to AI companies, and AI companies have been spending like the money is free. When your customers evaluate you on "did the demo work" rather than dollars per call, price stays where the market will bear it. Nobody's procurement team is benchmarking $0.005 against $0. Mine is, but my procurement team is a JSONL file.

## The gap persists because nobody is standing in it

In a normal market, a 5× spread between two paid vendors for a near-identical good gets arbitraged away. Here it doesn't, for three structural reasons.

First, the buyers can't see their own usage. Search spend arrives as an aggregate invoice line, not a per-call ledger. You can't shop a price you never see itemized. I've written about stamping `cost_usd` on every result as the fix; without something like it, the comparison never even happens.

Second, switching feels expensive even when it's cheap. The provider name is in the code, the response shape is assumed downstream, and the person who wired it in has left the project. So the integration chosen in week one — chosen for docs quality, not price — collects rent indefinitely.

Third, the providers aren't actually selling the same thing, and they know it. Marginalia is honest about being great for essays and long-form writing and weak on news and product pages. Wikipedia covers entities, full stop. A self-hosted SearXNG is real but it is not a Google-grade SERP. The premium rung *is* differentiated — on coverage, freshness, and consistency. The pricing gap survives because for some fraction of queries the differentiation is worth it. The market failure is that everyone pays as if *every* query were in that fraction.

## The wrong conclusion and the right one

The wrong conclusion from the rate table is "use the free one." Free rungs have partial coverage; route everything to Marginalia and your agent goes blind on half the web. The equally wrong conclusion is "pay for the best one everywhere," which is where most teams actually live — every entity lookup, every documentation query, every "what year did X happen" billed at the premium rack rate.

The right conclusion is that a per-provider decision is the wrong altitude. Query difficulty varies call by call, so the price you pay should too. "Which search API should we use" assumes one answer for a workload that contains thousands of different questions. Some of those questions are answerable from a free index in under a second. Some genuinely need the $0.005 treatment. A contract can't tell them apart. A router can.

That's the argument, made properly, in my guide to [provider routing for AI agents](/blog/provider-routing-for-ai-agents/): walk a ladder cheapest-first, fall through when a free rung comes up empty, stop the chain when a paid rung does. The rate table stops being a purchasing decision you agonize over once and becomes a set of rungs you traverse automatically, thousands of times a day.

## What this means for your bill

Run the arithmetic on your own traffic. If a meaningful share of your agent's searches are reference lookups — and for research-style agents, a large share are — those calls price at $0 on the ladder and $0.005 on the default path. The remainder splits between the $0.001 rung and the $0.005 rung based on what the query actually needs. You don't have to guess at the split in advance; you discover it by routing and reading the receipts. My monthly ledger tells me exactly which rung answered what, and what the same month would have cost at full rack rate.

The rack-rate gap isn't a scandal. Providers are pricing their costs and their customers rationally. But it is an opportunity that stays open precisely because it's boring — no one gets promoted for shaving tool-call spend, so the spread sits there, call after call, waiting for someone to pick per call instead of per contract. More on where agent money actually goes lives on the [AI costs](/blog/topics/ai-costs/) hub.

Start with visibility. Count your searches for one week, multiply by your provider's rate, and then multiply by what the ladder would have charged. The gap between those two numbers is the rack-rate gap, localized to your bill. For most search-heavy agents I've seen, it's the largest line item nobody has looked at.
