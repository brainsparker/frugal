---
title: "7 ways to cut your AI agent's API bill this week"
slug: cut-ai-agent-api-costs
date: 2026-05-07T15:15
description: "Seven ways to reduce AI API costs this week: measure cost per result, route free-first, cache, cap retries, and kill zombie loops."
excerpt: "No replatforming, no procurement cycle. Seven changes you can ship by Friday that attack the parts of an agent bill nobody is looking at."
cluster: ai-costs
type: listicle
keyword: "reduce AI API costs"
related:
  - rack-rate-gap-web-search-costs
  - provider-routing-for-ai-agents
  - how-to-choose-a-model-for-agents
---
Most agent bills are unexamined. Not mismanaged — unexamined. Nobody decided to spend $0.005 on a search that Wikipedia would have answered for free; the agent just called whatever was wired in. Which means you can reduce AI API costs substantially without touching your product, your model, or your architecture. Here are seven things you can do this week. None of them requires a migration.

## 1. Measure cost per result before you change anything

Do this one first, because it decides whether the other six are worth your time. Not cost per token, not cost per call — cost per *result your user actually got*. A research task that fans out into 40 searches, 12 extractions, and 3 model calls has one number that matters: what did the answer cost?

Start crude. Log every external call to a JSONL file with the price you'd pay at rack rate, then sum per task. That's the whole system. In one deep-research teardown I keep coming back to, search alone was 54% of task cost — not the model, the *searches*. If you'd guessed the model was the expensive part, you'd have optimized the wrong half of the bill. Measure, then cut.

## 2. Route free-first

The same web search costs $0 to $0.005 per call depending on who answers it. Wikipedia and Marginalia are free. A self-hosted SearXNG is free. Serper is $0.001. You.com is $0.005. That's [a 5× gap between the two paid rungs alone](/blog/rack-rate-gap-web-search-costs/), and an infinite ratio between free and paid.

So order your providers as a ladder, cheapest first, and only fall through when a rung returns nothing. The free rungs have real limits — Wikipedia covers entities, Marginalia is strong on essays and the indie web and weak on news — but "real limits" is not "useless." Every query a free rung answers is a paid call you didn't make. I wrote up the full mechanics in [the provider routing guide](/blog/provider-routing-for-ai-agents/); the one-week version is: put a $0 provider in front of your paid one and fall through on zero hits.

## 3. Cache repeated lookups

Agents re-fetch the same things constantly. The same documentation page, the same company homepage, the same definitional query — sometimes within a single task, reliably across tasks. Every one of those repeats is a paid call that returns bytes you already have.

A content-addressed cache keyed on normalized URL (for extraction) or normalized query (for search) with a TTL of even one hour will absorb a surprising share of traffic. Extraction is the easy win: a page you pulled through Firecrawl at ~$0.001 ten minutes ago has not changed. Serve it from disk. Cache hits cost $0 and return in single-digit milliseconds, so this cut also makes your agent faster. Few cost optimizations do that.

## 4. Dedupe near-identical queries

Caching catches exact repeats. Deduplication catches the sloppier failure: an agent that searches "acme corp pricing", then "acme corporation pricing", then "pricing for acme corp" — three paid calls for one intent. Models paraphrase; billing meters don't care.

Normalize before you look up: lowercase, strip punctuation, sort terms, then check the cache. You can go further with fuzzy matching, but plain normalization already collapses most of the paraphrase tax. If you're paying $0.005 a call, three-for-one queries mean you're paying triple rack rate for that lookup and nobody notices, because each individual call looks legitimate in the logs.

## 5. Cap retries — with a budget, not a vibe

Retries are where bills go to multiply. A provider starts timing out, your loop retries three times per call, and your cost for that provider quadruples during the exact window when it's delivering the least value. Retry storms during [provider outages](/blog/ai-agents-provider-outages/) are how a bad afternoon becomes a bad invoice.

Two rules. First, a hard retry cap per call — one retry, maybe two, never unbounded. Second, retries spend from the same budget as the original call: if a task has a 20-call ceiling, a retry is one of the 20. And don't retry a paid provider that returned zero hits. Zero hits is an answer. Paying a pricier provider to confirm that the query has no results is the single most avoidable line item I see.

## 6. Right-size the model per call

Not every call in an agent loop needs your best model. Query rewriting, relevance filtering, format extraction, yes/no classification — these are small-model jobs riding on frontier-model pricing because picking one model for everything was easy on day one.

Split your calls into "reasoning" and "plumbing." Route the plumbing to a model a tier or two down and eval the difference — usually it's negligible, occasionally it's zero. The plumbing calls are typically the *majority* of calls in a loop, so this cut compounds. If you want a framework for the tiering decision, I covered it in [how to choose a model for agent work](/blog/how-to-choose-a-model-for-agents/). Same ladder logic as tool routing: cheap first, escalate when the cheap rung demonstrably fails.

## 7. Kill zombie loops

Every agent operator eventually finds one: a task that failed in a way the loop didn't recognize, still dutifully searching, extracting, retrying — burning paid calls toward a goal it can no longer reach. Zombie loops are pure waste with no quality tradeoff at all, which makes them the most satisfying thing on this list to kill.

Three tripwires, in order of effort. A wall-clock timeout per task. A call-count ceiling per task. And a progress check: if the last N tool calls produced no new information — same queries, same zero-hit results, same pages — halt and surface a partial answer. An agent that has stopped making progress and keeps spending isn't being thorough. It's being billed.

## Where to start

Do item 1 today; it's an afternoon of logging and it tells you which of the other six pays best for your traffic. For most search-heavy agents, the ranking is: free-first routing, then caching, then retry caps — those three attack the biggest line item directly. Items 4 through 7 are smaller individually but they're also cheaper to ship, and they stack.

None of this is exotic. It's the same discipline you'd apply to any metered dependency, applied to a system that happens to spend money autonomously. That's really the theme of everything I write about [AI tool costs](/blog/topics/ai-costs/): agents spend like interns with a corporate card, and the fix is not a cheaper card. It's a receipt.
