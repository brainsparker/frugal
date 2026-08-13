"""Fine-tune ModernBERT-base as the frugal router intent classifier.

Consumes:
  out/train.jsonl, out/val.jsonl   (from `stage4_qc.py finalize`)
  eval/eval_v1.jsonl               (frozen eval set; never trained on)

Produces:
  out/model/                       best checkpoint (HF format)
  out/metrics_eval_v1.json         per-class, per-boundary, confusion, fallback rates

Usage:
  pip install -r requirements-train.txt
  python3 train.py                          # defaults
  python3 train.py --base answerdotai/ModernBERT-base --epochs 4 --lr 5e-5

Hardware: single consumer GPU (or Apple Silicon MPS) trains this in well
under an hour; CPU works for smoke tests with --max-train 2000.
"""
import argparse
import json
import os
from collections import Counter, defaultdict

import numpy as np

from taxonomy import LABELS, TAXONOMY_VERSION, label_names


def read_jsonl(path):
    rows = []
    with open(path) as f:
        for line in f:
            line = line.strip()
            if line:
                rows.append(json.loads(line))
    return rows


def build_metrics(y_true, y_pred, probs, eval_rows, names):
    """Per-class P/R/F1, confusion pairs, per-boundary accuracy, fallback rates."""
    n = len(y_true)
    acc = float(np.mean(np.array(y_true) == np.array(y_pred)))

    per_class = {}
    for i, name in enumerate(names):
        tp = sum(1 for t, p in zip(y_true, y_pred) if t == i and p == i)
        fp = sum(1 for t, p in zip(y_true, y_pred) if t != i and p == i)
        fn = sum(1 for t, p in zip(y_true, y_pred) if t == i and p != i)
        prec = tp / (tp + fp) if tp + fp else 0.0
        rec = tp / (tp + fn) if tp + fn else 0.0
        f1 = 2 * prec * rec / (prec + rec) if prec + rec else 0.0
        per_class[name] = {"precision": round(prec, 4), "recall": round(rec, 4),
                           "f1": round(f1, 4), "support": tp + fn}
    macro_f1 = float(np.mean([v["f1"] for v in per_class.values()]))
    worst = min(per_class.items(), key=lambda kv: kv[1]["f1"])

    confusions = Counter()
    for t, p in zip(y_true, y_pred):
        if t != p:
            confusions[f"{names[t]} -> {names[p]}"] += 1

    by_boundary = defaultdict(lambda: [0, 0])  # boundary -> [correct, total]
    for row, t, p in zip(eval_rows, y_true, y_pred):
        b = row.get("boundary")
        if b:
            by_boundary[b][1] += 1
            if t == p:
                by_boundary[b][0] += 1
    boundary_acc = {b: {"accuracy": round(c / tot, 4), "n": tot}
                    for b, (c, tot) in sorted(by_boundary.items())}

    conf = np.max(probs, axis=1)
    fallback = {str(th): round(float(np.mean(conf < th)), 4)
                for th in (0.5, 0.6, 0.7, 0.8, 0.9)}
    # Accuracy on the subset the model would actually decide at each threshold.
    decided_acc = {}
    for th in (0.5, 0.6, 0.7, 0.8, 0.9):
        mask = conf >= th
        if mask.sum():
            decided_acc[str(th)] = round(float(np.mean(
                (np.array(y_true) == np.array(y_pred))[mask])), 4)
    return {
        "taxonomy": TAXONOMY_VERSION,
        "n_eval": n,
        "accuracy": round(acc, 4),
        "macro_f1": round(macro_f1, 4),
        "worst_class": {"label": worst[0], "f1": worst[1]["f1"]},
        "per_class": per_class,
        "per_boundary": boundary_acc,
        "top_confusions": confusions.most_common(10),
        "fallback_rate_at_threshold": fallback,
        "decided_accuracy_at_threshold": decided_acc,
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="answerdotai/ModernBERT-base")
    ap.add_argument("--train", default="out/train.jsonl")
    ap.add_argument("--val", default="out/val.jsonl")
    ap.add_argument("--eval-file", default="eval/eval_v1.jsonl")
    ap.add_argument("--out", default="out/model")
    ap.add_argument("--metrics-out", default="out/metrics_eval_v1.json")
    ap.add_argument("--epochs", type=int, default=4)
    ap.add_argument("--lr", type=float, default=5e-5)
    ap.add_argument("--batch", type=int, default=32)
    ap.add_argument("--max-length", type=int, default=128)
    ap.add_argument("--seed", type=int, default=7)
    ap.add_argument("--max-train", type=int, default=0,
                    help="cap training rows (CPU smoke tests)")
    args = ap.parse_args()

    import torch  # noqa: PLC0415
    from datasets import Dataset  # noqa: PLC0415
    from transformers import (AutoModelForSequenceClassification,  # noqa: PLC0415
                              AutoTokenizer, Trainer, TrainingArguments,
                              set_seed)

    set_seed(args.seed)
    names = label_names()
    label2id = {n: i for i, n in enumerate(names)}
    id2label = {i: n for n, i in label2id.items()}

    def to_ds(rows):
        return Dataset.from_dict({
            "text": [r["text"] for r in rows],
            "label": [label2id[r["label"]] for r in rows]})

    train_rows = read_jsonl(args.train)
    if args.max_train:
        train_rows = train_rows[:args.max_train]
    val_rows = read_jsonl(args.val)
    eval_rows = read_jsonl(args.eval_file)
    for r in eval_rows:
        assert r["label"] in LABELS, f"eval label {r['label']} not in taxonomy"
    print(f"train={len(train_rows)} val={len(val_rows)} eval={len(eval_rows)} "
          f"classes={len(names)}")

    tok = AutoTokenizer.from_pretrained(args.base)

    def tokenize(batch):
        return tok(batch["text"], truncation=True, max_length=args.max_length)

    train_ds = to_ds(train_rows).map(tokenize, batched=True)
    val_ds = to_ds(val_rows).map(tokenize, batched=True)

    model = AutoModelForSequenceClassification.from_pretrained(
        args.base, num_labels=len(names), id2label=id2label, label2id=label2id)

    use_cuda = torch.cuda.is_available()
    targs = TrainingArguments(
        output_dir="out/checkpoints",
        num_train_epochs=args.epochs,
        learning_rate=args.lr,
        per_device_train_batch_size=args.batch,
        per_device_eval_batch_size=args.batch * 2,
        warmup_ratio=0.06,
        weight_decay=0.01,
        eval_strategy="epoch",
        save_strategy="epoch",
        load_best_model_at_end=True,
        metric_for_best_model="eval_loss",
        save_total_limit=2,
        logging_steps=50,
        fp16=use_cuda,
        seed=args.seed,
        report_to=[],
    )

    trainer = Trainer(model=model, args=targs, train_dataset=train_ds,
                      eval_dataset=val_ds, processing_class=tok)
    trainer.train()

    os.makedirs(args.out, exist_ok=True)
    trainer.save_model(args.out)
    tok.save_pretrained(args.out)
    print(f"saved best model -> {args.out}")

    # ---- frozen eval ----
    model.eval()
    device = model.device
    probs_all = []
    with torch.no_grad():
        for i in range(0, len(eval_rows), 64):
            batch = [r["text"] for r in eval_rows[i:i + 64]]
            enc = tok(batch, truncation=True, max_length=args.max_length,
                      padding=True, return_tensors="pt").to(device)
            logits = model(**enc).logits
            probs_all.append(torch.softmax(logits, dim=-1).cpu().numpy())
    probs = np.concatenate(probs_all)
    y_pred = probs.argmax(axis=1).tolist()
    y_true = [label2id[r["label"]] for r in eval_rows]

    metrics = build_metrics(y_true, y_pred, probs, eval_rows, names)
    metrics["base_model"] = args.base
    with open(args.metrics_out, "w") as f:
        json.dump(metrics, f, indent=2)

    print(json.dumps({k: metrics[k] for k in
                      ("accuracy", "macro_f1", "worst_class",
                       "per_boundary", "fallback_rate_at_threshold")}, indent=2))
    print(f"full metrics -> {args.metrics_out}")


if __name__ == "__main__":
    main()
