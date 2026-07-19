---
title: "Local models are underrated infrastructure"
slug: local-models-underrated
date: 2026-07-31T14:55
description: "Local LLM inference is the $0 rung of the model ladder. Which workloads genuinely fit on your own hardware today — and which don't yet."
excerpt: "Everyone prices the model ladder from cheap-API up. There's a rung below that: weights on hardware you already own, at a marginal cost of zero."
cluster: model-selection
type: strategic
keyword: "local LLM inference"
related:
  - small-models-first
  - free-first-default-provider
  - evaluate-models-own-traffic
---
Everyone prices the model ladder from the cheapest API upward. There's a rung below the cheapest API: weights running on hardware you already own, at a marginal cost of zero per token. Local LLM inference is the $0 rung of the chat ladder, and the industry treats it as a hobbyist curiosity while quietly proving out the identical argument everywhere else in the stack.

I say identical deliberately. My entire case for [free-first defaults](/blog/free-first-default-provider/) in tool routing rests on one observation: a large fraction of requests don't need the premium answer, so the premium provider should be the fallback, not the default. Nobody seriously disputes this for web search anymore. The same logic applied to model calls gets treated as fringe. It shouldn't be.

## The symmetry with self-hosted search

In Frugal's search ladder, self-hosted SearXNG sits on the free rung: you run the software, you pay the electricity, and every query it answers is a query no paid API bills you for. Nobody calls a SearXNG box a toy. It's understood as infrastructure — a deliberate trade of a little operational effort for a marginal cost of zero.

A local model server is the same object. Ollama or LM Studio on a machine you control, an OpenAI-compatible endpoint on localhost, and every completion it serves is a completion with no meter running. The structural role is identical to SearXNG's: the rung you try first because trying it costs nothing, backed by paid rungs for the queries it can't handle. Frugal doesn't route chat yet — model routing is planned, with local models as the floor of that ladder — but the architecture question is already settled by the search side. If free-first is right for retrieval, the burden of proof is on explaining why it's wrong for inference.

The usual explanation is "local models aren't good enough." That's answering the wrong question. The free rung of the search ladder isn't Google-grade either — Marginalia is weak on news, Wikipedia only covers entities — and it still absorbs a meaningful share of traffic because *a meaningful share of traffic is easy*. The right question is never "is the local model as good as the frontier model." It's "what fraction of my requests does the local model handle indistinguishably." For a lot of workloads, that fraction is embarrassingly high.

## Which workloads fit locally today

Honest triage, as of mid-2026, based on what small open-weight models in the 4–30B range reliably do well.

Good fits: classification and routing decisions (is this query about billing or shipping; is this document relevant); extraction into a schema from text you provide; summarization of moderate-length documents; reformatting, retitling, tagging; draft generation where a human or a stronger model reviews anyway; and high-volume internal pipelines where each individual output is low-stakes. The common thread — the context contains the answer and the model's job is transformation, not knowledge. This is the same territory I mapped in [small models first](/blog/small-models-first/): transformation tasks degrade gently as models shrink, recall-heavy and long-horizon reasoning tasks fall off a cliff.

Poor fits, still: frontier-grade reasoning chains, broad world knowledge without retrieval, long multi-step agent trajectories where small errors compound, and anything where a subtle wrongness costs real money. Route those up the ladder without guilt. The point of a ladder is that the top rungs exist.

Privacy deserves its own line item, because it inverts the usual framing: for some workloads local isn't the budget option, it's the only compliant option. Medical notes, legal drafts, unreleased financials — a model whose weights sit on your disk sends nothing anywhere. That property has no API-side price because no API can sell it.

And there's a compounding trend doing quiet work here: the capability floor keeps rising. The open-weight model you can run on a workstation today would have been a paid-API-only capability two years ago. Every release cycle, tasks migrate across the good-fit line in one direction. A ladder built without a local rung has no way to collect that dividend; it just keeps paying API rates for yesterday's frontier.

## The honest hardware caveats

Now the part local-inference enthusiasm tends to skip. Zero marginal cost is not zero cost.

You need real VRAM. A usefully quick 8B model wants a GPU with 8–12GB; 30B-class models want 24GB or unified-memory Macs; quantization stretches all of this but trades away some quality, and the aggressive quants trade away more than the benchmarks admit on your specific task. Throughput is finite and concurrency is the wall — a single consumer GPU serving one stream feels instant, and the same GPU serving twelve parallel agent requests queues them. Paid APIs made elasticity someone else's problem; locally, it's yours again.

There's ops load: model files to update, a server process to keep alive, thermals and driver versions and the occasional mysterious slowdown. Small, but not nothing, and it lands on whoever owned zero of it before. And electricity is not free — for a workstation doing occasional inference it rounds to trivia, but a GPU pinned at full draw around the clock shows up on a bill. The honest accounting is that local inference converts a per-token cost into a fixed capacity cost. That conversion is a bargain when utilization is high and a bad deal when a $200/month API bill is being replaced by a $3,000 GPU that idles.

Which is why the decision should be made the same way as every other model decision: measured. Take the [fifty real queries from your logs](/blog/evaluate-models-own-traffic/), run them through a local candidate, and put quality, latency, and cost-per-thousand in one table next to your incumbent API. If the local model passes on 70% of your traffic at $0, the ladder design writes itself: local first, escalate on the classified-hard 30%. If it passes on 20%, you've spent an afternoon learning your workload is top-rung-shaped, which is also worth knowing.

## Infrastructure, not ideology

The mistake on both sides of this argument is treating it as identity. Local-everything maximalism ignores the concurrency wall and the quality cliff. API-everything convention ignores a $0 rung sitting in plain sight. The boring correct position is that local inference is a rung — the bottom one, with specific strengths (marginal cost zero, privacy, no rate limits you didn't choose) and specific limits (capacity, capability ceiling, ops load) — and rungs don't require belief. They require a router and an eval.

Where to start: install Ollama on whatever machine you have, pull a current small open-weight model, and run your own traffic through it before forming an opinion. Not a demo prompt — your traffic. Most people who do this are surprised in one direction or the other, and either surprise beats the default of paying rack rate for requests a machine you already own could have answered. The broader question of matching model tiers to workloads has a whole [model selection](/blog/topics/model-selection/) hub behind it; local is just the rung the industry keeps forgetting to price in.
