"""Orchestrator: runs stages 1-4 in order.

Demo (offline, no API keys; verifies the plumbing end to end):
  python3 run_pipeline.py --demo

Real run (set env vars per generator family):
  python3 run_pipeline.py --per-label 1500 \
      --family-a "https://openrouter.ai/api/v1|<KEY>|<modelA>" \
      --family-b "https://api.together.xyz/v1|<KEY>|<modelB>" \
      --verifier "https://api.openai.com/v1|<KEY>|<modelC>"

The human spot-check gate is intentionally NOT automated: the pipeline stops
after writing out/spotcheck.csv. Review it, then run finalize yourself:
  python3 stage4_qc.py finalize out/decontaminated.jsonl
"""
import argparse
import os
import subprocess
import sys


def sh(cmd, env_extra=None):
    env = dict(os.environ)
    if env_extra:
        env.update(env_extra)
    print(f"\n$ {' '.join(cmd)}")
    result = subprocess.run(cmd, env=env)
    if result.returncode != 0:
        sys.exit(result.returncode)


def family_env(spec):
    base_url, key, model = spec.split("|", 2)
    return {"FRUGAL_GEN_BASE_URL": base_url, "FRUGAL_GEN_API_KEY": key,
            "FRUGAL_GEN_MODEL": model}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--demo", action="store_true")
    ap.add_argument("--per-label", type=int, default=1500,
                    help="per label PER FAMILY (2 families -> ~3000/label pre-QC)")
    ap.add_argument("--family-a", help="base_url|api_key|model")
    ap.add_argument("--family-b", help="base_url|api_key|model")
    ap.add_argument("--verifier", help="base_url|api_key|model (third family)")
    ap.add_argument("--eval-file", default="eval/eval_v1.jsonl")
    ap.add_argument("--hf", action="store_true", help="stage 1: also harvest HF datasets")
    args = ap.parse_args()

    py = sys.executable

    # Stage 1
    stage1 = [py, "stage1_seeds.py"]
    if args.hf:
        stage1.append("--hf")
    sh(stage1)

    if args.demo:
        sh([py, "stage2_generate.py", "--backend", "demo", "--per-label", "40",
            "--tag", "demoA"])
        sh([py, "stage2_generate.py", "--backend", "demo", "--per-label", "40",
            "--tag", "demoB", "--seed", "13"])
        sh([py, "stage3_hardnegatives.py", "--backend", "demo",
            "--pairs-per-boundary", "20", "--adversarial-per-brief", "10"])
        sh([py, "stage4_qc.py", "dedup", "out/stage2_demoA.jsonl",
            "out/stage2_demoB.jsonl", "out/stage3_hardneg.jsonl",
            "out/stage1_seeds.jsonl"])
        sh([py, "stage4_qc.py", "verify", "out/deduped.jsonl",
            "--backend", "demo"])
    else:
        if not (args.family_a and args.family_b and args.verifier):
            sys.exit("Real runs need --family-a, --family-b, and --verifier "
                     "(or use --demo).")
        sh([py, "stage2_generate.py", "--per-label", str(args.per_label),
            "--tag", "familyA"], family_env(args.family_a))
        sh([py, "stage2_generate.py", "--per-label", str(args.per_label),
            "--tag", "familyB", "--seed", "13"], family_env(args.family_b))
        sh([py, "stage3_hardnegatives.py"], family_env(args.family_a))
        sh([py, "stage4_qc.py", "dedup", "out/stage2_familyA.jsonl",
            "out/stage2_familyB.jsonl", "out/stage3_hardneg.jsonl",
            "out/stage1_seeds.jsonl"])
        sh([py, "stage4_qc.py", "verify", "out/deduped.jsonl"],
           family_env(args.verifier))

    if os.path.exists(args.eval_file):
        sh([py, "stage4_qc.py", "decontaminate", "out/verified.jsonl",
            "--eval-file", args.eval_file])
        final_input = "out/decontaminated.jsonl"
    else:
        print(f"\n[pipeline] NOTE: eval file {args.eval_file} not found; "
              "skipping decontamination. Build the eval set before any real "
              "training run.")
        final_input = "out/verified.jsonl"

    sh([py, "stage4_qc.py", "spotcheck", final_input])
    print("\n[pipeline] STOP: human gate. Review out/spotcheck.csv by hand.")
    print(f"[pipeline] Then run: {py} stage4_qc.py finalize {final_input}")


if __name__ == "__main__":
    main()
