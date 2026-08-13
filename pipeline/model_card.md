---
language:
- en
license: apache-2.0
base_model: answerdotai/ModernBERT-base
pipeline_tag: text-classification
tags:
- intent-classification
- routing
- mcp
- agents
- tool-routing
- onnx
library_name: transformers
model-index:
- name: frugal-router-v1
  results:
  - task:
      type: text-classification
      name: Intent classification (tool routing)
    dataset:
      name: frugal-router-eval-v1
      type: frugal-router-eval-v1
    metrics:
    - type: accuracy
      value: {{ACCURACY}}
    - type: f1
      name: Macro F1
      value: {{MACRO_F1}}
---

# frugal-router-v1

A 149M-parameter intent classifier that routes AI agent tool calls locally, in single-digit milliseconds, on CPU. Built for [frugal.sh](https://frugal.sh), the open routing layer for AI tools, and usable by any MCP-style agent stack that needs to decide *what kind of tool* a request needs before deciding *which provider* gets it.

**The pitch:** most routing layers call an LLM to decide where to send your request, adding latency and cost to every single tool call. frugal-router-v1 is a fine-tuned encoder, not a generative model. One forward pass, one label, one confidence score. No tokens generated, no API called, nothing leaves your machine.

- **Base model:** [answerdotai/ModernBERT-base](https://huggingface.co/answerdotai/ModernBERT-base) (149M params)
- **Task:** single-label classification of agent intent strings onto 11 routing capabilities
- **Deployment target:** ONNX INT8, single-thread CPU ({{INT8_SIZE_MB}} MB)
- **Latency:** p50 {{P50_MS}} ms / p95 {{P95_MS}} ms per classification (single thread, {{BENCH_CPU}})
- **Taxonomy version:** v1 (11 classes)

## What it classifies

The model maps the short `intent` string an agent passes to a routing tool (register: terse, imperative, agent-authored) onto one of 11 semantic capabilities. Labels are intents, never tool or provider names, so provider churn does not invalidate the model.

| Label | Meaning | Routing consequence in frugal |
|---|---|---|
| `search.general` | Open web lookup, no freshness constraint | Cheapest general provider |
| `search.news` | Freshness-sensitive: stale = wrong | Freshness-capable provider |
| `search.deep` | Semantic/conceptual research, find-similar, papers | Neural search provider, worth paying up |
| `extract.url` | Static fetch of a known/implied URL | Cheap fetch/parse chain |
| `browse.dynamic` | Needs a real browser: JS, clicks, login, screenshots | Browser provider (the expensive one) |
| `embed` | Vector embeddings | Embedding provider |
| `transcribe` | Speech-to-text | Transcription provider |
| `code.exec` | Run code in a sandbox | Code execution provider |
| `generate` | Text generation over provided content | Routed chat model |
| `multi_step` | Bundles two or more capabilities | Decompose before routing |
| `out_of_scope` | Not a routable capability | Clean no-route instead of a forced guess |

## Evaluation

Evaluated against **frugal-router-eval-v1**: 175 hand-written examples, frozen and versioned, never used in training (training data is decontaminated against it at cosine similarity 0.85). The eval set includes 60 boundary-tagged examples, 12 for each of the five confusion pairs that cost users real money when routers get them wrong.

### Headline numbers

| Metric | Value |
|---|---|
| Accuracy | {{ACCURACY}} |
| Macro F1 | {{MACRO_F1}} |
| Worst-class F1 | {{WORST_CLASS_F1}} ({{WORST_CLASS_NAME}}) |
| INT8 accuracy drop vs fp32 | {{PARITY_DROP}} points |

### Per-boundary accuracy

The five confusion pairs the model is specifically trained and measured against:

| Boundary | Accuracy (n=12) |
|---|---|
| `search.general` vs `search.news` | {{B1}} |
| `search.deep` vs `search.general` | {{B2}} |
| `extract.url` vs `browse.dynamic` | {{B3}} |
| `multi_step` vs `search.general` | {{B4}} |
| `generate` vs `search.deep` | {{B5}} |

### Baselines

| System | Accuracy | p50 latency |
|---|---|---|
| frugal keyword heuristics | {{KW_ACC}} | ~0 ms |
| fasttext baseline | {{FT_ACC}} | <1 ms |
| Zero-shot LLM classifier (API) | {{LLM_ACC}} | {{LLM_MS}} ms |
| **frugal-router-v1 (INT8, CPU)** | **{{ACCURACY}}** | **{{P50_MS}} ms** |

### Confidence and fallback

The intended deployment keeps deterministic keyword heuristics as the fallback path. Below a confidence threshold, the router falls back rather than guessing, so a stale or uncertain model degrades to current behavior, never below it.

| Threshold | Fallback rate | Accuracy on decided subset |
|---|---|---|
| 0.7 | {{FB_07}} | {{DA_07}} |
| 0.8 | {{FB_08}} | {{DA_08}} |
| 0.9 | {{FB_09}} | {{DA_09}} |

## Usage

### Transformers

```python
from transformers import pipeline

router = pipeline("text-classification", model="{{HF_REPO}}")
router("click through to the changelog and screenshot it")
# [{'label': 'browse.dynamic', 'score': 0.98}]
```

### ONNX Runtime (the deployment path)

```python
import numpy as np
import onnxruntime as ort
from transformers import AutoTokenizer

tok = AutoTokenizer.from_pretrained("{{HF_REPO}}")
sess = ort.InferenceSession("model.int8.onnx", providers=["CPUExecutionProvider"])

enc = tok("latest x402 foundation news", return_tensors="np")
logits = sess.run(None, {k: v.astype(np.int64) for k, v in enc.items()})[0][0]
probs = np.exp(logits - logits.max()); probs /= probs.sum()
# argmax -> label id; id2label lives in config.json
```

### In frugal

```bash
frugal config set routing.classifier modernbert-v1   # opt-in; auto-downloads on first use
```

Keyword heuristics remain the default and the below-threshold fallback. Every routed response carries the audit trail: `classifier=modernbert-v1 label=search.news conf=0.94`.

## Training

- **Data:** ~{{TRAIN_N}} synthetic examples generated by a two-model-family grid pipeline (persona x domain x phrasing style), plus contrastive hard negatives targeting the five confusion boundaries and adversarial cases (freshness words used non-temporally, conjunction traps, implied URLs). Every example blind-verified by a third model family; semantic dedup; 500-row human spot-check; decontaminated against the eval set. Pipeline code: [github.com/brainsparker/frugal (pipeline/)](https://github.com/brainsparker/frugal).
- **Procedure:** full fine-tune, {{EPOCHS}} epochs, lr {{LR}}, max length 128, best checkpoint by validation loss.
- **Quantization:** dynamic INT8 via ONNX Runtime; parity gate of at most 1.0 accuracy point vs fp32.

## Limitations

- **English-first.** The training grid is English; multilingual coverage is a v2 question.
- **Fixed taxonomy.** The model classifies onto taxonomy v1's 11 capabilities. New capabilities require retraining (the data pipeline makes this roughly a one-day operation).
- **Phrasing-only judgment.** The model sees the intent string, not the target page. The `extract.url` vs `browse.dynamic` boundary is genuinely ambiguous from phrasing alone in some cases; frugal handles this with empty-result failover from extract to browse, not with the classifier.
- **Register-tuned.** Trained on terse, agent-authored intent strings. Long conversational end-user messages are out of distribution; classify the distilled intent, not the transcript.
- **Synthetic training data.** Real-world phrasing drift is monitored via fallback-rate telemetry (opt-in) and quarterly probe-set refreshes.

## Versioning and retraining

Taxonomy is versioned separately from weights (taxonomy-v1, model frugal-router-v1.x). Retraining triggers: taxonomy changes, eval accuracy drop >2 points on a refreshed probe set, or fallback rate above ~15% in opt-in telemetry, with a quarterly floor.

## License

Model weights and the eval set are Apache 2.0. The frugal binary itself is BUSL 1.1 (converts to Apache 2.0 four years after each release); using this model does not require the frugal binary.

## Citation

```bibtex
@software{frugal_router_v1,
  author = {Sparker, Brian},
  title = {frugal-router-v1: local intent classification for AI tool routing},
  year = {2026},
  url = {https://huggingface.co/{{HF_REPO}}}
}
```
