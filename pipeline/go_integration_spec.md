# Spec: `internal/classify/` — local model classification in frugal

Status: draft for review · Taxonomy: v1 · Model: frugal-router-v1 (ModernBERT-base, ONNX INT8, ~150 MB)

## Goals and non-goals

**Goals**
- Upgrade `frugal__execute` intent classification from keyword heuristics to the local model, opt-in.
- Preserve frugal's invariants: single Go binary, cross-compiles cleanly, no cgo, works offline, keys/data never leave the machine, every routing decision auditable.
- The model can only improve routing, never degrade it: keywords remain the default engine and the fallback path.

**Non-goals (v1)**
- GPU execution, batch classification, multilingual, per-tool argument extraction, replacing the deterministic URL shortcut.

## Decision 1: ONNX runtime binding — purego, not cgo

Use the **purego** approach (`onnxruntime-purego`, or the `mackross/gonnx` pattern): the ONNX Runtime C API is bound at runtime via `dlopen`/`purego`, with **no cgo at build time**.

Rationale:
- `yalue/onnxruntime_go` (the mainstream binding) requires `CGO_ENABLED=1`, which breaks frugal's trivial cross-compilation matrix and static-binary story.
- Pure-Go ONNX interpreters are 10-50x too slow for a 150M encoder; sub-50ms CPU latency is not achievable without ORT. (Research: no cgo-free, no-external-lib path exists at this latency; purego + ORT shared lib is the pragmatic middle.)
- Static-linking ORT is not supported by upstream (no official `.a`).

The ORT shared library is treated like the model: a **downloaded artifact, not a build dependency** (CPU-only builds, ~8 MB linux-x64 / ~7 MB linux-arm64 / ~30 MB osx-arm64 / ~72 MB win-x64 compressed, public GitHub release assets, no auth).

## Decision 2: tokenizer — pure Go (sugarme), gated by a parity test

Use `sugarme/tokenizer` loading the model's `tokenizer.json` (ModernBERT BPE). The high-fidelity alternative (`daulet/tokenizers`) is a Rust FFI + cgo dependency, which is disqualified by Decision 1's rationale.

**Hard gate before ship:** a golden parity test tokenizes all 175 eval_v1 texts plus 1,000 sampled training rows with sugarme and compares token ids byte-for-byte against the Python tokenizer's output (checked into the repo as a fixture). Any mismatch blocks the feature. Fallback if sugarme fails ModernBERT's config: `malusama/tokenizer`, then reconsider.

## Package layout

```
internal/classify/
  classify.go      // Classifier interface + Chain
  keywords.go      // existing heuristic, extracted from current execute path
  onnx.go          // purego ORT session, build-tag free
  tokenizer.go     // sugarme wrapper + fixture parity test
  download.go      // artifact fetch/verify for model + ORT lib
  testdata/        // golden classifications, tokenizer fixtures
```

```go
type Result struct {
    Label      string  // taxonomy v1 label
    Confidence float64 // softmax max; 1.0 for keyword engine
    Engine     string  // "modernbert-v1" | "keywords"
    Reason     string  // e.g. "fallback=low_confidence(0.61<0.70)"
}

type Classifier interface {
    Classify(ctx context.Context, intent string) (Result, error)
    Ready() bool
}
```

`Chain` wraps `[onnx, keywords]`: URL shortcut fires before anything (unchanged); then the model if `Ready()` and confidence ≥ threshold; otherwise keywords. Keywords can never be removed from the chain.

## Config

`~/.frugal/config/models.yaml`:

```yaml
routing:
  classifier:
    engine: keywords          # default; "modernbert-v1" opts in
    confidence_threshold: 0.70
    model_dir: ~/.frugal/models   # override for air-gapped installs
```

CLI sugar: `frugal config set routing.classifier modernbert-v1` and `frugal classifier status` (shows engine, model version, download state, last-24h fallback rate from the local ledger).

## Download flow

Trigger: first `frugal__execute` after opt-in (or eagerly via `frugal classifier pull`).

1. Resolve manifest **pinned in the binary** per frugal release: artifact URLs + sha256 for (a) `model.int8.onnx`, `tokenizer.json`, `config.json` from the HF repo at a pinned revision, and (b) the ORT CPU shared library for `GOOS/GOARCH` from the pinned ORT release.
2. Download to `~/.frugal/models/frugal-router-v1/` with `.partial` files, resume support, and sha256 verification before rename. ORT archive is unpacked to `lib/`.
3. **Never block a tool call.** Downloads run in a background goroutine; until `Ready()`, the chain serves keywords with `Reason: "fallback=model_downloading"`. Failure → warn once per session, retry with backoff, keywords continue.
4. Corrupt file on load (sha mismatch, ORT load error) → delete artifact, re-download once, then disable engine for the session with a single logged warning.

Total first-download cost: ~160 MB (model 150 + ORT ~10 on linux). Documented next to the opt-in flag.

## Runtime behavior

- One ORT session, created lazily on `Ready()`, `intra_op_num_threads: 1` (measured p50 17.4 ms on a weak sandbox vCPU; desktop CPUs land well under that), warmup inference at load so the first real call isn't an outlier.
- Truncate input at 128 tokens (matches training).
- Softmax over 11 logits; `id2label` read from `config.json`, **never hardcoded** (taxonomy version comes from the artifact).
- Audit trail: every response's existing `reason` field gains `classifier=modernbert-v1 label=search.news conf=0.94`, or the fallback reason. Ledger rows gain `classifier_engine`, `classifier_confidence` so `frugal stats` can report fallback rate (the drift signal; alarm documented at >15%).

## Failure modes and responses

| Failure | Response |
|---|---|
| ORT lib fails to `dlopen` (missing glibc symbol, quarantine on macOS) | Disable engine for session, warn once, keywords |
| Model file corrupt | Re-download once, then disable for session |
| Inference error mid-session | Log once, keywords for the remainder |
| Tokenizer produces unknown token pattern | Classify anyway (BPE has byte fallback); no special case |
| Air-gapped machine | `frugal classifier pull --from <dir>` sideload; sha256 still enforced |

## Testing

1. **Tokenizer parity** (hard gate, above).
2. **Golden classifications:** eval_v1's 175 rows through the Go ONNX path must match the Python `export_onnx.py` INT8 predictions exactly (same model file, same logits argmax). Fixture generated by the pipeline, committed.
3. **Latency benchmark:** `go test -bench` asserting p95 < 50ms single-thread on CI hardware; publishes the number for the README.
4. **Chain semantics:** unit tests for threshold fallback, not-ready fallback, URL shortcut precedence.
5. **Download:** httptest server with truncated/corrupted artifacts; resume and re-download paths.

## Rollout

1. **v0.1.0-router (opt-in):** feature ships behind `engine: modernbert-v1`, README section, launch post.
2. **Observe:** fallback rates and disagreement anecdotes from opt-in users (local `frugal stats` only; nothing phones home).
3. **Default-on candidate:** only after the eval-v2 refresh and at least one taxonomy-stable release cycle; keywords remain the sub-threshold fallback forever.

## Open questions for Brian

1. Ship the ORT lib download from Microsoft's GitHub release URLs directly, or mirror the blobs under frugal.sh/artifacts for stability and one fewer third-party dependency at install time?
2. macOS notarization: the dlopen'd ORT dylib may trip Gatekeeper quarantine when downloaded by a non-notarized binary; needs a quick empirical test on a stock Mac.
3. Is `classifier status` worth shipping in v1, or keep the surface minimal (config flag only)?
