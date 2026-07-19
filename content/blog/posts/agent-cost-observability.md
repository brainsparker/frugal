---
title: "Cost observability for agents: put cost_usd on every result"
slug: agent-cost-observability
date: 2026-05-28T16:10
description: "AI cost observability starts with cost_usd stamped on every result, not a monthly invoice. A local JSONL ledger answers per-task questions."
excerpt: "Your invoice knows what you spent by provider and month. It has no idea what one agent run cost. Stamp the cost on the response envelope and the question answers itself."
cluster: ai-costs
type: problem-solution
keyword: "AI cost observability"
related:
  - rack-rate-gap-web-search-costs
  - cut-ai-agent-api-costs
  - provider-fallback-chains
---
Here's a question I like to ask people running agents in production: what did the last run cost? Not the monthly bill. That one run, the one that finished a minute ago. Almost nobody can answer it, and the reason is structural — the cost data lives in a place that can't answer per-run questions. AI cost observability isn't a dashboard problem. It's a "where do you stamp the number" problem, and the right place is the response envelope, at the moment the call returns.

## The invoice is an aggregate; your questions aren't

A monthly invoice tells you what you spent, per provider, thirty days late. Every question you actually need answered is shaped differently: what does the research feature cost per use? Which user's workflows are expensive? Did last Tuesday's prompt change move tool spend? Those are per-task, per-feature, per-day questions, and an aggregate can't be disaggregated after the fact. There's no join key. The invoice says "14,000 calls"; it doesn't say which ones belonged to the feature you're trying to price.

It gets worse with more providers, and agents always have more providers. Search bills per call, extraction bills per page, browsing bills per 30-second unit. Three invoices, three units, three billing lags — and one deep-research teardown I keep coming back to found search alone was 54% of task cost. You cannot reconstruct a per-task number from that pile. I've watched people try. It's archaeology, and the dig always ends at "approximately."

## Stamp cost at the source

There is exactly one moment when the cost of a call is precisely knowable: when the response comes back. The client that made the call knows the provider, the units consumed, and the rate. Multiply, and stamp it on the envelope before anything else touches the result.

This is how Frugal does it. Every tool result carries the receipt inline:

```json
{"provider_used": "wikipedia", "cost_usd": 0, "latency_ms": 954}
```

That line is from a real run — a free rung answered, so the cost is zero, and the zero is stated rather than implied. The stamp costs one multiplication. Reconstructing the same number later costs a data pipeline, an invoice parser, and a guess.

Once cost rides on the envelope, everything downstream inherits it for free. The agent can see what its own call cost. The orchestrator can sum a task. Your logs carry the number without a separate metering system. Nothing has to be joined, because nothing was ever separated.

## A local JSONL ledger is enough

Stamped costs need to land somewhere, and the somewhere is less impressive than you'd think: append one line per call to a local file. Frugal writes to `~/.frugal/usage` — JSONL, one object per call, and it never leaves the machine. No metering service, no analytics vendor, no account. Your spend data is itself sensitive data; a file you can `grep` is both the cheapest and the most private option available.

Per-task attribution falls out of one field. Put a task or run ID on every line as the call happens, and "what did this run cost" becomes a filter and a sum:

```
jq -s 'map(select(.task=="run_8123") | .cost_usd) | add' ~/.frugal/usage/*.jsonl
```

Try getting that number out of an invoice. The ID has to be attached at call time or it's gone — which is the whole argument in one sentence. Attribution is something you record, not something you compute later.

`frugal stats` reads the same ledger and prints a monthly receipt. One accounting rule keeps it honest: only the call that produced the result earns credit. In a [fallback chain](/blog/provider-fallback-chains/), rungs that erred out or returned nothing don't get counted as if they'd answered — the receipt reflects who actually delivered, not who was merely asked.

## The rack-rate counterfactual

Here's the field that surprised me most in practice: alongside what a call did cost, record what it would have cost at list price. When a free rung answers, `cost_usd` is 0 — but the top paid rung would have charged $0.005 for the same search. That $0.005 is the rack rate, and [the gap between rungs](/blog/rack-rate-gap-web-search-costs/) is exactly what routing is supposed to capture.

Without the counterfactual, savings are invisible. A ledger full of zeros looks like nothing happened. With it, the monthly receipt can say: you made 40,000 searches, paid $6.20, and the same traffic at rack rate was $180. That second number is the one that justifies the routing work — and it's also the honest check on it. If actual and rack rate are nearly equal, your ladder isn't routing anything; the expensive rung is answering everything and you should go find out why. Most of the tactics for [cutting agent API costs](/blog/cut-ai-agent-api-costs/) are unverifiable without this one column.

## Why this beats every dashboard you'd build instead

Notice what the whole setup is: a stamp, a file, a field, and a counterfactual column. No control plane. The reason I'm stubborn about this shape is that cost observability tools tend to grow into cost platforms — another vendor, another account, another place your usage data goes. A receipt on every result inverts that. The data is born attributed, stays local, and answers questions at the granularity you'll actually ask them. Dashboards summarize; receipts prove. More arguments in this vein live under [AI costs](/blog/topics/ai-costs/).

Where to start, with or without [Frugal](/): find the code that calls your paid APIs — there's usually one wrapper, or there should be. At the return point, compute `cost_usd` from units and rate, attach the current task ID, and append a JSON line to a file. Maybe forty minutes of work. Then let it run for a week and ask it the question from the top of this post. The first time you answer "what did that run cost" with an actual number instead of a shrug, you'll stop trusting the invoice as your source of truth. It was never telling you what things cost. It was telling you what you owed.
