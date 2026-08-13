"""Stage 1: seed harvesting.

Anchors the training register (terse, imperative, agent-authored).
Two sources:
  1. seeds/manual_seeds.jsonl  -- hand-written, always used.
  2. Optional public-dataset harvest (--hf) via the `datasets` library:
     Glaive function-calling / xlam queries mapped onto the taxonomy with
     conservative keyword rules; unmapped rows are discarded. Requires
     network access + `pip install datasets`.

Output: out/stage1_seeds.jsonl
"""
import argparse
import json
import os
import re

from taxonomy import LABELS

# Conservative mapping rules for harvesting external tool-calling datasets.
# Only map when we are confident; everything else is discarded.
HARVEST_RULES = [
    (re.compile(r"\b(latest|today|this week|breaking|current price|right now)\b", re.I), "search.news"),
    (re.compile(r"\b(papers? on|similar to|research on|state of the art|landscape of)\b", re.I), "search.deep"),
    (re.compile(r"\b(transcribe|transcript)\b", re.I), "transcribe"),
    (re.compile(r"\b(embed|embedding|vectorize)\b", re.I), "embed"),
    (re.compile(r"\b(run|execute)\b.*\b(code|script|snippet)\b", re.I), "code.exec"),
    (re.compile(r"\b(click|log ?in|screenshot|scroll|navigate|fill (in|out))\b", re.I), "browse.dynamic"),
    (re.compile(r"\b(extract|scrape|pull|fetch|get)\b.*\b(page|url|article|readme|table)\b", re.I), "extract.url"),
    (re.compile(r"\b(search|find|look up|what is|who is|how (do|does|to))\b", re.I), "search.general"),
]


def load_manual(path):
    rows = []
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            row = json.loads(line)
            assert row["label"] in LABELS, f"unknown label {row['label']}"
            rows.append(row)
    return rows


def harvest_hf(limit_per_source=2000):
    """Best-effort harvest from public tool-calling datasets. Returns []
    (with a warning) when `datasets` or network access is unavailable."""
    try:
        from datasets import load_dataset  # noqa: PLC0415
    except ImportError:
        print("[stage1] `datasets` not installed; skipping HF harvest "
              "(pip install datasets)")
        return []
    sources = [
        ("glaiveai/glaive-function-calling-v2", "chat"),
        ("Salesforce/xlam-function-calling-60k", "query"),
    ]
    rows = []
    for name, field in sources:
        try:
            ds = load_dataset(name, split=f"train[:{limit_per_source}]")
        except Exception as e:  # noqa: BLE001
            print(f"[stage1] could not load {name}: {e}")
            continue
        for rec in ds:
            text = rec.get(field) or ""
            # Glaive stores conversations; take the first USER turn.
            if name.startswith("glaiveai"):
                m = re.search(r"USER:\s*(.+?)(?:\n|$)", text)
                text = m.group(1) if m else ""
            text = " ".join(str(text).split())
            if not (8 <= len(text) <= 300):
                continue
            for pattern, label in HARVEST_RULES:
                if pattern.search(text):
                    rows.append({"text": text.lower(), "label": label,
                                 "source": f"hf:{name}"})
                    break
    print(f"[stage1] harvested {len(rows)} mapped rows from HF datasets")
    return rows


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--manual", default="seeds/manual_seeds.jsonl")
    ap.add_argument("--hf", action="store_true", help="also harvest public HF datasets")
    ap.add_argument("--out", default="out/stage1_seeds.jsonl")
    args = ap.parse_args()

    rows = load_manual(args.manual)
    print(f"[stage1] loaded {len(rows)} manual seeds")
    if args.hf:
        rows += harvest_hf()

    os.makedirs(os.path.dirname(args.out), exist_ok=True)
    seen = set()
    kept = 0
    with open(args.out, "w") as f:
        for row in rows:
            key = row["text"].strip().lower()
            if key in seen:
                continue
            seen.add(key)
            f.write(json.dumps(row) + "\n")
            kept += 1
    by_label = {}
    for row in rows:
        by_label[row["label"]] = by_label.get(row["label"], 0) + 1
    print(f"[stage1] wrote {kept} seeds -> {args.out}")
    for label in sorted(by_label):
        print(f"  {label}: {by_label[label]}")


if __name__ == "__main__":
    main()
