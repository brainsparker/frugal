---
title: "Build vs buy for agent infrastructure"
slug: build-vs-buy-agent-infrastructure
date: 2026-08-06T13:25
description: "Build vs buy AI infrastructure: keep the logic that differentiates you, adopt boring open plumbing you can read, and apply one decision rule."
excerpt: "Your evals are your product. Your retry loop is not. Splitting the agent stack along that line answers the platform question before a vendor call."
cluster: building-with-ai
type: strategic
keyword: "build vs buy AI infrastructure"
related:
  - byok-agent-tooling
  - evals-before-scale
  - instrumenting-ai-agents-logging
---
Build vs buy is the wrong question until you split the stack. Asked about "AI infrastructure" as one blob, it produces blob answers — a platform contract that swallows your differentiation along with your plumbing, or a build-everything posture that has your best engineer writing retry logic in month three. The build vs buy AI infrastructure decision gets easy once you draw one line through your stack: what makes your product yours, and what is plumbing that happens to touch AI.

I run agents for a living and I've watched teams argue this for weeks. The argument almost always dissolves when the stack gets split, because the two halves want opposite answers.

## Two kinds of code in every agent stack

The first kind is differentiating logic. Your evals — the ones that encode what "good" means for *your* users, on *your* traffic. Your orchestration — the decisions about what your agent attempts, in what order, with what fallbacks in meaning (not in transport). Your product surface: what the user sees, what a failure looks like, what the agent is allowed to promise. If a competitor copied this half, they'd have copied your product.

The second kind is plumbing. Provider routing. Retries and backoff. Timeouts. Rate limiting. Cost ledgers. Caching. Transport. If a competitor copied this half, they'd have copied... good engineering hygiene. Nothing about a well-built fallback chain is yours, any more than your HTTP client is yours.

The line is sharper than it first looks. A useful test: would you put it in a demo to a customer? Your eval results, yes. Your JSONL ledger format, no. Another: does it improve when *you* study your users, or when *anyone* studies the failure modes? Orchestration strategy improves with user knowledge. Retry semantics improve with distributed-systems knowledge that was settled before LLMs existed.

## The differentiating half: build it, because it is the product

Nobody sells your evals. Vendors sell eval *harnesses*, and a harness is fine — but the judgment inside the evals, the labeled examples from your own traffic, the definition of acceptable, cannot be bought, because a vendor doesn't know what your users consider a good answer. I've argued before that [evals are the thing to have before you scale anything](/blog/evals-before-scale/); the corollary here is that they're also the thing you can't outsource.

Same for orchestration at the semantic level. Which sub-tasks to attempt, when to give up, what partial result is worth showing — those choices *are* the product experience. A platform that makes them for you has made your product generic by construction.

Build this half. Staff it. It's where your engineering hours compound.

## The plumbing half wants boring tools you can read

Plumbing has a different physics. It's where the 2 a.m. failures live — the hung connection, the rate-limit storm, the provider outage — and at 2 a.m. the only documentation that matters is the source. Plumbing therefore wants *boring, open tools you can read*: small in scope, stable in interface, auditable in an afternoon.

"Buy" here rarely means a platform. It means adopting something open and dull, the way you adopted your HTTP library. You didn't write it, you can read every line of it, and it will not acquire a pricing page mid-incident. My bias is visible in what I built — Frugal is one Go binary, BYOK, no account, source-available under BUSL that converts to Apache 2.0 — but the bias predates the tool: I want to be able to grep the thing that stands between my agent and the internet. The same logic applies to your [logging and instrumentation layer](/blog/instrumenting-ai-agents-logging/): a local ledger you can `cat` beats a dashboard you can subpoena.

What plumbing should *not* be is bespoke and clever. Hand-rolled routing with undocumented retry semantics is the worst of both worlds — you paid to build it and you still can't trust it.

## What a SaaS platform actually charges

The managed agent-infrastructure platforms are genuinely convenient, and the convenience is real on day one. Price the other side of the trade before signing.

First, margin. A platform in your request path takes a cut of every metered call, forever, and the cut scales with exactly the thing you hope will grow. Per-call economics compound the way [unit economics always do](/blog/ai-product-unit-economics/): a small markup times your fan-out times your growth is not a small number in year two.

Second — and this one gets underpriced — a second data controller. Every query your agent makes, every page it reads, every user intent it acts on now transits someone else's control plane. That's a security-review line item, a compliance dependency, a subpoena surface, and a business risk if the platform pivots, reprices, or gets acquired. I made the longer version of this argument in [the case for BYOK tooling](/blog/byok-agent-tooling/): keys and traffic are strategy, not configuration.

Neither cost means "never buy." They mean the platform must clear a bar: convenience today versus margin plus a data controller for the life of the product. Some platforms clear it, especially for teams with no infrastructure appetite at all. Most clear it only because nobody priced the trade.

## A decision rule you can apply in a meeting

Here it is, compressed. For each component, ask: **whose knowledge makes this better?**

- If it improves when you learn about *your users* — evals, orchestration semantics, product UX — **build it and staff it.** This is the moat.
- If it improves when anyone learns about *failure modes* — routing, retries, ledgers, transport, rate limits — **adopt something boring and open that you can read end to end.** Building it from scratch is a distraction; renting it from a platform adds margin and a data controller to a problem that a readable binary solves.
- If a platform still tempts you, price both hidden costs explicitly — the per-call cut at your projected fan-out, and the second controller in your next security review — and see if the convenience survives the arithmetic.

That's the whole rule. No vendor comes out universally ahead, including mine.

Where to start: list every component in your agent stack in one column, and in the next column write "users" or "failure modes" — whichever kind of knowledge improves it. Twenty minutes. The build vs buy answer for each row is usually sitting right there, and the rows that spark argument are the ones worth an actual design review. More along these lines on the [building with AI](/blog/topics/building-with-ai/) hub.
