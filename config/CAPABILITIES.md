# Capability scores

The `reasoning`, `coding`, `creative`, and `instruction_following` fields in
`config/models.yaml` drive routing. This document explains how those numbers
are grounded and how to refresh them.

## Sources

| Axis | Source | Notes |
|---|---|---|
| `reasoning` | [LiveBench](https://livebench.ai/) `reasoning` + `mathematics` average | Refreshed monthly. Normalize each provider's raw score into `[0.0, 1.0]` by dividing by LiveBench's reported top-of-leaderboard score at the same cutoff. |
| `coding` | [Aider polyglot benchmark](https://aider.chat/docs/leaderboards/) | Use the `pass_rate` column. Normalize by dividing by the highest pass_rate in the snapshot. |
| `creative` | Aider side-benchmark qualitative tiering | Top-tier flagship models get 0.90 – 0.95; mid-tier 0.70 – 0.85; nano / haiku-class 0.55 – 0.70. Tier boundaries are judgment calls; re-review when a new frontier drops. |
| `instruction_following` | Aider `instruction_adherence` scaled to `[0.0, 1.0]` | Tracks how reliably the model follows structured prompts without going off-script. |

Every `ModelConfig.capabilities` block should include both:

```yaml
source: livebench+aider
as_of: 2026-04-15
```

## Refresh protocol

Run this on a reliable cadence (monthly, or when a new frontier model ships):

1. Pull the latest LiveBench leaderboard JSON for the month.
2. Pull Aider's `leaderboard.yml` for the same cutoff.
3. For each model in `config/models.yaml`:
   - Compute `reasoning` and `coding` from the normalized benchmark scores.
   - Assign the `creative` and `instruction_following` tier by eye, using the tiering rubric above.
4. Update `source:` / `as_of:` for every model touched.
5. Open a PR titled `chore(capabilities): refresh scores (livebench YYYY-MM, aider YYYY-MM)` and include a table of deltas in the description.

`frugal sync` **does not** mutate capability scores. It only refreshes pricing,
context length, tool-use, and JSON-mode flags from `models.dev`. The scores are
the editorial product of this project and should move through code review.

## Why not LMArena Elo?

LMArena produces a single composite preference score per model. Frugal routes
on four axes because "best model for coding" and "best model for creative" are
often different picks; collapsing into one number loses the signal that lets
Frugal drop Opus-tier cost for mini-tier cost on routine tasks.

## Why not MMLU / GPQA?

MMLU and GPQA are saturated at the frontier — modern flagship models all score
within a few points of each other, so the axis loses discrimination power.
LiveBench re-rolls its problems monthly, and Aider measures real edit-loop
coding accuracy rather than multiple-choice recall.

## Changing sources

If you switch to a new benchmark suite, do it for every model in one PR, bump
the `source:` field for each, and document the mapping in this file. Mixed
sources across models make routing decisions hard to justify in review.
