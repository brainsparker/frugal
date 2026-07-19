---
title: "What is an LLM router — and do you actually need one?"
slug: what-is-an-llm-router
date: 2026-05-12T13:45
description: "What an LLM router does, how model routing differs from tool-call routing, what the components are, and when a router is overkill."
excerpt: "The term gets used for two different things. Here's what a router actually contains — policy, health, fallback, accounting — and the honest cases where you should skip one."
cluster: provider-routing
type: educational
keyword: "LLM router"
related:
  - provider-routing-for-ai-agents
  - how-to-choose-a-model-for-agents
  - cut-ai-agent-api-costs
---
An LLM router is a piece of software that sits between your application and multiple model providers and decides, per request, which one gets the call. That's the whole definition. Everything else — the dashboards, the semantic caching, the "intelligent orchestration" — is packaging around a decision function: given this request, which provider, and what do we do if it fails?

The term is having a moment, which means it's being stretched to cover things it shouldn't. Before you evaluate any router, or build one, it's worth being precise about what's actually in the box.

## Two different things wear the same name

When people say "LLM router" they usually mean **model routing**: choosing which model handles an inference call. Send the easy classification to a small cheap model, send the gnarly synthesis to the frontier one, fail over to a second provider when the first returns a 529. The request is a prompt; the decision is which brain answers it.

There's a second kind of routing that matters just as much for agents and gets a fraction of the attention: **tool-call routing**. An agent's search, extraction, and browse calls also have multiple possible providers at wildly different prices — the same web search costs $0 from Wikipedia or Marginalia and $0.005 from You.com, a spread I broke down in [the rack-rate gap post](/blog/rack-rate-gap-web-search-costs/). The request is a tool call; the decision is which service answers it.

The two are structurally the same problem — walk a ladder of providers, cheapest capable rung first — but they differ in one important way. Model routing has a quality-prediction problem: you have to guess, before calling, whether the small model is capable enough for *this* prompt. Tool routing mostly doesn't. A search either returns hits or it doesn't; an extraction either yields the article body or it doesn't. You can verify the cheap rung's output after the fact and fall through on failure, which makes tool routing the easier problem to get right. It's why I built Frugal around [routing tool calls down a price ladder](/blog/provider-routing-for-ai-agents/) rather than starting with model selection.

This post covers both, because the anatomy is shared.

## What's actually inside an LLM router

Strip the marketing and a router has four components. If a product can't show you all four, it's a proxy with opinions.

**Policy.** The decision function itself: given a request, produce an ordered list of providers to try. Policies range from trivial (static priority list) to elaborate (a classifier that predicts difficulty and picks a model tier). My experience is that the trivial end is underrated. A static ladder ordered by price, with fall-through on failure, captures most of the value of routing with none of the "who routes the router" recursion — using a model call to decide which model to call has a cost too.

**Health signals.** The router needs to know when a rung is degraded: error rates, timeout counts, latency percentiles per provider. Without health signals, your fallback chain only handles the failures a provider admits to (explicit errors) and not the ones it doesn't (30-second hangs). This is the component people skip and then rediscover during their first real outage, when the fallback chain they were sure they had turns out to only cover the polite failures.

**Fallback semantics.** What counts as "this rung failed, try the next one"? Errors and timeouts, obviously. But the interesting case is the *empty success*: a search that returns 200 and zero hits. In Frugal's ladder, a free provider returning zero hits falls through to the next rung, while a paid provider returning zero hits ends the chain — if a provider you paid for found nothing, don't pay a pricier one to confirm it. Whatever your rules, they need to be explicit. Fallback semantics are where routers quietly differ most.

**Accounting.** Every response should carry what it cost and who answered it. Frugal stamps `{"provider_used": ..., "cost_usd": ..., "latency_ms": ...}` on every result and appends it to a local JSONL ledger. Without per-call accounting you can't verify the router is saving you anything — you've added a hop on faith. A router without receipts is a black box that bills you.

## Build or adopt?

The build-versus-adopt math here is friendlier than for most infrastructure, because a minimal router is genuinely small. A static ladder, a timeout per rung, fall-through rules, and a log line per call — that's a few hundred lines in any language, and you should not feel bad about writing it. If your needs stop there, a dependency is overhead.

Adopt when you want the parts that are tedious to build and maintain: provider integrations that track API churn, health tracking with sane defaults, cost tables that stay current, transport plumbing. That last category is why I think routing increasingly belongs at the protocol layer — an MCP server that routes is a router your agent framework doesn't have to know about, which fits the direction the [ecosystem is heading](/blog/mcp-ecosystem-2026/).

One hard rule either way: the router must not become lock-in itself. If your "router" requires its own account, proxies your traffic through its cloud, and holds your keys, you've traded N provider dependencies for one aggregator dependency — with margin on top. Bring your own keys, keep the ledger on your machine, and make sure you can eject.

## When a router is overkill

Honest section, because the answer to the title question is sometimes no.

You don't need an LLM router if you call one model, from one provider, at low volume, in a non-critical path. A router with one rung is a function call with extra steps. You also don't need one if your provider spend is a rounding error — if the whole bill is $40 a month, the engineering hour is worth more than the routing will ever save, and [there are cheaper cuts to make first](/blog/cut-ai-agent-api-costs/).

And you don't need a *product* when you need a *pattern*. A hardcoded try/except with a second provider in the except branch is a two-rung router. It's fine. Plenty of production systems should stop exactly there.

The signals that you've outgrown the hardcoded version: you're calling three or more providers for the same capability; an outage has actually cost you a bad day; your bill crossed the threshold where 30–50% savings pays for the work; or you're spreading routing logic across every call site and fixing bugs in four places. Any one of those, and the decision function deserves to be one piece of software with one set of rules.

Start by writing down your ladder — which providers, in what order, and what makes a call fall through. If that document is one static list, congratulations: your router is twenty lines, and you can spend the saved time reading more about [provider routing](/blog/topics/provider-routing/) for the day your ladder grows a second rung.
