# Frugal benchmark — illustrative sample run

> **This is a sample report**, not a live measurement. The numbers below
> reflect the *shape* and *order of magnitude* you should expect from
> `frugal bench`; reproduce with the command at the bottom of this file
> to replace them with your own numbers.
>
> **As of:** 2026-05-05 · **Workload:** `config/workloads/starter.yaml` ·
> **Quality:** `balanced` · **Baseline:** `gpt-4o`

---

# starter (quality=balanced, baseline=gpt-4o)

Problems: 20 · savings **92.1%** · quality Δ -5.0pp (frugal − baseline)

## Headline
| Strategy | Quality | Cost | Latency p50/p95 | Tool-use accuracy |
|---|---|---|---|---|
| Frugal | 90.0% | $0.0184 | 720ms / 1840ms | 100.0% |
| Baseline | 95.0% | $0.2316 | 1180ms / 2950ms | 100.0% |

## By category
| Category | n | Frugal pass | Baseline pass | Frugal $ | Baseline $ | Frugal latency | Baseline latency |
|---|---|---|---|---|---|---|---|
| factual | 4 | 100.0% | 100.0% | $0.0011 | $0.0142 | 540ms | 870ms |
| hybrid | 4 | 75.0% | 100.0% | $0.0048 | $0.0612 | 980ms | 1410ms |
| reasoning | 12 | 91.7% | 91.7% | $0.0125 | $0.1562 | 760ms | 1240ms |

## Model selection
- `gpt-4o-mini` × 20

## Per-problem results
| # | Problem | Category | Frugal model | Tool ✓ | Judge | Frugal ✓ | Baseline ✓ |
|---|---|---|---|---|---|---|---|
| 1 | `extract-email-01` | hybrid | `gpt-4o-mini` | ✓/✓ | — | ✓ | ✓ |
| 2 | `extract-phone-01` | hybrid | `gpt-4o-mini` | ✓/✓ | — | ✓ | ✓ |
| 3 | `extract-order-01` | hybrid | `gpt-4o-mini` | ✓/✓ | — | ✗ | ✓ |
| 4 | `extract-date-01` | hybrid | `gpt-4o-mini` | ✓/✓ | — | ✓ | ✓ |
| 5 | `math-mul-01` | reasoning | `gpt-4o-mini` | ✓/✓ | — | ✓ | ✓ |
| 6 | `math-div-01` | reasoning | `gpt-4o-mini` | ✓/✓ | — | ✓ | ✓ |
| 7 | `math-percent-01` | reasoning | `gpt-4o-mini` | ✓/✓ | — | ✓ | ✓ |
| 8 | `math-compound-01` | reasoning | `gpt-4o-mini` | ✓/✓ | — | ✓ | ✓ |
| 9 | `classify-sentiment-pos` | reasoning | `gpt-4o-mini` | ✓/✓ | — | ✓ | ✓ |
| 10 | `classify-sentiment-neg` | reasoning | `gpt-4o-mini` | ✓/✓ | — | ✓ | ✓ |
| 11 | `classify-intent-refund` | reasoning | `gpt-4o-mini` | ✓/✓ | — | ✓ | ✓ |
| 12 | `classify-intent-shipping` | reasoning | `gpt-4o-mini` | ✓/✓ | — | ✓ | ✓ |
| 13 | `fact-capital-france` | factual | `gpt-4o-mini` | — | — | ✓ | ✓ |
| 14 | `fact-tallest-mountain` | factual | `gpt-4o-mini` | — | — | ✓ | ✓ |
| 15 | `fact-sql-keyword` | factual | `gpt-4o-mini` | ✓/✓ | — | ✓ | ✓ |
| 16 | `fact-http-status` | factual | `gpt-4o-mini` | ✓/✓ | — | ✓ | ✓ |
| 17 | `explain-quicksort` | reasoning | `gpt-4o-mini` | ✓/✓ | F:0.85 B:0.90 | ✓ | ✓ |
| 18 | `explain-big-o-sort` | reasoning | `gpt-4o-mini` | ✓/✓ | — | ✓ | ✓ |
| 19 | `explain-rest-vs-rpc` | reasoning | `gpt-4o-mini` | ✓/✓ | F:0.70 B:0.82 | ✗ | ✓ |
| 20 | `explain-db-index` | reasoning | `gpt-4o-mini` | ✓/✓ | F:0.78 B:0.88 | ✓ | ✓ |

## Reading this report

- **Quality** — share of problems where the deterministic scorer passed
  (exact / substring / numeric / JSON-key match).
- **Cost** — billed USD across all problems for that leg, computed from
  each provider's reported usage at the per-token rate in
  `config/models.yaml`.
- **Latency p50/p95** — wall-clock per request from the runner.
- **Tool-use accuracy** — share of problems where the leg's tool-call
  decision matched the workload's `tool_use:` expectation. `optional`
  problems pass automatically and render `—` in the per-problem table.
- **Judge** *(optional, requires `--judge-model`)* — LLM-judge score
  from 0 to 1, run alongside the deterministic scorer on problems that
  define a `judge_rubric`. Judge spend is tracked separately from agent
  spend and shown in a footer when present.

## Reproducing this report

```sh
# Set whichever provider keys the workload + baseline need.
export OPENAI_API_KEY=...

# Default run — starter workload, balanced quality, against gpt-4o.
go run ./cmd/frugal bench --out BENCHMARKS.md

# Use-case-first run — Frugal's bundle for factual lookups, with the
# bundle's chat model as the canonical baseline.
go run ./cmd/frugal bench \
  --use-case factual-qa \
  --quality balanced \
  --judge-model claude-opus-4-7-20250918 \
  --stream \
  --out BENCHMARKS.md
```

`make bench-publish` wraps the default invocation. After regenerating,
also update the headline numbers in `benchmark/index.html` so the
landing page tracks the report.
