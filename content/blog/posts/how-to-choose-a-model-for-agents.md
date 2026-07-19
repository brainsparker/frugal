---
title: "How to choose a model for your agent workload"
slug: how-to-choose-a-model-for-agents
date: 2026-04-28T13:55
description: "How to choose an LLM for agent work: five questions — task shape, context, latency, cost per action, escalation — that beat brand loyalty."
excerpt: "Model selection for agents isn't a leaderboard question. It's five workload questions, and most teams skip straight to the logo."
cluster: model-selection
type: educational
keyword: "how to choose an LLM"
related:
  - provider-routing-for-ai-agents
  - rack-rate-gap-web-search-costs
---
Most advice on how to choose an LLM starts with a leaderboard and ends with a brand. That's backwards for agent work. An agent isn't a chat window — it's a loop that plans, calls tools, reads results, and decides what to do next, hundreds of times an hour. The model is a component in that loop, and you should select it the way you'd select any component: by the shape of the work, not the logo on the box.

I route my agents' tool calls down a price ladder, and the longer I run that setup the more obvious it becomes that model choice is the same problem wearing a different hat. Providers get picked by what was easy to wire in, the choice fossilizes, and nobody re-examines it because the costs and failures are aggregated out of sight. Here's the framework I actually use — five questions, in order.

## Start with task shape, not model rank

Leaderboards measure models on tasks that mostly aren't yours. Before comparing anything, write down what the model does *per step* in your loop. Not "research assistant" — the actual atomic operations. Classify a query. Pick a tool and fill its arguments. Summarize twelve search results into three bullets. Decide whether the answer is done.

Then sort those operations into two piles: judgment steps and mechanical steps. Judgment steps — planning a research strategy, resolving contradictory sources, writing the final synthesis — reward reasoning depth. Mechanical steps — extraction, classification, reformatting, argument-filling — reward speed and cost, and a mid-tier or small model usually handles them indistinguishably from a flagship. In every agent I've built, mechanical steps outnumber judgment steps by a wide margin. If you buy a frontier model for the whole loop, you're paying frontier rates for work that's mostly clerical.

## Context needs: measure, don't assume

The second question is how much the model has to hold. Agents are context-hungry in a specific way: tool results pile up. A search step returns snippets, an extract step returns page text, and by turn fifteen the transcript is dominated by material the model needs to *reference*, not reason deeply about.

Two failure modes here. Teams either buy the biggest context window on the market and stuff it — paying per token to re-send an ever-growing transcript on every single step — or they buy short context and watch the agent forget its own findings. The fix for both is the same: measure your real accumulation. Log the token count per step for a week of traffic and look at the distribution. Most workloads need far less than the maximum window *if* you summarize or drop stale tool output as you go, and the discipline of pruning is worth more than the headroom of a giant window. Context is a budget, and like every budget it gets spent to the limit if nobody watches it.

## Latency budget: the loop multiplies everything

A model's latency doesn't matter in isolation. It matters multiplied by your loop depth. A model that responds in a couple of seconds feels fine in a chat box and is unusable in an agent that takes forty sequential steps — that's over a minute of pure model wait before a single tool has run. My tool router stamps `latency_ms` on every result for exactly this reason: you can't budget what you don't measure, and end-to-end task time is the sum of a lot of small numbers nobody looks at individually.

So set the budget top-down. Decide what the whole task is allowed to take — a user-facing agent might get ten seconds, an overnight batch job might get an hour — divide by expected steps, and you have your per-step allowance for model time plus tool time. That number, not vibes, tells you whether a slower-but-smarter model fits. Often the answer is mixed: the slow model fits at the two or three judgment steps and nowhere else.

## Cost per action, not cost per token

Per-token pricing is how models are sold; cost per action is how agents spend. The unit that matters is "what does one complete step of my loop cost" — input tokens (including that accumulated context), output tokens, multiplied by steps per task, multiplied by tasks per day.

Run that arithmetic and two things happen. First, models reorder: a cheap-per-token model that needs three attempts at a structured output can cost more per *action* than a pricier model that nails it once. Second, the model bill lands next to the tool bill, where it belongs. I've written about how the same web search prices anywhere from $0 to $0.005 in [the rack-rate gap](/blog/rack-rate-gap-web-search-costs/), and in one deep-research teardown I keep coming back to, search alone was 54% of task cost. Model selection that ignores the tool side optimizes a minority of the bill. Whatever you choose, stamp the cost on every call — model and tool alike — so the per-action number is a fact in a ledger, not an estimate in a spreadsheet.

## Escalation paths: choose a ladder, not a model

Here's where brand loyalty fully breaks down. The best answer to "which model" is usually "more than one, ordered." Run the cheap model by default. Detect when it's out of its depth — a failed validation, low confidence, a judgment-class step — and escalate that step to the stronger model. This is the same cheapest-first ladder I described in the [provider routing guide](/blog/provider-routing-for-ai-agents/), applied to inference: the premium rung earns its place per call instead of being the default for every call.

Escalation needs a trigger you can implement, not a feeling. The reliable ones are structural: output failed schema validation, tool call errored twice, step is on your hand-labeled judgment list, task flagged high-stakes. Start with those. And define the semantics of "the cheap rung came up empty" deliberately — retry, escalate, or stop — the same way a search chain decides when falling through is worth it.

## The choice is provisional. Design for that.

Whatever you pick will be wrong in six months — a price cut, a new release, a quality regression after a silent update. So the durable decision isn't the model; it's the architecture that makes the model swappable. Keep prompts provider-neutral where you can. Keep the model name in config, not scattered through code. Keep an eval set of your own real tasks — twenty cases beats zero — so "should we switch" is an afternoon's test run instead of a quarter's migration.

Where to start: write down your loop's steps, label each one judgment or mechanical, and price one full task at your current model's rates. That single exercise usually answers the question better than any leaderboard, and it's the foundation for everything else on the [model selection](/blog/topics/model-selection/) hub. Pick per step. The logo can stay out of it.
