---
title: "A receipt for every agent run"
slug: receipt-for-every-agent-run
date: 2026-09-10T13:35
description: "AI spend tracking with receipts, not dashboards: a per-run, local, auditable ledger showing what each result cost — and what rack rate would have."
excerpt: "Dashboards tell you what the month cost. A receipt tells you what this run cost, which provider earned it, and what the same result would have cost at rack rate."
cluster: ai-costs
type: strategic
keyword: "AI spend tracking"
related:
  - agent-cost-observability
  - rack-rate-gap-web-search-costs
  - deep-research-cost-teardown
---
Buy a coffee and you get a receipt. Let an agent spend money on your behalf — search calls, extraction calls, model calls, all night, unsupervised — and you get a dashboard, thirty days later, with the spending pre-blended into categories someone else chose. That's backwards. The less supervised the spender, the more itemized the accounting should be. AI spend tracking has standardized on the dashboard, and I think that's the wrong artifact. The right one is a receipt: per-run, local, auditable, and counterfactual.

Frugal is my attempt at building that artifact into the plumbing, so this essay has a product angle — but the argument stands without it. Receipts and dashboards answer different questions, and the question you actually have is the receipt's.

## Four properties a dashboard doesn't have

**Per-run.** A receipt attaches cost to the unit of work — this agent run, this task, this answer. Dashboards attach cost to time and category: "search, September, $412." But nobody's real question is shaped like a category-month. The real questions are "why did last night's research job cost what it cost" and "which step of this pipeline is the expensive one." I've written about [agent cost observability](/blog/agent-cost-observability/) as an engineering problem; the receipt is its unit test. If you can't produce the receipt for one run, your aggregate numbers are a summary of things you can't verify.

**Local.** A receipt should live where the work happened. Frugal's ledger is a JSONL file at `~/.frugal/usage` that never leaves the machine — not a SaaS analytics tier, not a vendor's usage page that shows you your own behavior on their schedule, at their granularity, for their pricing tier. Cost data is operational data. When the ledger is a local file, it composes with everything: `grep`, `jq`, a cron job, your own dashboards if you insist on building one — but downstream of the receipts, not instead of them.

**Auditable.** Each ledger line records what actually happened on one call: the tool, the provider that answered, the cost, the latency. Every number in any report I generate can be traced back to lines in that file. Compare that with the standard dashboard pipeline, where the raw events are sampled, bucketed, and discarded, and the chart is the only surviving artifact. A chart you can't decompose isn't evidence; it's a claim.

**Counterfactual.** This is the property I've never seen a dashboard offer, and it's the one I care about most. A receipt can record not just what the call cost but what the *result* would have cost at rack rate — the price if the premium provider had answered instead. Without the counterfactual, "we saved money" is a feeling. With it, savings are a computable column.

## Dashboards aggregate away the question

None of this makes dashboards useless — it makes them lossy in a specific, predictable direction. Aggregation destroys attribution. Once ten thousand calls become one bar on a chart, you can no longer ask which runs were expensive, which providers earned their fees, or which single retry loop quietly generated 40% of the bar. The dashboard answers "how much"; every actionable question is some form of "which" or "why," and "which" needs line items.

The dynamic gets worse at exactly the moment it matters. When a bill spikes, the dashboard tells you *that* it spiked and roughly *where* — category, day. Then the investigation starts, and the investigation is always an attempt to reconstruct receipts after the fact: pulling logs, correlating timestamps, guessing at attribution. In one deep-research teardown I keep coming back to, search alone was 54% of task cost — the kind of fact that's invisible in a monthly aggregate and obvious in a per-run trace. I walked through that arithmetic in my [deep research cost teardown](/blog/deep-research-cost-teardown/). If the receipts existed from the start, the spike investigation is a query, not a forensics project.

## What the working version looks like

Every response Frugal returns is stamped — provider, cost, latency — and every call appends a line to the local ledger. A run's receipt is just its lines:

```
{"tool":"search","provider_used":"marginalia","cost_usd":0,"latency_ms":529}
{"tool":"search","provider_used":"wikipedia","cost_usd":0,"latency_ms":954}
```

That's a real captured chain: Marginalia came up empty in 529ms, the router fell through, Wikipedia answered with 3 hits at $0. Two lines, full attribution, no reconstruction required.

Then `frugal stats` reads the ledger and prints a monthly receipt, and this is where one accounting rule does a lot of work: **only the call that produced the result earns rack credit.** In the chain above, Wikipedia's line is credited with the counterfactual — the $0.005 the answer would have cost at [the top of the rack-rate ladder](/blog/rack-rate-gap-web-search-costs/). Marginalia's miss earns nothing. It cost nothing, but it also gets no credit for savings, because it didn't produce the result.

That rule is what keeps the counterfactual honest. The tempting version of savings math credits every free call at rack rate — every miss, every retry, every speculative fan-out — and produces a triumphant number that nobody should believe. The strict version credits only delivered results, so the "saved vs. rack rate" figure on the monthly receipt is a claim you could defend line by line to an auditor, or to yourself in a skeptical mood. If you're going to brag about savings, the bragging should be reproducible.

## The strategic point: receipts change behavior, dashboards report it

Here's why I think this is a strategy question and not a tooling preference. A dashboard is positioned as management information — reviewed monthly, discussed quarterly, acted on rarely. A receipt is positioned at the point of work. When every agent response carries `cost_usd`, cost becomes something engineers see in the debugger, in the logs, in the test output. It gets treated like latency: a property of the code you're responsible for, not a finance abstraction that arrives later.

I've watched this change how I build my own agents. A fan-out that looks elegant in the abstract looks different when each branch prints its price. A retry policy reads differently when the retries have line items. Nobody optimizes what they see monthly; everybody optimizes what they see per run.

## Where to start

You don't need my tool for this. Append one JSON line per provider call — tool, provider, cost, latency, run ID — to a local file, and adopt the strict credit rule from day one: results earn credit, attempts don't. Within a month you'll have per-run receipts, an auditable savings figure, and better questions than any chart has ever handed you. If you'd rather have it prebuilt, the ledger and `frugal stats` come with [Frugal](/) out of the box. Either way, more on where agent money actually goes lives on the [AI costs](/blog/topics/ai-costs/) hub — and the next spike investigation on your team should be a `jq` one-liner, not an archaeology dig.
