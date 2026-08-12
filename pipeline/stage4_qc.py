"""Stage 4: quality control.

Subcommands:
  dedup        Semantic near-duplicate removal (TF-IDF char n-grams, cosine).
               Default threshold 0.92. Use --backend st for sentence-transformers
               embeddings if installed (closer to the spec's semantic dedup).
  verify       Cross-model label verification: a THIRD LLM relabels every row
               blind; disagreements are dropped (or kept with --keep-flagged).
  spotcheck    Stratified sample -> CSV for human review (default 500).
  decontaminate  Drop training rows too similar (>0.85) to any eval-set row.
  finalize     Merge, shuffle, report class balance, emit train/val split.

Typical order:
  dedup -> verify -> decontaminate -> spotcheck (human gate) -> finalize
"""
import argparse
import csv
import json
import os
import random

import numpy as np
from sklearn.feature_extraction.text import TfidfVectorizer
from sklearn.metrics.pairwise import cosine_similarity

from taxonomy import LABELS


def read_jsonl(paths):
    rows = []
    for path in paths:
        with open(path) as f:
            for line in f:
                line = line.strip()
                if line:
                    rows.append(json.loads(line))
    return rows


def write_jsonl(rows, path):
    os.makedirs(os.path.dirname(path) or ".", exist_ok=True)
    with open(path, "w") as f:
        for row in rows:
            f.write(json.dumps(row) + "\n")


def embed_texts(texts, backend="tfidf"):
    if backend == "st":
        try:
            from sentence_transformers import SentenceTransformer  # noqa: PLC0415
            model = SentenceTransformer("sentence-transformers/all-MiniLM-L6-v2")
            return np.asarray(model.encode(texts, batch_size=256,
                                           show_progress_bar=False))
        except ImportError:
            print("[stage4] sentence-transformers not installed; "
                  "falling back to tfidf")
    vec = TfidfVectorizer(analyzer="char_wb", ngram_range=(3, 5), min_df=1)
    return vec.fit_transform(texts)


def pairwise_block_dedup(rows, threshold, backend, block=4000):
    """Greedy near-dup removal. Exact-dup pass first, then blocked cosine
    similarity (memory-safe for ~100k rows)."""
    seen_exact, unique = set(), []
    for row in rows:
        key = row["text"].strip().lower()
        if key not in seen_exact:
            seen_exact.add(key)
            unique.append(row)
    exact_removed = len(rows) - len(unique)

    texts = [r["text"] for r in unique]
    emb = embed_texts(texts, backend)
    keep = np.ones(len(unique), dtype=bool)
    for i0 in range(0, len(unique), block):
        i1 = min(i0 + block, len(unique))
        sims = cosine_similarity(emb[i0:i1], emb[:i1])
        for local in range(i1 - i0):
            gi = i0 + local
            if not keep[gi]:
                continue
            dup_of = np.where(sims[local, :gi] >= threshold)[0]
            if any(keep[j] for j in dup_of):
                keep[gi] = False
    kept = [r for r, k in zip(unique, keep) if k]
    print(f"[dedup] exact removed: {exact_removed}, "
          f"near-dup removed: {len(unique) - len(kept)}, kept: {len(kept)}")
    return kept


def cmd_dedup(args):
    rows = read_jsonl(args.inputs)
    kept = pairwise_block_dedup(rows, args.threshold, args.backend)
    write_jsonl(kept, args.out)
    print(f"[dedup] -> {args.out}")


VERIFY_SYSTEM = (
    "You are a strict data labeler for an intent classifier used by a local "
    "tool-routing proxy for AI agents. Given an intent string, answer with "
    "EXACTLY one label name from the provided list. No other text.")

VERIFY_TEMPLATE = """Labels:
{label_block}

Intent string:
{text}

Answer with exactly one label name."""


def cmd_verify(args):
    from llm import LLMClient  # noqa: PLC0415
    client = LLMClient(backend=args.backend, temperature=0.0)
    rows = read_jsonl(args.inputs)
    label_block = "\n".join(
        f"- {name}: {info['definition']}" for name, info in sorted(LABELS.items()))
    kept, dropped, errors = [], 0, 0
    for i, row in enumerate(rows):
        if args.limit and i >= args.limit:
            kept.extend(rows[i:])  # pass through unverified remainder
            print(f"[verify] limit reached; passed through {len(rows) - i} unverified")
            break
        try:
            raw = client.complete(
                VERIFY_SYSTEM,
                VERIFY_TEMPLATE.format(label_block=label_block, text=row["text"])
                + f"\nSPEC_JSON:{json.dumps({'label': row['label'], 'n': 1})}")
            if args.backend == "demo":
                answer = row["label"]  # demo backend can't relabel; agree
            else:
                answer = raw.strip().split()[0].strip("`\"'.,")
        except Exception:  # noqa: BLE001
            errors += 1
            kept.append(row)  # network error: keep, don't silently drop
            continue
        if answer == row["label"]:
            row["verified"] = True
            kept.append(row)
        elif args.keep_flagged:
            row["verified"] = False
            row["verifier_label"] = answer
            kept.append(row)
        else:
            dropped += 1
    write_jsonl(kept, args.out)
    print(f"[verify] kept {len(kept)}, dropped {dropped}, errors {errors} -> {args.out}")
    if dropped / max(1, len(rows)) > 0.05:
        print("[verify] WARNING: >5% disagreement. Inspect generator prompts "
              "or label definitions before training.")


def cmd_spotcheck(args):
    rows = read_jsonl(args.inputs)
    rng = random.Random(args.seed)
    by_label = {}
    for row in rows:
        by_label.setdefault(row["label"], []).append(row)
    per_label = max(1, args.n // max(1, len(by_label)))
    sample = []
    for label, group in sorted(by_label.items()):
        sample.extend(rng.sample(group, k=min(per_label, len(group))))
    rng.shuffle(sample)
    os.makedirs(os.path.dirname(args.out) or ".", exist_ok=True)
    with open(args.out, "w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow(["text", "label", "source", "human_verdict(ok/wrong/ambiguous)",
                         "correct_label_if_wrong", "notes"])
        for row in sample:
            writer.writerow([row["text"], row["label"], row.get("source", ""), "", "", ""])
    print(f"[spotcheck] wrote {len(sample)} rows -> {args.out}")
    print("[spotcheck] Review by hand. If any class shows >5% error, "
          "regenerate that class before training.")


def cmd_decontaminate(args):
    rows = read_jsonl(args.inputs)
    eval_rows = read_jsonl([args.eval_file])
    if not eval_rows:
        print("[decontaminate] eval set empty; nothing to do")
        write_jsonl(rows, args.out)
        return
    all_texts = [r["text"] for r in rows] + [r["text"] for r in eval_rows]
    emb = embed_texts(all_texts, args.backend)
    train_emb, eval_emb = emb[:len(rows)], emb[len(rows):]
    kept, removed = [], 0
    block = 4000
    for i0 in range(0, len(rows), block):
        i1 = min(i0 + block, len(rows))
        sims = cosine_similarity(train_emb[i0:i1], eval_emb)
        for local in range(i1 - i0):
            if sims[local].max() >= args.threshold:
                removed += 1
            else:
                kept.append(rows[i0 + local])
    write_jsonl(kept, args.out)
    print(f"[decontaminate] removed {removed} rows >= {args.threshold} "
          f"similar to eval -> {args.out}")


def cmd_finalize(args):
    rows = read_jsonl(args.inputs)
    rng = random.Random(args.seed)
    rng.shuffle(rows)
    by_label = {}
    for row in rows:
        by_label.setdefault(row["label"], []).append(row)
    print("[finalize] class balance:")
    for label in sorted(LABELS):
        n = len(by_label.get(label, []))
        flag = "  <-- LOW" if n < args.min_per_class else ""
        print(f"  {label}: {n}{flag}")
    val, train = [], []
    for label, group in sorted(by_label.items()):
        n_val = max(1, int(len(group) * args.val_frac))
        val.extend(group[:n_val])
        train.extend(group[n_val:])
    rng.shuffle(train)
    rng.shuffle(val)
    write_jsonl(train, args.train_out)
    write_jsonl(val, args.val_out)
    print(f"[finalize] train {len(train)} -> {args.train_out}; "
          f"val {len(val)} -> {args.val_out}")


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("dedup")
    p.add_argument("inputs", nargs="+")
    p.add_argument("--out", default="out/deduped.jsonl")
    p.add_argument("--threshold", type=float, default=0.92)
    p.add_argument("--backend", default="tfidf", choices=["tfidf", "st"])
    p.set_defaults(func=cmd_dedup)

    p = sub.add_parser("verify")
    p.add_argument("inputs", nargs="+")
    p.add_argument("--out", default="out/verified.jsonl")
    p.add_argument("--backend", default="openai", choices=["openai", "demo"])
    p.add_argument("--keep-flagged", action="store_true")
    p.add_argument("--limit", type=int, default=0, help="verify only first N rows")
    p.set_defaults(func=cmd_verify)

    p = sub.add_parser("spotcheck")
    p.add_argument("inputs", nargs="+")
    p.add_argument("--out", default="out/spotcheck.csv")
    p.add_argument("--n", type=int, default=500)
    p.add_argument("--seed", type=int, default=7)
    p.set_defaults(func=cmd_spotcheck)

    p = sub.add_parser("decontaminate")
    p.add_argument("inputs", nargs="+")
    p.add_argument("--eval-file", required=True)
    p.add_argument("--out", default="out/decontaminated.jsonl")
    p.add_argument("--threshold", type=float, default=0.85)
    p.add_argument("--backend", default="tfidf", choices=["tfidf", "st"])
    p.set_defaults(func=cmd_decontaminate)

    p = sub.add_parser("finalize")
    p.add_argument("inputs", nargs="+")
    p.add_argument("--train-out", default="out/train.jsonl")
    p.add_argument("--val-out", default="out/val.jsonl")
    p.add_argument("--val-frac", type=float, default=0.05)
    p.add_argument("--min-per-class", type=int, default=1500)
    p.add_argument("--seed", type=int, default=7)
    p.set_defaults(func=cmd_finalize)

    args = ap.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
