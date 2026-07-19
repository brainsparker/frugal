---
title: "Shipping your first AI feature: a checklist for product teams"
slug: ai-feature-launch-checklist
date: 2026-05-14T16:20
description: "An AI feature launch checklist from the operational side: evals before code, cost ceilings, latency budgets, fallback UX, and a kill switch."
excerpt: "The demo is the easy 20%. This is the other 80% — seven checks that decide whether your AI feature survives contact with real users and a real invoice."
cluster: building-with-ai
type: educational
keyword: "AI feature launch checklist"
related:
  - cut-ai-agent-api-costs
  - how-to-choose-a-model-for-agents
  - ai-agents-provider-outages
---
The demo works. That's the trap. An AI feature demos at 90% done and ships at 40% done, because everything that makes it survivable in production — the evals, the budgets, the fallbacks, the receipts — is invisible in a demo. This is the AI feature launch checklist I wish someone had handed me before I started operating my own agents: seven checks, in the order they'll hurt you if you skip them.

None of this is about the model. Model choice matters, but it's the most reversible decision on the list. These are the irreversible ones — the things that are cheap to build before launch and expensive to retrofit after.

## 1. Build the eval set before you write the feature

Before the first line of feature code, collect 50–100 real inputs and write down what a good output looks like for each. Real inputs — pulled from support tickets, search logs, user interviews — not inputs you invented, because invented inputs are secretly optimized to be answerable.

This feels like it delays the project. It's the opposite: it's the only thing that makes every later decision fast. Which model? Run the eval set, look at the deltas — the framework I laid out in [choosing a model for agent work](/blog/how-to-choose-a-model-for-agents/) assumes this set exists. New prompt? Run the set. Vendor swap? Run the set. Without it, every change is a vibes-based argument between whoever tried the feature most recently. Teams that skip this don't skip evals; they run them manually, forever, on whatever example is in the Slack thread.

## 2. Set a cost ceiling per user action

Decide, in dollars, what one user action is allowed to cost. Not per month, not per customer — per click. An AI feature turns a button into a fan-out: one "research this" click can become 40 searches, a dozen page extractions, and several model calls. In one deep-research teardown I keep coming back to, search alone was 54% of task cost. The meter runs whether or not you're watching.

The ceiling forces the right engineering conversations early. If a click is allowed $0.05 and your prototype spends $0.40, you know before launch that you need [the cheap cuts](/blog/cut-ai-agent-api-costs/) — caching, free-first provider routing, retry caps — not after the first invoice arrives with a comma in it. Enforce the ceiling in code as a hard budget per task, because an agent that can't stop spending will not stop spending.

## 3. Set a latency budget — for the whole interaction

Users experience your feature end-to-end, so budget end-to-end: "answer in under 8 seconds, p95," then divide that among the steps. The division is where AI features differ from normal ones. A pipeline of five sequential calls doesn't get five chances to be fast; it accumulates five chances to be slow, and tail latencies compound viciously across a chain.

The budget also disciplines your provider choices. A slow free provider is fine in a background job and unacceptable in an interactive path — same provider, different verdict, and only a written budget tells you which situation you're in. Give every external call an explicit timeout derived from the budget. A call with no timeout is a promise your UI can hang indefinitely.

## 4. Design the fallback UX before you need it

Every dependency in your AI feature will fail — the model API, the search provider, the extraction service. The question is what the user sees when it does. If the answer is a spinner that never resolves, you designed the failure; you just didn't design it on purpose.

Decide the degraded modes now. Provider down: can a cheaper or [fallback provider](/blog/ai-agents-provider-outages/) answer, even at lower quality? Partial results: can you show what you got with a note, instead of nothing? Total failure: what's the honest empty state, and does it give the user a non-AI path to their goal? "Graceful degradation" is a product decision wearing an engineering costume. Make it in a design review, not in an incident channel.

## 5. Log everything from day one

Every AI call should log: input, output, provider used, model, latency in ms, cost in dollars, and a task ID that ties multi-step runs together. A JSONL file is enough — one line per call, grep-able, no platform required. What matters is that day one of launch is also day one of data.

This is the cheapest item on the checklist and the one with the worst retrofit story. Skip it and your first bad week goes like this: the bill spikes, or quality drops, and you have no record of what was called, what it cost, or what came back. You're debugging a spending system with no receipts. Log first; the observability dashboard can come later, since a log can always become a dashboard but a dashboard can't become the logs you didn't keep.

## 6. Wire a kill switch

You need a way to turn the feature off — completely, in seconds, without a deploy. A config flag your on-call can flip that swaps the feature to its non-AI fallback state.

AI features earn this requirement twice over. First, they fail in novel ways: a provider change or a prompt regression can make output embarrassing rather than merely broken, and embarrassing output screenshots well. Second, they fail expensively — a retry storm or a zombie loop burns real money per minute while you debug. The kill switch converts both from "all-hands emergency" to "flip the flag, fix it Monday." Test the switch before launch. An untested kill switch is a decoration.

## 7. Roll out in stages, watching the boring numbers

Internal users, then 5%, then 25%, then everyone — with cost per action, p95 latency, error rate, and fallback rate reviewed at every gate. Not because staged rollouts are novel advice, but because of what AI features do at each stage: they change shape with traffic. Real users write inputs your eval set doesn't contain, hit rate limits your demo never approached, and find the prompt injection your team didn't think of. Each stage should feed its surprises back into the eval set from item 1 — that's the loop that makes the next launch cheaper. The 5% stage is also where your cost ceiling meets reality: multiply observed cost per action by full traffic *before* you're at full traffic.

Note which items you just validated: the gate metrics come from item 5's logs, the gate thresholds from items 2 and 3, and the rollback path from item 6. The checklist isn't seven independent chores. It's one system, and the rollout is its first real test.

## The demo was the easy part

If you're staring down a deadline and can't do all seven: do the eval set, the logging, and the kill switch. Those three are the ones you can't reconstruct after the fact — everything else on the list can be added in week two, painfully but possibly.

The uncomfortable truth about AI features is that the impressive part (the model) is rented, and the durable part (the operational scaffolding) is yours. Teams that internalize that ship features that survive their first outage, their first invoice, and their first viral screenshot. There's more in this vein under [building with AI](/blog/topics/building-with-ai/) — the unglamorous 80% is the product.
