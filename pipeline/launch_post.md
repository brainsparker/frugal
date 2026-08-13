# Show HN: A 149M model that routes AI agent tool calls in {{P50_MS}}ms on CPU

*Suggested HN title: Show HN: Frugal Router, a 149M open-weight model that routes agent tool calls in {{P50_MS}}ms on CPU*

---

Your agent doesn't think about where its tool calls go. It calls "search" and somebody's API bills you. In one Deep Research teardown I did, search alone was 54% of task cost, and the providers behind that call vary in price by 5x for near-identical results.

I build [frugal](https://frugal.sh), a local MCP proxy that sits between an agent and its tool providers and routes each call to the cheapest provider that can handle it. Until now, frugal classified intent with deterministic keyword rules. That's fast and auditable, but keywords have a ceiling. "Get the pricing page" and "click through the pricing calculator" look almost identical to a keyword rule, and sending the first one to a headless browser costs you about 100x more than a plain fetch.

The obvious fix is to ask an LLM to classify each call. That's what most routing layers do, and it's backwards: you're adding a 500ms network round trip and a metered API call to every tool invocation, in a product whose whole job is cutting latency and cost.

So we did the unfashionable thing. Routing is not a generation problem. It's an 11-way classification problem, and classification is what encoders have been good at for years.

## What we built

frugal-router-v1 is a fine-tuned [ModernBERT-base](https://huggingface.co/answerdotai/ModernBERT-base) (149M params) that maps an agent's intent string onto one of 11 routing capabilities: three flavors of search (general, freshness-sensitive, semantic research), URL extraction vs. real browser automation, embeddings, transcription, code execution, generation, multi-step, and out-of-scope.

Numbers on the frozen eval set:

| | Accuracy | p50 latency (1 CPU thread) |
|---|---|---|
| Keyword heuristics (current frugal) | {{KW_ACC}} | ~0ms |
| Zero-shot LLM classifier via API | {{LLM_ACC}} | {{LLM_MS}}ms |
| **frugal-router-v1 (ONNX INT8)** | **{{ACCURACY}}** | **{{P50_MS}}ms** |

The model file is {{INT8_SIZE_MB}}MB quantized. It runs offline, downloads once, and nothing about your agent's traffic leaves your machine. No generative model at this size gets anywhere near this latency on CPU. A 600M decoder takes 100-400ms per classification; an encoder does one forward pass and it's done.

## The part that actually took effort

The model is the easy 20%. Two design decisions matter more:

**1. The taxonomy classifies intent, not tools.** Labels are things like `search.news` and `browse.dynamic`, never provider names. Providers churn constantly; the shape of what agents want doesn't. This is what keeps a downloadable model from going stale every time the tool ecosystem shifts.

**2. The model can only improve routing, never degrade it.** Keyword rules stay as the default and as the fallback below a confidence threshold. If the classifier is unsure, frugal behaves exactly as it does today. Every response still carries the audit trail: `classifier=modernbert-v1 label=search.news conf=0.94`. If you've been burned by ML silently replacing a deterministic system, this is the deployment pattern that would have saved you.

Training data is fully synthetic, and we assumed from day one that naive synthetic data would be garbage. The pipeline generates across a persona x domain x phrasing grid with two different model families (so the encoder can't shortcut-learn one model's style), then a third family blind-relabels every example and disagreements get dropped. Twenty percent of the data is contrastive hard negatives targeting the five boundaries that cost real money, like extract vs. browse. There's a mandatory human review gate before anything trains. The whole pipeline is in the repo.

## The eval is the point

Nobody publishing "routers" in this space publishes evals, so we did: [frugal-router-eval-v1]({{EVAL_URL}}), 175 hand-written examples, frozen, versioned, decontaminated against training data. Sixty of them are boundary-tagged pairs built to be maximally confusing: "current limiting resistor calculator" is general search despite the word "current"; "search for cheap and reliable proxies" is single-step despite the "and". We report per-class F1 and per-boundary accuracy, not just a headline number. If you think the model is bad, you can check, and if you beat it, I want to know how.

## Honest limitations

English-only for now. Fixed taxonomy: new capabilities mean retraining, which the pipeline makes about a one-day job. The model judges phrasing, not the target page, so genuinely ambiguous extract-vs-browse cases are handled by frugal's empty-result failover, not by the classifier pretending to know. And it's trained on terse agent-style intent strings, not conversational transcripts.

## Try it

- Model + eval set (Apache 2.0): {{HF_URL}}
- frugal (local MCP proxy, single Go binary): https://frugal.sh
- Pipeline, training code, eval: https://github.com/brainsparker/frugal

Feedback wanted on the taxonomy especially. If your agent stack routes tool calls some other way, I'd genuinely like to hear what breaks.
