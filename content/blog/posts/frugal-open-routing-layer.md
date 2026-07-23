---
title: "Frugal is the open routing layer for AI tools"
slug: frugal-open-routing-layer
date: 2026-07-23T16:00
description: "Frugal grew from a cost router into a routing layer: policies, intent routing with frugal__execute, and a roadmap for Frugal Cloud."
excerpt: "Cost was the wedge. Routing is the product. What's shipping, what changed, and what Frugal Cloud will add."
cluster: provider-routing
type: strategic
keyword: "AI tool routing layer"
related:
  - provider-routing-for-ai-agents
  - what-is-an-llm-router
  - inside-frugals-router
draft: false
---

Frugal started with one observation: the same web search costs $0 on
Wikipedia or Marginalia, $0.001 on Serper, $0.005 on You.com — and agents
pick providers by what got wired in, not by price. So we built a router
that walks every tool call down the price ladder and prints the cost on
the result. That observation hasn't changed. It's still true, and cost is
still the policy most Frugal calls run under.

But cost is a *policy*, not an identity. The moment routing exists, the
questions stop being only about price: route by *my* observed latency,
not yours. Never send this workload to a paid API. Always try our
compliance-approved provider first. Use the premium provider for the
requests that matter. Those are all the same mechanism — an ordered
provider chain with failover — under a different rule.

So that's what Frugal is now: **the open routing layer for AI tools.**
Describe the job; Frugal decides how to complete it, and shows its work.

## What ships today

**Routing policies.** One optional YAML block, per capability:

```yaml
routing:
  search:
    strategy: fast            # cheap (default) | fast | premium
    deny: [youcom]            # never called — not even when pinned
  extract:
    order: [firecrawl, goreadability]
```

`cheap` is the default you already know — effective cost ascending,
quota-aware, automatic failover. `fast` orders by your machine's own
observed latency, from the local usage ledger (successful calls only,
cost order until enough history exists — it's your data, not a live
probe). `premium` prefers your premium-priced providers. `order` is an
explicit preference prefix; unlisted providers still serve as fallback.
`deny` means never — it's enforced even on pinned calls, which makes it
the honest privacy knob: deny every paid provider and nothing leaves
your free and local rungs.

**Intent routing.** The new `frugal__execute` tool takes a job
description instead of a capability choice. Classification is
deterministic — URL and keyword cues, no model call — and the response
says exactly what was decided. Captured from a live zero-key run:

```
frugal__execute {"intent": "search for MCP server security best practices"}

  stderr › search zero hits; falling back  provider=marginalia latency_ms=273
  result › {
    "capability": "search",
    "provider_used": "wikipedia",
    "cost_usd": 0,
    "latency_ms": 650,
    "reason": "routed to a web search; policy=cheap: effective cost
               ascending; provider=wikipedia won on attempt 2"
  }
```

The `reason` field is the point. A router you can't audit is a black
box with a bill; every Frugal response carries the provider, the cost,
and — through `execute` — the why.

A `priority` argument (`cheap | balanced | premium`) maps onto the
policies above per call, and a URL intent runs an extract that falls
forward to a headless render when the page turns out to need JS, costs
summed. The direct tools — `frugal__search`, `frugal__extract`,
`frugal__browse` — are still there for agents that already know the
capability.

## What didn't change

Everything that made Frugal trustable is intact. One Go binary. Your
keys, your machine, no account, no data retention by architecture. The
local ledger and the `frugal stats` receipt — cost is the flagship
policy, and the receipt is its proof. And the license: source-available
under BUSL 1.1, converting to Apache 2.0 four years after each release.
We're deliberately not calling that "open source" — it isn't, by the
OSI definition — but you can read every line, self-host everything, and
never be locked in.

## What's next: Frugal Cloud

The routing layer runs on your machine and always will. What teams have
asked for is the layer *above* it: edit policies in a dashboard and
deploy them to every app, share templates across a workspace, see usage
and spend org-wide, watch provider health, search routing traces.

That's Frugal Cloud, and it is **roadmap, not product** — nothing there
ships today. If you want it when it exists:
[join the waitlist](mailto:sparker@hey.com?subject=Frugal%20Cloud%20waitlist).

## Five minutes

```
curl -fsSL https://frugal.sh/install | bash
frugal mcp install
```

Restart your agent, and the next tool call it makes is an intelligently
routed one — with the decision printed on the result.
