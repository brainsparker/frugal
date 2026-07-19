---
title: "Model selection is a routing problem"
slug: model-selection-is-routing
date: 2026-09-08T15:40
description: "Model routing reframes 'which model should we standardize on' as a per-call policy. Why call-time selection beats contract-time committees."
excerpt: "'Which model do we standardize on' is procurement thinking applied to a per-call decision. Escalation rules, premium criteria, and eval gates are one framework wearing three hats."
cluster: model-selection
type: analysis
keyword: "model routing"
related:
  - small-models-first
  - when-premium-models-are-worth-it
  - evaluate-models-own-traffic
---
"Which model should we standardize on?" is the wrong question wearing a reasonable suit. It sounds like engineering diligence. It's actually procurement thinking — the assumption that a capability bought by the call should be chosen by the year, by a committee, for everyone. Model routing is the alternative: the model is selected at call time, by policy, based on what the call needs. One is a contract. The other is a function.

I've spent two years watching this exact mistake play out with search APIs, where teams pick one provider in week one and bill every trivial lookup at the premium rack rate forever. The model version of the mistake is bigger, because model spend is usually the largest line on the bill and because the quality spread between calls is wider. Some calls in your workload need frontier reasoning. Most need a competent small model. A standardization decision prices them identically. A routing decision doesn't.

## Standardization answers a call-time question at contract time

Look at what "standardize on model X" actually claims: that one point on the quality-cost-latency surface is right for every call your product will make this year. That claim is almost never examined, because it's not stated that way. It's stated as "we evaluated the top models and X won our benchmark."

But your workload isn't a benchmark; it's a distribution. Classification calls, extraction calls, short rewrites, long multi-step reasoning, judgment calls with money attached. The spread in difficulty across that distribution is enormous, and the price spread between the smallest viable model and the frontier is routinely an order of magnitude or more. Standardization collapses the whole distribution to a single point — and to be safe, committees collapse it upward. Nobody gets blamed for picking the expensive model. The result is the model-spend version of a pattern I've written about elsewhere: every easy call subsidizing a contract sized for the hard ones.

The difficulty varies per call. Therefore the decision belongs per call. Everything else in this essay is elaboration.

## You already believe the pieces

Here's what convinced me this framing is right: the individually-accepted best practices for model selection are all fragments of one routing policy. Three of them, and I've written about each.

**Escalation.** [Start with small models](/blog/small-models-first/) and escalate when the small model fails or signals low confidence. That's a fallback chain — walk the ladder cheapest-first, climb on failure. Nobody calls it routing, but it's routing: the rung order is the policy, the escalation trigger is the fall-through rule.

**Premium criteria.** [Premium models are worth it](/blog/when-premium-models-are-worth-it/) for specific, nameable call types — when the cost of a wrong answer dwarfs the cost of the call, when the task genuinely needs long-horizon reasoning. Notice the shape of that sentence: it's a predicate over calls, not a verdict over vendors. "Use the premium model when condition C holds" is a routing rule. It cannot be expressed as a standardization decision at all.

**Eval gates.** [Evaluate models on your own traffic](/blog/evaluate-models-own-traffic/), not on public benchmarks, and let those evals decide what's allowed to serve which call class. That's the admission-control half of routing: a rung gets added to the ladder for a query class only after it passes the gate on real traffic from that class.

Escalation says climb on failure. Premium criteria say some calls start high. Eval gates say measure before you reorder. Separately, each reads as a tip. Together, they're a complete model routing policy: per call class, an ordered list of models, entry criteria for each rung, and evidence requirements for changing the order. The committee question — "which model?" — dissolves, because the honest answer was always "it depends on the call," and a router is just "it depends" made executable.

## What the committee is actually protecting

Standardization persists for reasons that deserve answers rather than mockery, so here are the three I hear most.

**"We need consistency."** You need consistency of *outcome*, which is what eval gates enforce — every rung serving a call class has passed the same bar on the same traffic. Consistency of *vendor* is a proxy, and a bad one: model versions change under you within a single vendor anyway.

**"We can't evaluate everything."** You can't evaluate everything at contract time, which is exactly the argument against deciding everything at contract time. A router plus a ledger evaluates continuously as a side effect of operating: every call records which model served it, what it cost, and — with eval sampling — whether the output held up. The committee runs a bake-off once. The router runs one forever.

**"Who's accountable when it breaks?"** With standardization, accountability is clear and useless: the committee chose the model, eighteen months ago, based on a benchmark that didn't include your traffic. With routing, accountability attaches to the policy — a readable, versioned artifact that says which calls go where and why. Policies can be reviewed, diffed, and rolled back. Committee decisions can only be relitigated.

## The policy is smaller than you think

The phrase "routing policy" suggests infrastructure. In practice a first policy is embarrassingly short: classify calls into a handful of classes, assign each class an ordered list of two or three models, define what failure means for each class, log every decision. The classifier can start as a switch statement on call site. I route tool calls this way in Frugal — the same walk-the-ladder, stamp-the-receipt loop — and the mechanics transfer almost unchanged; the differences between routing tool calls and routing model calls are real but narrower than they look, and I've [compared the two directly](/blog/routing-tool-calls-vs-model-calls/).

What you get for that modest effort is optionality with receipts. New model ships? It's a candidate rung — run it through the gate, add it to one class, watch the ledger. Vendor raises prices? Demote, don't migrate. The org never again faces a big-bang model migration, because no call was ever married to a model in the first place.

## Where to start

Take one call class — your highest-volume, most boring one — and write its ladder: the model it uses today, one cheaper candidate, and the failure rule that would trigger escalation. Gate the cheap candidate on a sample of real traffic. If it passes, reorder. You've now made your first call-time model decision, and the interesting thing is what happens to the committee question afterward: it stops being "which model do we standardize on" and becomes "what does the policy say, and what would change it." That's a better question, and there's more like it on the [model selection](/blog/topics/model-selection/) hub.
