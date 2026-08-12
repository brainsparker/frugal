"""Stage 2: grid-based LLM generation (~70% of training data).

Builds a persona x domain x style grid per label, prompts a generator LLM
with 5 sampled stage-1 seeds for register grounding, and requires the
generator to echo the label definition back (self-check that catches
prompt truncation / label confusion at the source).

Run TWICE with two different model families and mix (the script tags each
row with gen_model so stage 4 can verify the 50/50 mix):
  python3 stage2_generate.py --per-label 1500 --tag familyA
  FRUGAL_GEN_BASE_URL=... FRUGAL_GEN_MODEL=... python3 stage2_generate.py --per-label 1500 --tag familyB

Offline end-to-end test:  python3 stage2_generate.py --backend demo --per-label 40 --tag demo

Output: out/stage2_<tag>.jsonl
"""
import argparse
import itertools
import json
import os
import random

from llm import LLMClient
from taxonomy import DOMAINS, LABELS, PERSONAS, STYLES, TAXONOMY_VERSION

SYSTEM = (
    "You generate synthetic training data for an intent classifier used by a "
    "local tool-routing proxy for AI agents. The classifier input is the short "
    "`intent` string an AI agent passes to a routing tool. The register is "
    "terse, imperative, agent-authored: not chatty end-user phrasing. "
    "Output ONLY a JSON array of objects with keys: text, label, definition_echo. "
    "definition_echo must restate the label definition in your own words (one "
    "sentence). Vary vocabulary and structure aggressively; no two texts may "
    "share more than 4 consecutive words."
)

USER_TEMPLATE = """Generate {n} intent strings for ONE label.

Label: {label}
Definition: {definition}
Extra guidance: {guidance}

Persona writing the intent: {persona}
Subject domain: {domain}
Phrasing style: {style}

Register examples from real seeds (match their terseness, NOT their topics):
{seed_block}

Rules:
- Every example must genuinely belong to `{label}` under the definition above.
- Stay inside the given domain and phrasing style.
- "sloppy with typos" style: include realistic typos/missing words, still unambiguous.
- Do NOT mention tool or provider names (no 'use serper', no 'via exa').
- Output only the JSON array.

SPEC_JSON:{spec_json}"""


def load_seeds(path):
    by_label = {}
    with open(path) as f:
        for line in f:
            row = json.loads(line)
            by_label.setdefault(row["label"], []).append(row["text"])
    return by_label


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--seeds", default="out/stage1_seeds.jsonl")
    ap.add_argument("--per-label", type=int, default=1500,
                    help="target examples per label for THIS run")
    ap.add_argument("--batch-size", type=int, default=10)
    ap.add_argument("--backend", default="openai", choices=["openai", "demo"])
    ap.add_argument("--tag", required=True, help="generator family tag, e.g. familyA")
    ap.add_argument("--labels", nargs="*", default=None, help="subset of labels")
    ap.add_argument("--seed", type=int, default=7)
    args = ap.parse_args()

    rng = random.Random(args.seed)
    seeds = load_seeds(args.seeds)
    client = LLMClient(backend=args.backend)
    labels = args.labels or sorted(LABELS.keys())

    grid = list(itertools.product(PERSONAS, DOMAINS, STYLES))
    out_path = f"out/stage2_{args.tag}.jsonl"
    os.makedirs("out", exist_ok=True)

    total, bad_json, bad_echo = 0, 0, 0
    with open(out_path, "w") as out:
        for label in labels:
            info = LABELS[label]
            n_batches = max(1, args.per_label // args.batch_size)
            cells = rng.sample(grid, k=min(n_batches, len(grid)))
            # Reuse cells (with reshuffle) if per-label demand exceeds grid size.
            while len(cells) < n_batches:
                cells += rng.sample(grid, k=min(n_batches - len(cells), len(grid)))
            written = 0
            for persona, domain, style in cells:
                seed_pool = seeds.get(label, []) or [
                    s for pool in seeds.values() for s in pool]
                seed_block = "\n".join(
                    f"- {s}" for s in rng.sample(seed_pool, k=min(5, len(seed_pool))))
                spec = {"label": label, "definition": info["definition"],
                        "persona": persona, "domain": domain, "style": style,
                        "n": args.batch_size}
                user = USER_TEMPLATE.format(
                    n=args.batch_size, label=label, definition=info["definition"],
                    guidance=info["guidance"], persona=persona, domain=domain,
                    style=style, seed_block=seed_block,
                    spec_json=json.dumps(spec))
                try:
                    raw = client.complete(SYSTEM, user)
                    start, end = raw.find("["), raw.rfind("]")
                    rows = json.loads(raw[start:end + 1])
                except Exception:  # noqa: BLE001
                    bad_json += 1
                    continue
                for row in rows:
                    text = " ".join(str(row.get("text", "")).split())
                    echo = str(row.get("definition_echo", ""))
                    if not (6 <= len(text) <= 400):
                        continue
                    if len(echo) < 15:  # failed self-check
                        bad_echo += 1
                        continue
                    out.write(json.dumps({
                        "text": text, "label": label, "source": "stage2",
                        "persona": persona, "domain": domain, "style": style,
                        "gen_model": args.tag,
                        "taxonomy": TAXONOMY_VERSION}) + "\n")
                    written += 1
                    total += 1
                if written >= args.per_label:
                    break
            print(f"[stage2] {label}: {written} examples")
    print(f"[stage2] wrote {total} rows -> {out_path} "
          f"(bad_json batches: {bad_json}, failed echo self-check: {bad_echo})")


if __name__ == "__main__":
    main()
