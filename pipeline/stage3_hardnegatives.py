"""Stage 3: hard negatives and boundary cases (~20% of training data).

For each confusion pair in taxonomy.CONFUSION_PAIRS, generates contrastive
minimal-edit pairs: two intents that differ by a small phrase edit but flip
the label. Also generates adversarial singles (freshness words used
non-temporally, tool names mentioned but wrong, URLs inside deep-search
intents).

Offline test:  python3 stage3_hardnegatives.py --backend demo --pairs-per-boundary 20

Output: out/stage3_hardneg.jsonl
"""
import argparse
import json
import os

from llm import LLMClient
from taxonomy import CONFUSION_PAIRS, LABELS, TAXONOMY_VERSION

SYSTEM = (
    "You generate HARD NEGATIVE training pairs for an intent classifier used "
    "by a local tool-routing proxy for AI agents. Each pair contains two short "
    "agent-authored intent strings that are lexically similar (minimal edit) "
    "but belong to DIFFERENT labels. Output ONLY a JSON array of objects with "
    "keys: text_a, label_a, text_b, label_b, edit_note."
)

PAIR_TEMPLATE = """Generate {n} contrastive minimal-edit pairs for this label boundary.

Label A: {label_a}
Definition A: {definition_a}

Label B: {label_b}
Definition B: {definition_b}

Requirements:
- text_a must clearly be {label_a}; text_b must clearly be {label_b}.
- The two texts should share most of their wording; the flip should hinge on
  a small, realistic phrase change (like the difference between "get the
  pricing page" and "click through the pricing calculator").
- edit_note: one clause naming what flipped the label.
- Terse agent register. No tool/provider names.
- Output only the JSON array.

SPEC_JSON:{spec_json}"""

ADVERSARIAL_BRIEFS = [
    ("search.general",
     "freshness-looking words used NON-temporally, e.g. 'new york news sites "
     "directory', 'latest.js library docs', 'current limiting resistor formula'"),
    ("search.general",
     "browser/tool words used incidentally where the task is plain search, "
     "e.g. 'best headless browser for scraping' is a search, not a browse"),
    ("search.deep",
     "intents that contain a URL but ask for conceptually-similar OTHER "
     "sources, e.g. 'find papers similar to the one at this link'"),
    ("extract.url",
     "no explicit URL in the text; the URL is implied by context, e.g. 'pull "
     "the spec section from the page above'"),
    ("multi_step",
     "conjunction traps: also generate near-miss SINGLE-step intents with "
     "'and' that are NOT multi_step, labeled with their true single label"),
]

ADV_TEMPLATE = """Generate {n} adversarial single examples.

Target label: {label}
Definition: {definition}
Adversarial brief: {brief}

Rules:
- Each example must STILL genuinely belong to the target label (or, where the
  brief says so, to the named true label). Set the correct `label` per row.
- Output ONLY a JSON array of objects with keys: text, label, definition_echo.

SPEC_JSON:{spec_json}"""


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--backend", default="openai", choices=["openai", "demo"])
    ap.add_argument("--pairs-per-boundary", type=int, default=300)
    ap.add_argument("--adversarial-per-brief", type=int, default=150)
    ap.add_argument("--batch-size", type=int, default=10)
    ap.add_argument("--tag", default="hardneg")
    args = ap.parse_args()

    client = LLMClient(backend=args.backend, temperature=1.0)
    os.makedirs("out", exist_ok=True)
    out_path = "out/stage3_hardneg.jsonl"
    total = 0

    with open(out_path, "w") as out:
        # Contrastive pairs per confusion boundary
        for label_a, label_b in CONFUSION_PAIRS:
            n_batches = max(1, args.pairs_per_boundary // args.batch_size)
            written = 0
            for i in range(n_batches):
                spec = {"label": label_a, "label_b": label_b,
                        "n": args.batch_size, "batch": i}
                user = PAIR_TEMPLATE.format(
                    n=args.batch_size,
                    label_a=label_a, definition_a=LABELS[label_a]["definition"],
                    label_b=label_b, definition_b=LABELS[label_b]["definition"],
                    spec_json=json.dumps(spec))
                try:
                    raw = client.complete(SYSTEM, user)
                    if args.backend == "demo":
                        # Demo backend emits singles; synthesize pair shape.
                        rows_a = json.loads(raw)
                        rows = [{"text_a": r["text"], "label_a": label_a,
                                 "text_b": r["text"] + " and screenshot it",
                                 "label_b": label_b, "edit_note": "demo"}
                                for r in rows_a]
                    else:
                        start, end = raw.find("["), raw.rfind("]")
                        rows = json.loads(raw[start:end + 1])
                except Exception:  # noqa: BLE001
                    continue
                for row in rows:
                    for text_key, label_key in (("text_a", "label_a"), ("text_b", "label_b")):
                        text = " ".join(str(row.get(text_key, "")).split())
                        label = row.get(label_key, "")
                        if label not in LABELS or not (6 <= len(text) <= 400):
                            continue
                        out.write(json.dumps({
                            "text": text, "label": label, "source": "stage3.pair",
                            "boundary": f"{label_a}|{label_b}",
                            "edit_note": str(row.get("edit_note", ""))[:200],
                            "gen_model": args.tag,
                            "taxonomy": TAXONOMY_VERSION}) + "\n")
                        written += 1
                        total += 1
            print(f"[stage3] boundary {label_a} | {label_b}: {written} rows")

        # Adversarial singles
        for label, brief in ADVERSARIAL_BRIEFS:
            n_batches = max(1, args.adversarial_per_brief // args.batch_size)
            written = 0
            for i in range(n_batches):
                spec = {"label": label, "n": args.batch_size,
                        "brief": brief[:60], "batch": i}
                user = ADV_TEMPLATE.format(
                    n=args.batch_size, label=label,
                    definition=LABELS[label]["definition"], brief=brief,
                    spec_json=json.dumps(spec))
                try:
                    raw = client.complete(SYSTEM, user)
                    start, end = raw.find("["), raw.rfind("]")
                    rows = json.loads(raw[start:end + 1])
                except Exception:  # noqa: BLE001
                    continue
                for row in rows:
                    text = " ".join(str(row.get("text", "")).split())
                    row_label = row.get("label", label)
                    if row_label not in LABELS or not (6 <= len(text) <= 400):
                        continue
                    out.write(json.dumps({
                        "text": text, "label": row_label,
                        "source": "stage3.adversarial", "brief": brief[:80],
                        "gen_model": args.tag,
                        "taxonomy": TAXONOMY_VERSION}) + "\n")
                    written += 1
                    total += 1
            print(f"[stage3] adversarial '{brief[:50]}...': {written} rows")

    print(f"[stage3] wrote {total} rows -> {out_path}")


if __name__ == "__main__":
    main()
