---
title: "Timeouts are a product decision"
slug: timeouts-product-decision
date: 2026-07-30T16:05
description: "API timeout best practices start with a reframe: a timeout is how long a user waits for a maybe. Per-rung budgets beat one global number."
excerpt: "Somewhere in your config is a number deciding how long users stare at a spinner for an answer that may never come. Nobody senior chose it."
cluster: reliability
type: strategic
keyword: "API timeout best practices"
related:
  - latency-budgets-agent-tool-calls
  - ai-agents-provider-outages
  - agent-tool-chain-failure-modes
---
A timeout is how long a user waits for a maybe. That's the whole reframe. Not a network parameter, not a resilience setting — a product decision about the worst experience you're willing to ship. And in most agent stacks, that decision was made by whoever wrote the HTTP client's default, which means nobody made it.

Most writing about API timeout best practices treats the number as an engineering hygiene item: set one, make it "reasonable," move on. I want to argue the opposite framing. The timeout is the single knob that shapes your entire fallback chain's worst case, it should differ per rung and per workload, and choosing it belongs in the same conversation as choosing what the loading state says. Engineering sets the mechanism. The number is product.

## The default nobody chose

Open your agent's config and find the timeout. There's a decent chance it's 30 seconds, because that's a common client default, or 120, because someone quadrupled it once during an incident and never revisited. Whatever it is, ask who decided that a user should wait exactly that long for a response that may never arrive. Usually the answer is a library author who has never seen your product.

Here's what that default actually encodes. It says: when a provider is having its worst day, we will hold the user hostage for N seconds before admitting anything is wrong. Providers do have worst days — I've written about [what outages do to agents](/blog/ai-agents-provider-outages/) — and the ugly failure mode is rarely a fast error. Fast errors are a gift; your chain catches them and falls through in milliseconds. The expensive failure is the hang: a provider that accepts the connection and then trickles or stalls. Against a hang, your timeout isn't one defense among several. It's the only defense.

## One global number is always wrong

A single timeout for every call is wrong in both directions at once. Set it low enough to keep interactive requests snappy and you'll strangle the legitimately slow operations — a browser rendering a heavy page through Browserless genuinely needs more time than a Wikipedia lookup. Set it high enough for the slow operations and every fast path inherits a pathological worst case.

The resolution is per-rung timeouts, and the reasoning falls out of data you already have. Every result that flows through Frugal carries `latency_ms` — my captured Wikipedia run answered in 954ms, Marginalia returned its zero hits in 529ms. A few thousand ledger lines give you a latency distribution per provider, and the timeout question becomes concrete: set each rung's budget a bit past its own tail, not at some global ceiling. A rung that answers in under two seconds at the 99th percentile and hasn't answered by four isn't going to — waiting past that point buys you nothing but user resentment.

Notice what per-rung budgets do to the chain arithmetic. A three-rung fallback chain's worst case is the *sum* of its timeouts plus overhead. Three rungs at a global 30 seconds is a 90-second worst case — an eternity you configured by accident. Three rungs at 2s, 3s, and 5s is a 10-second worst case you chose on purpose. Same chain, same providers, same code. The timeout knob is doing all the work. This is the core of the [latency budget](/blog/latency-budgets-agent-tool-calls/) discipline: the budget belongs to the request, and every rung gets a slice, not a fresh allowance.

## Batch and interactive are different products

The same tool call deserves different timeouts depending on who's waiting.

Interactive means a human is watching a spinner. The budget comes from attention spans, not infrastructure: a chat interface has maybe 5–10 seconds of credibility for a tool round-trip before the user decides the product is broken, and every rung in the chain has to fit inside that together. Tight per-rung timeouts, few rungs, and a strong bias toward answering worse-but-now.

Batch means nobody is watching. An overnight research run or a queue of extraction jobs can afford 30 seconds per rung and a five-rung chain, because the marginal cost of patience is zero and the marginal value of a recovered result is high. Here the risk inverts — it's not user frustration, it's a stuck job pinning a worker forever, so you still cap everything; you just cap it generously.

Most stacks run one timeout config across both. That's the same number being too small and too large simultaneously, which is a strong sign the number was never really designed. If your router can't express "this call is interactive, this one is batch," that's the missing feature — more foundational than any individual value you'd tune.

## Giving up gracefully is a feature

The reason timeouts feel like a dial to crank upward is that timing out feels like failure. Reframe it: a timeout that fires is your product *choosing* to respond — with a degraded answer, a partial result, or an honest "couldn't reach the search index, here's what I know without it" — instead of letting a third party's bad day dictate your UX indefinitely.

That only works if firing the timeout leads somewhere designed. A chain that times out on the last rung and surfaces a raw exception has graceful degradation nowhere in it. The [failure-mode catalog](/blog/agent-tool-chain-failure-modes/) I wrote up earlier has a theme running through it: chains fail badly when nobody scripted the exits. The timeout is the entrance to the exit. What the user sees in the two seconds after it fires is worth more design attention than the number itself.

And hanging, for the record, is strictly worse than every alternative. A wrong-ish answer can be corrected. An honest failure can be retried. A spinner that runs for 90 seconds teaches the user one permanent lesson: don't trust this thing with anything urgent.

## Put the number in the product review

The practical takeaway is organizational, not technical. Timeout values should be visible artifacts — in the config, per rung, per workload class, with the worst-case sum computed and written down — and the worst-case number should be reviewed by whoever owns the user experience. "Our search feature's worst case is 10 seconds, then it degrades to cached results" is a product spec. "Timeout: 30" in a YAML file nobody has opened since March is a liability with a spinner attached.

Where to start: pull your latency logs, compute per-provider tails, and set each rung's timeout just past its own p99. Sum the chain and ask whether you'd defend that worst case in a product review. If not, cut rungs or cut budgets until you would. Then go design the screen the user sees when the whole chain gives up — because on some Tuesday, it will, and that screen is part of your product too. More on this general discipline lives on the [reliability hub](/blog/topics/reliability/).
