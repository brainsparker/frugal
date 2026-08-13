# Frugal Router: Synthetic Data Pipeline

Generates the training set for frugal.sh's local intent-classification model
(taxonomy v1: 11 classes). Implements the four stages from the design spec:
seed harvesting, grid-based LLM generation, hard negatives, and QC
(dedup, cross-model verification, human spot-check, decontamination).

## Requirements

- Python 3.9+
- `pip install scikit-learn requests`
- Optional: `pip install datasets` (Stage 1 HF harvest),
  `pip install sentence-transformers` (embedding-based dedup instead of TF-IDF)

## Quick start: offline demo (no API keys)

Verifies the whole pipeline end to end with a deterministic offline generator:

```bash
python3 run_pipeline.py --demo
```

Artifacts land in `out/`: stage outputs, `deduped.jsonl`, `verified.jsonl`,
`spotcheck.csv`, and after your manual review, `train.jsonl` / `val.jsonl`.

## Real run

You need three model families (two generators + one verifier) on any
OpenAI-compatible endpoints (OpenRouter, Together, OpenAI, local Ollama):

```bash
python3 run_pipeline.py --per-label 1500 \
  --family-a "https://openrouter.ai/api/v1|$KEY_A|<model-a>" \
  --family-b "https://api.together.xyz/v1|$KEY_B|<model-b>" \
  --verifier "https://api.openai.com/v1|$KEY_C|<model-c>"
```

Targets ~3,000 raw examples per label (1,500 per family) before QC losses of
roughly 15-25%, landing near the spec's 33k total.

### The human gate

The pipeline stops after writing `out/spotcheck.csv` (500 stratified rows).
Review it by hand (about half a day). If any class shows >5% label error,
regenerate that class before training. Then:

```bash
python3 stage4_qc.py finalize out/decontaminated.jsonl
```

## Eval set discipline

Put the frozen eval set at `eval/eval_v1.jsonl` (`{"text", "label"}` rows).
The pipeline decontaminates training data against it (cosine >= 0.85 dropped).
**Never train before the eval set exists** or the decontamination guarantee is
meaningless. The eval set is hand-curated and never produced by this pipeline.

## Training and ONNX export

After `finalize` has produced `out/train.jsonl` / `out/val.jsonl`:

```bash
pip install -r requirements-train.txt
python3 train.py            # fine-tune ModernBERT-base, eval against eval_v1
python3 export_onnx.py      # ONNX export, INT8 quantize, parity check, latency bench
```

`train.py` writes the best checkpoint to `out/model/` and
`out/metrics_eval_v1.json` with per-class P/R/F1, per-boundary accuracy
(the five confusion pairs), top confusions, and fallback/decided-accuracy
rates at thresholds 0.5-0.9 (this is the data for choosing the production
confidence threshold).

`export_onnx.py` writes `out/onnx/model.int8.onnx` plus
`out/metrics_onnx.json`. Gates: INT8 accuracy drop <= 1.0 point vs torch,
and single-thread CPU p50 is the latency number to publish (target: sub-10ms).

CPU smoke test without a GPU: `python3 train.py --max-train 2000 --epochs 1`.

## Files

| File | Role |
|---|---|
| `taxonomy.py` | 11 labels, definitions, confusion pairs, generation grid axes |
| `seeds/manual_seeds.jsonl` | 59 hand-written register-anchoring seeds |
| `stage1_seeds.py` | Seed loading + optional HF tool-calling dataset harvest |
| `stage2_generate.py` | Persona x domain x style grid generation with definition-echo self-check |
| `stage3_hardnegatives.py` | Contrastive minimal-edit pairs per confusion boundary + adversarial singles |
| `stage4_qc.py` | dedup / verify / spotcheck / decontaminate / finalize subcommands |
| `run_pipeline.py` | Orchestrator with a hard stop at the human gate |
| `llm.py` | OpenAI-compatible client + offline demo backend |

## Design notes

- Rows carry `gen_model`, `persona`, `domain`, `style`, `source`, `taxonomy`
  so any slice can be audited or ablated later.
- Stage 2 requires the generator to echo the label definition back; batches
  that fail the echo are discarded (cheap label-noise filter at the source).
- Verification errors (network) keep the row rather than silently dropping it;
  disagreements drop it (default) or flag it (`--keep-flagged`).
- Dedup is greedy first-wins over exact matches then blocked cosine
  similarity, memory-safe to ~100k rows.
