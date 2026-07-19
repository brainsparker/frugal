---
title: "Price ladders: a mental model for every API dependency"
slug: price-ladders-api-dependencies
date: 2026-09-03T14:10
description: "API cost optimization starts with a price ladder: a free floor, priced rungs, and fall-through rules. How to build one for any dependency."
excerpt: "The free-cheap-premium ladder isn't a search trick. Extraction, transcription, embeddings, and code execution all have the same shape — here's how to build the ladder."
cluster: provider-routing
type: educational
keyword: "API cost optimization"
related:
  - provider-routing-for-ai-agents
  - rack-rate-gap-web-search-costs
  - free-first-default-provider
---
Search taught me the pattern, but the pattern was never about search. Every API dependency an agent has — every capability you rent by the call — comes in three grades: a free floor, a cheap middle, and a premium top. Once you see one dependency that way, you can't unsee the rest, and API cost optimization stops being a quarterly vendor negotiation and becomes a data structure: a ladder, walked cheapest-first, with rules for when to climb.

I've spent enough posts on the search version — [the same query prices at $0 or $0.005](/blog/rack-rate-gap-web-search-costs/) depending on who answers. This post is about generalizing it. The ladder is a mental model for any dependency, and building one takes about an afternoon.

## The shape repeats everywhere

Here are the ladders I either run today or have priced out for capabilities on my planned list. Frugal routes search, extraction, and browse now; the last three rows are market rates I've collected while scoping what comes next.

| Dependency | Free floor | Middle rung | Premium rung |
|---|---|---|---|
| Web search | Wikipedia, Marginalia, self-hosted SearXNG ($0) | Serper ($0.001/call) | You.com ($0.005/call) |
| Page extraction | go-readability, local ($0) | Firecrawl (~$0.001/page) | headless browse, ~$0.002/30s via Browserless |
| Transcription | whisper.cpp, local ($0) | Deepgram (~$0.0043/min) | Whisper API ($0.006/min) |
| Embeddings | local nomic or bge ($0) | text-embedding-3-small ($0.02/1M tok) | Voyage, Cohere |
| Code execution | local Docker ($0) | E2B (~$0.10/hr) | Modal (~$0.14/hr) |

Five different capabilities, five different unit types — per call, per page, per minute, per million tokens, per hour — and the same three-grade structure in every row. That's not a coincidence. It's what a market looks like when compute is cheap, open source is good, and a managed SLA is the thing actually being sold at the top. The free floor exists because someone open-sourced the core capability. The premium rung exists because someone wrapped it in reliability and coverage. The middle rung arbitrages between them.

Most teams buy one row of this table at the premium rung and never look at the other two columns. The ladder model says: buy the whole column, per call.

## Building a ladder for any dependency

Three steps. None of them require new infrastructure — they require looking things up and writing down rules.

### Find the free floor

There is almost always one: a local library, a public API, a self-hostable service. go-readability extracts articles locally. whisper.cpp transcribes on your own CPU. A local embedding model handles the bulk of retrieval workloads. The floor is real, but you have to be honest about its edges — I say this constantly about my own free rungs. Marginalia is genuinely good for essays and documentation and genuinely weak on news and product pages. Wikipedia covers entities and stops there. Self-hosted SearXNG works, but it is not a Google-grade SERP. A ladder built on a dishonest floor collapses in production; a ladder built on an honestly-scoped floor absorbs a surprising share of traffic at $0. I've made the fuller argument for [free-first defaults](/blog/free-first-default-provider/) separately — the short version is that the floor should be the default precisely because its failure mode is falling through, not failing silently.

### Price the rungs

Write down the actual rates, in the provider's native unit, side by side. This step feels trivial and almost nobody does it, because the rates live in five different pricing pages and the invoice arrives pre-aggregated. The moment the rates sit in one table, the gaps get loud. Deepgram to Whisper API is a 40% jump per minute. Serper to You.com is 5×. E2B to Modal is 40%. Those spreads are the budget for your routing logic: every call the cheaper rung handles correctly is the spread, banked.

### Define the fall-through rules

This is the step that turns a rate table into a ladder. A ladder needs an explicit answer to: what counts as this rung failing, and what happens next?

For search, my rules are asymmetric and worth stealing. A free provider returning zero hits falls through to the next rung — the miss cost nothing, so trying again is free upside. A paid provider returning zero hits ends the chain — the query has no hits, and paying a pricier provider to confirm that is burning money on a second opinion. Errors and timeouts fall through everywhere, which is the reliability half of the story and the reason [fallback chains](/blog/provider-fallback-chains/) exist at all.

Every dependency needs its own version of "zero hits." For extraction it might be "fewer than N characters of body text." For transcription, a confidence score below threshold. For code execution, a sandbox that fails to start. The rule doesn't have to be perfect; it has to be written down, because an unwritten fall-through rule means every engineer implements a different one in every call site.

## The ladder is procurement policy, compiled

Here's the reframe that made this click for me. In a normal company, procurement is an annual event: someone evaluates vendors, negotiates a contract, and the whole org inherits one answer for a year. The ladder is the same activity — evaluate options, set the order of preference, define acceptance criteria — but encoded in code and executed per call, thousands of times a day.

That changes what "switching vendors" means. With contract-time procurement, switching is a migration project: rewrite the integration, re-test, coordinate the cutover. With a ladder, switching is reordering rungs — or adding one and watching the ledger to see what share of traffic it wins. The vendor relationship becomes revocable at the granularity of a single call, which is exactly the granularity at which quality and price actually vary.

It also changes who enforces the policy. A procurement policy in a PDF gets violated by every engineer who hardcodes their favorite API. A ladder in the router can't be violated, because the router is the only code path that talks to providers. The receipt on every result — provider used, cost incurred — is the audit trail, generated for free.

## Where to start

Pick your second-biggest API dependency — the biggest one probably has politics attached. Spend an hour finding its free floor and pricing its rungs into a table like the one above. Then write the fall-through rule as a single sentence and put it somewhere the code can enforce it. You now have a ladder for one dependency, and the next one goes faster because the shape is always the same: floor, rungs, rules.

The generalization from one ladder to a routing habit is the subject of most of what I write — there's more on [provider routing](/blog/topics/provider-routing/) if you want the search-specific machinery. But the model stands on its own. Every dependency is a column of three prices, and the question is never "which one do we pick." It's "in what order, and when do we climb."
