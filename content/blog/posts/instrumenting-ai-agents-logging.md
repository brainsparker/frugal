---
title: "Instrumenting an agent: what to log from day one"
slug: instrumenting-ai-agents-logging
date: 2026-07-14T14:15
description: "AI agent logging that pays off: six fields to capture from the first deploy, why JSONL on disk beats a dashboard, and which question each field answers."
excerpt: "Six fields, one JSONL file, first deploy. The instrumentation playbook that answers every question you'll be asked about your agent in month three."
cluster: building-with-ai
type: problem-solution
keyword: "AI agent logging"
related:
  - agent-cost-observability
  - inside-frugals-router
  - agent-tool-chain-failure-modes
---
Every question you'll be asked about your agent in month three has to be answered with data you either started logging in week one or didn't. That's the uncomfortable math of AI agent logging: the bill dispute, the "why is it slow" thread, the provider comparison — all of them need history, and history doesn't backfill. The instrumentation itself is trivial. A struct and a file append. The only hard part is knowing which fields matter before you feel the pain, so here is the list I'd give anyone deploying an agent this week.

## The six fields

Log one line per tool call, with at least these:

**provider_used** — who actually answered. Not who you called first; who produced the result. If you run any kind of fallback, "search" is not a provider and logging it as one destroys the data.

**cost_usd** — what this call cost at rack rate, stamped at call time. Not reconstructed later from an invoice. Free calls log `0`, explicitly — a zero you wrote down is data, a missing field is a shrug.

**latency_ms** — wall-clock for the call. Cheap to capture, impossible to recover, and the first field you'll grep the day someone says the agent feels slow.

**hits** — how many results came back. This is the field almost everyone skips and the one that detects the most failure modes: it's the only way to tell "provider errored" from "provider answered: nothing," and the only early warning for a provider whose quality is sliding while its status page stays green. I walked through both of those in [the five failure modes piece](/blog/agent-tool-chain-failure-modes/) — neither is visible without a hit count.

**fallback_path** — the rungs the call walked before someone answered. Without it you know Wikipedia answered; with it you know Marginalia missed first, and how often, and whether that rung is earning its position.

**task_id** — the identifier that groups calls into the unit your users and your invoice actually care about. Per-call data answers trivia; per-task data answers "what does a research run cost" and "which task burned 400 calls last night."

Concretely, a line from my own ledger — this is the real captured run I walked through in [the router post](/blog/inside-frugals-router/), as it lands on disk:

```json
{"ts":"2026-07-06T09:12:41Z","task_id":"tsk_4f2a","tool":"search","provider_used":"wikipedia","fallback_path":["marginalia","wikipedia"],"hits":3,"cost_usd":0,"latency_ms":954}
```

One line, six answers. Whoever reads it knows who answered, what it cost, how long it took, how many results came back, which rung missed first, and which task to bill it to.

## JSONL on disk beats a dashboard — for now

The reflex when someone says "observability" is to reach for a SaaS dashboard. At the start, resist it. A JSONL file on local disk is the better tool for roughly your first year, and not because dashboards are bad — because they're premature.

The early questions are ad hoc, and a dashboard answers predefined questions. You don't yet know what you'll need to ask, so what you want is raw lines and `grep`, `jq`, `sort`, a ten-line script. Every question in the next section is one pipe away from a JSONL file; against a dashboard, each one is either a pre-built chart or a feature request.

The rest of the case is boring and decisive. Appending a line to a file adds no latency, can't be down, and doesn't put a vendor in your hot path. The data stays on your machine — tool-call logs contain your users' queries, which is exactly the data to think twice about shipping to a third party. And the format is the least committal decision you can make: JSONL imports into anything later. Frugal itself works this way — a local ledger at `~/.frugal/usage`, one JSON line per call, and a `frugal stats` command that reads the file and prints a monthly receipt. No control plane. The file is the source of truth, and any dashboard you add later is a view of it, not a replacement for it.

When you outgrow the file — multiple hosts, retention policies, a team that wants charts — you'll migrate a well-structured dataset into whatever you choose. Migrating *into* structure is easy. Inventing history isn't.

## The questions you'll be asked, and which field answers each

Instrumentation is only as good as the questions it can answer. These are the ones that actually arrive, usually in this order:

| The question | The fields that answer it |
|---|---|
| "Why was the bill $X this month?" | `cost_usd` summed by `provider_used` |
| "What does one task cost us?" | `cost_usd` grouped by `task_id` |
| "Why does the agent feel slow?" | `latency_ms`, plus `fallback_path` length |
| "Is the free rung actually earning its spot?" | `provider_used` share, `hits` on fall-throughs |
| "Did provider X get worse?" | `hits` per provider, trended over weeks |
| "Which task went runaway last night?" | call count per `task_id` |
| "What would switching providers save?" | `provider_used` × `cost_usd`, repriced |

Read the table backwards and it's the field list again — that's the test I'd apply to any proposed schema. If a field doesn't answer a question on this list, it can wait. If a question on this list has no field, that's the gap that will hurt, and every one of these questions gets asked eventually. The cost rows alone justify the whole exercise; I've written a longer argument for that in [agent cost observability](/blog/agent-cost-observability/), where the summary is that a bill you can't decompose is a bill you can't dispute, cut, or defend.

The provider-quality row deserves a special mention. "Did provider X get worse" is the question nobody plans for, because degradation without errors doesn't feel like a thing until it happens to you. A weekly `jq` one-liner over `hits` per provider is the entire detection system. Two fields, trended. That's it.

## Day one means day one

The playbook, compressed: define the six-field struct, append a JSON line per tool call, ship it with the first deploy. An afternoon of work, most of it deciding field names.

What you get for that afternoon compounds quietly. Week one, it's a curiosity — you'll `wc -l` the file and watch your agent work. Month one, it's a receipt: real cost per task, real provider mix, numbers instead of vibes in the pricing discussion. Month three, it's leverage — the trend lines that catch a degrading provider, the task-level outliers that expose a runaway loop, the repricing analysis that turns a provider negotiation from a guess into an ultimatum.

None of that exists if the logging starts in month three. The agents I trust most aren't the ones with the cleverest loops — they're the ones whose operators can answer questions about them. More on building that kind of operational muscle in the [building with AI](/blog/topics/building-with-ai/) archive. Start the file today; it's the one part of your agent you cannot add retroactively.
