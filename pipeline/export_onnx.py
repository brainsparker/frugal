"""Export the trained classifier to ONNX, quantize to INT8, verify parity,
and benchmark CPU latency.

Consumes:  out/model/  (from train.py)
Produces:  out/onnx/model.onnx            fp32 export
           out/onnx/model.int8.onnx       dynamic INT8 quantized
           out/metrics_onnx.json          parity vs torch + latency stats

Usage:
  python3 export_onnx.py
  python3 export_onnx.py --model-dir out/model --threads 1

Parity gate: INT8 eval accuracy may drop at most 1.0 point vs the torch
model's stored metrics (out/metrics_eval_v1.json). Latency claim to defend:
sub-10ms p50 single-thread CPU on short intents.
"""
import argparse
import json
import os
import statistics
import time

import numpy as np

from taxonomy import label_names
from train import build_metrics, read_jsonl


def softmax(x):
    e = np.exp(x - x.max(axis=-1, keepdims=True))
    return e / e.sum(axis=-1, keepdims=True)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--model-dir", default="out/model")
    ap.add_argument("--eval-file", default="eval/eval_v1.jsonl")
    ap.add_argument("--out-dir", default="out/onnx")
    ap.add_argument("--metrics-out", default="out/metrics_onnx.json")
    ap.add_argument("--torch-metrics", default="out/metrics_eval_v1.json")
    ap.add_argument("--max-length", type=int, default=128)
    ap.add_argument("--threads", type=int, default=1,
                    help="ORT intra-op threads for the benchmark (1 = worst case)")
    ap.add_argument("--bench-n", type=int, default=200)
    ap.add_argument("--parity-budget", type=float, default=1.0,
                    help="max allowed accuracy drop in points vs torch")
    args = ap.parse_args()

    import onnxruntime as ort  # noqa: PLC0415
    from onnxruntime.quantization import QuantType, quantize_dynamic  # noqa: PLC0415
    from optimum.onnxruntime import ORTModelForSequenceClassification  # noqa: PLC0415
    from transformers import AutoTokenizer  # noqa: PLC0415

    os.makedirs(args.out_dir, exist_ok=True)

    # ---- export fp32 ----
    print(f"exporting {args.model_dir} -> ONNX")
    ort_model = ORTModelForSequenceClassification.from_pretrained(
        args.model_dir, export=True)
    ort_model.save_pretrained(args.out_dir)
    fp32_path = os.path.join(args.out_dir, "model.onnx")
    assert os.path.exists(fp32_path), "fp32 export missing"

    # ---- dynamic INT8 quantization ----
    int8_path = os.path.join(args.out_dir, "model.int8.onnx")
    quantize_dynamic(fp32_path, int8_path, weight_type=QuantType.QInt8)
    fp32_mb = os.path.getsize(fp32_path) / 1e6
    int8_mb = os.path.getsize(int8_path) / 1e6
    print(f"fp32 {fp32_mb:.1f} MB -> int8 {int8_mb:.1f} MB")

    tok = AutoTokenizer.from_pretrained(args.model_dir)
    names = label_names()
    label2id = {n: i for i, n in enumerate(names)}
    eval_rows = read_jsonl(args.eval_file)

    so = ort.SessionOptions()
    so.intra_op_num_threads = args.threads
    sess = ort.InferenceSession(int8_path, so, providers=["CPUExecutionProvider"])
    input_names = {i.name for i in sess.get_inputs()}

    def run_one(text):
        enc = tok(text, truncation=True, max_length=args.max_length,
                  return_tensors="np")
        feeds = {k: v.astype(np.int64) for k, v in enc.items()
                 if k in input_names}
        return sess.run(None, feeds)[0][0]

    # ---- INT8 parity eval ----
    probs = np.stack([softmax(run_one(r["text"])) for r in eval_rows])
    y_pred = probs.argmax(axis=1).tolist()
    y_true = [label2id[r["label"]] for r in eval_rows]
    metrics = build_metrics(y_true, y_pred, probs, eval_rows, names)

    parity = {"int8_accuracy": metrics["accuracy"]}
    if os.path.exists(args.torch_metrics):
        with open(args.torch_metrics) as f:
            torch_acc = json.load(f)["accuracy"]
        drop = round((torch_acc - metrics["accuracy"]) * 100, 2)
        parity.update({"torch_accuracy": torch_acc, "drop_points": drop,
                       "within_budget": drop <= args.parity_budget})
        print(f"parity: torch {torch_acc:.4f} -> int8 {metrics['accuracy']:.4f} "
              f"(drop {drop} pts, budget {args.parity_budget})")
        if drop > args.parity_budget:
            print("WARNING: INT8 parity budget exceeded. Ship fp32 or try "
                  "static quantization with calibration data.")
    else:
        print("no torch metrics found; skipping parity comparison")

    # ---- latency benchmark (single text at a time, the real routing path) ----
    bench = [r["text"] for r in eval_rows[:args.bench_n]]
    for text in bench[:10]:
        run_one(text)  # warmup
    times = []
    for text in bench:
        t0 = time.perf_counter()
        run_one(text)
        times.append((time.perf_counter() - t0) * 1000)
    times.sort()
    latency = {
        "n": len(times),
        "threads": args.threads,
        "p50_ms": round(statistics.median(times), 2),
        "p95_ms": round(times[int(len(times) * 0.95) - 1], 2),
        "mean_ms": round(statistics.mean(times), 2),
        "max_ms": round(times[-1], 2),
    }
    print(f"latency (int8, {args.threads} thread): p50 {latency['p50_ms']}ms "
          f"p95 {latency['p95_ms']}ms")

    out = {"model_sizes_mb": {"fp32": round(fp32_mb, 1), "int8": round(int8_mb, 1)},
           "parity": parity, "latency": latency, "int8_eval_metrics": metrics}
    with open(args.metrics_out, "w") as f:
        json.dump(out, f, indent=2)
    print(f"metrics -> {args.metrics_out}")


if __name__ == "__main__":
    main()
