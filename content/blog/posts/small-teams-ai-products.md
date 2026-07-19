---
title: "How small teams ship AI products without a platform team"
slug: small-teams-ai-products
date: 2026-09-15T16:15
description: "Small team AI development without a platform team: boring pieces you can read, free-first defaults, one eval set, one ledger, one router."
excerpt: "You don't need an ML platform group to ship an AI feature. You need readable pieces, defaults that can't surprise you on cost, and four honest weeks."
cluster: building-with-ai
type: educational
keyword: "small team AI development"
related:
  - boring-parts-are-the-product
  - build-vs-buy-agent-infrastructure
  - evals-before-scale
---
You do not need a platform team to ship an AI product. You need four things: pieces boring enough to read, a clear line between what you buy and what you build, defaults that make cost surprises structurally impossible, and about a month of disciplined sequencing. Most advice about small team AI development quietly assumes you'll grow into a big team's architecture. I think the opposite: the constraints of a small team — no one to babysit infrastructure, no budget slack to absorb a surprise bill — are exactly the constraints that produce good agent systems.

I build Frugal alone. One Go binary, no control plane, a JSONL file for a ledger. That's not a hardship story; it's the design. Here's the version of it that generalizes.

## Choose boring pieces you can read

The platform-team instinct is to adopt the sophisticated thing — the orchestration framework, the vector database with a mascot, the observability suite with its own query language. Each one is a bet that someone will be around to understand it in eight months. On a five-person team, that someone is you, at 11pm, during an incident.

So invert the criterion. Choose components by whether you can read them end to end: a single binary over a cluster, a flat file over a hosted store, a switch statement over a plugin system, JSONL over a telemetry pipeline. Readable pieces have a property that demo-driven evaluation never surfaces — when they break, the failure is legible. You can open the file. You can grep the log. You can trace the request without a vendor's support tier. I've argued before that [the boring parts are the product](/blog/boring-parts-are-the-product/); for a small team they're also the headcount you don't have to hire.

The test I use: if this component misbehaved right now, could someone on the team locate the misbehavior in under an hour with the tools already on their laptop? If the honest answer is no, that component is a future platform team, prepaid.

## Buy plumbing, build judgment

The build-vs-buy line for a small team is not "build what's core, buy what's not." It's sharper than that: buy plumbing, build judgment.

Plumbing is everything with a known right answer — HTTP clients, model APIs, transcription, extraction, sandboxed execution. Vendors are good at plumbing, the market prices it by the call, and your version would be worse. Buy it, ideally through interfaces thin enough to swap.

Judgment is everything that encodes what *your* product means by good: the eval set that defines acceptable output, the routing policy that decides which provider or model serves which call, the failure rules that decide when to escalate, retry, or degrade. Judgment is the product. Outsource it and you've outsourced the reason customers pick you; keep it and every plumbing vendor becomes replaceable. I've written a fuller treatment of [build vs. buy for agent infrastructure](/blog/build-vs-buy-agent-infrastructure/) — the small-team corollary is that judgment components are small. An eval set is a file of examples. A routing policy is a page of rules. You can afford to own them precisely because they're tiny; they're just not tiny in consequence.

## Free-first defaults, so cost can't surprise you

A big company absorbs a surprise $4,000 API bill as a line-item annoyance. A small team absorbs it as a bad week and a lost option. The fix isn't vigilance — vigilance doesn't scale to thousands of autonomous calls a day. The fix is structural: make the default rung of every dependency free, and make paid rungs opt-in escalations with explicit triggers.

Free-first means the identical search tries Wikipedia or Marginalia before anything metered; extraction tries a local readability parser before a paid crawler; embeddings try a local model before an API. When the free rung misses, the call falls through and pays — deliberately, once, with the trigger logged. The worst case of a free-first mistake is a fall-through and a few milliseconds. The worst case of a paid-first mistake is an invoice. I've made the full argument for free-first defaults elsewhere; the small-team framing is simpler: a default you never have to monitor is the only kind of cost control a team without a platform group can actually operate.

## One eval set, one ledger, one router

If I had to compress the whole infrastructure question for a small team into a sentence: you need exactly three artifacts, and one of each.

**One eval set** — a few dozen real cases with known-good answers, run before any provider, model, or prompt change. Not a benchmark program. A file. This is the gate that lets a two-person team change things confidently on a Tuesday. [Evals come before scale](/blog/evals-before-scale/), and on a small team they come before almost everything.

**One ledger** — an append-only local record of every provider call: tool, provider, cost, latency, run ID. This is your observability suite, your billing reconciliation, and your capacity plan, in a file `jq` can read. I've made the case that [every agent run deserves a receipt](/blog/receipt-for-every-agent-run/); the ledger is where receipts live.

**One router** — the single code path through which every external call flows, walking cheapest-first with written fall-through rules. One router means one place to add a provider, one place to enforce policy, one place that stamps the ledger.

The "one" matters more than the artifact. Two eval sets drift apart. Ledgers per service stop reconciling. Routing logic per call site becomes no routing at all. Small teams win by having a single source of truth for each question — what is good, what did we spend, who answers this call.

## A realistic first month

Sequencing is where small teams actually fail — not by picking wrong pieces but by picking them in the wrong order, usually routing before measurement or shipping before evals. Here's the order that works, one focus per week.

| Week | Focus | Done means |
|---|---|---|
| 1 | Evals | 30–50 real cases with expected outputs, runnable by one command |
| 2 | Instrumentation | Every external call logs provider, cost, latency to one ledger |
| 3 | Routing | Free floor found for each dependency; fall-through rules written; router is the only path out |
| 4 | Ship behind a flag | Real traffic for some users, receipts reviewed daily, kill switch tested |

Week 1 before week 3 because you cannot reorder rungs safely without a gate that says quality held. Week 2 before week 3 because routing decisions without a ledger are guesses with extra steps. And week 4 behind a flag because the first week of real traffic is itself an eval — the flag is what makes its lessons cheap.

At the end of the month you have an AI feature in production, a file that defines correct, a file that records spend, and one code path that enforces policy. No platform. Three artifacts and a habit.

## Where to start

Start with week 1, this week, even if the rest is months away — the eval set improves every later decision and costs an afternoon. And when the feature ships and someone eventually asks whether you need to hire a platform team, you'll have the ledger open in a terminal and a fairly short answer. More on shipping with a small crew lives on the [building with AI](/blog/topics/building-with-ai/) hub.
