# Training Runbook: frugal-router-v1

Full fine-tune of ModernBERT-base on the v1 dataset, plus ONNX INT8 export.
Total hands-on time: ~10 minutes. Total wall time: 15-60 minutes depending on hardware.

## 1. Setup (once)

```bash
git clone -b router-pipeline https://github.com/brainsparker/frugal.git
cd frugal/pipeline
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements-train.txt
```

Notes:
- Python 3.9+ required. transformers >= 4.48 required (ModernBERT support).
- NVIDIA GPU: the default pip torch includes CUDA, nothing extra needed.
- Apple Silicon: works out of the box on MPS via the same install.
- No flash-attn needed; ModernBERT falls back automatically.

## 2. Train

```bash
python3 train.py --train data/train.jsonl --val data/val.jsonl
```

Defaults: 4 epochs, lr 5e-5, batch 32, max length 128, seed 7 (keep the seed for reproducibility).

Expected timings for 11,453 rows:
| Hardware | Approx wall time | Memory |
|---|---|---|
| RTX 4090 / A10 / L4 | 8-15 min | ~6 GB VRAM at batch 32 |
| RTX 3060 / T4 | 20-35 min | fits at batch 32; drop to 16 if OOM |
| M2/M3 Mac (MPS) | 30-60 min | 16 GB unified is plenty |

If you rent: any $0.30-0.60/hr GPU box (Lambda, RunPod, Vast) finishes for under a dollar.

Success looks like: a printed metrics block at the end and `out/metrics_eval_v1.json` written.
Reference point: the deliberately crippled CPU smoke test (2k rows, 1 epoch) hit accuracy 0.891 / macro F1 0.906. The full run should beat both. If it doesn't, stop and send the metrics anyway.

## 3. Export + benchmark

```bash
python3 export_onnx.py
```

Runs on CPU, ~5 minutes. Produces `out/onnx/model.int8.onnx` (~150 MB), a parity check, and a single-thread CPU latency benchmark.

Gate: INT8 accuracy drop must be <= 1.0 point vs the torch model. The smoke-test model failed this (2.85 pts) because it was undertrained; the full model is expected to pass. If it still fails, send the metrics anyway and we switch to static quantization.

Bonus if you have time: rerun `python3 export_onnx.py --threads 4` and note the numbers; the launch post can quote both. Latency on YOUR machine is a launch-post data point (the sandbox vCPU measured p50 17.4 ms).

## 4. What to send back

Required (small, attach to the thread):
1. `out/metrics_eval_v1.json`
2. `out/metrics_onnx.json`
3. The machine spec line: CPU model, GPU model, RAM (e.g. from `nvidia-smi` and `lscpu | grep "Model name"`)

The model itself (pick one):
- Preferred: push directly to Hugging Face from the box:
  ```bash
  pip install huggingface_hub
  hf auth login          # paste a WRITE token from hf.co/settings/tokens
  hf upload brainsparker/frugal-router-v1 out/model .
  hf upload brainsparker/frugal-router-v1 out/onnx/model.int8.onnx model.int8.onnx
  ```
- Or: zip `out/model/` + `out/onnx/model.int8.onnx` and share a link; I'll handle the HF upload and model card.

## 5. Troubleshooting

- CUDA OOM: `--batch 16` (or 8). Accuracy is insensitive to this.
- MPS error on Mac: retrain with `PYTORCH_ENABLE_MPS_FALLBACK=1`.
- `eval_strategy` argument error: your transformers is too old; `pip install -U "transformers>=4.48"`.
- Killed on CPU-only box: needs ~4 GB free; use `--batch 4 --max-length 64` (slow but works).
