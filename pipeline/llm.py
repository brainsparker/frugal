"""LLM client abstraction for the data pipeline.

Two backends:
  - "openai": any OpenAI-compatible chat completions endpoint.
      Env vars: FRUGAL_GEN_BASE_URL (default https://api.openai.com/v1),
                FRUGAL_GEN_API_KEY, FRUGAL_GEN_MODEL.
      Works with OpenAI, OpenRouter, Together, Ollama (http://localhost:11434/v1), etc.
  - "demo": deterministic template expander, no network. Lets you run the
      whole pipeline end to end and inspect artifacts before spending money.

Stage 2/3 need TWO generator families in real runs (pass different
base_url/model per run); stage 4 verification needs a THIRD.
"""
import hashlib
import json
import os
import random
import time

import requests


class LLMClient:
    def __init__(self, backend="openai", base_url=None, api_key=None, model=None,
                 temperature=0.9, max_retries=4):
        self.backend = backend
        self.base_url = (base_url or os.environ.get(
            "FRUGAL_GEN_BASE_URL", "https://api.openai.com/v1")).rstrip("/")
        self.api_key = api_key or os.environ.get("FRUGAL_GEN_API_KEY", "")
        self.model = model or os.environ.get("FRUGAL_GEN_MODEL", "")
        self.temperature = temperature
        self.max_retries = max_retries
        if backend == "openai" and not self.model:
            raise ValueError(
                "Set FRUGAL_GEN_MODEL (and FRUGAL_GEN_API_KEY / FRUGAL_GEN_BASE_URL) "
                "or use --backend demo.")

    def complete(self, system, user):
        if self.backend == "demo":
            return self._demo(system, user)
        payload = {
            "model": self.model,
            "temperature": self.temperature,
            "messages": [
                {"role": "system", "content": system},
                {"role": "user", "content": user},
            ],
        }
        headers = {"Authorization": f"Bearer {self.api_key}",
                   "Content-Type": "application/json"}
        last_err = None
        for attempt in range(self.max_retries):
            try:
                r = requests.post(f"{self.base_url}/chat/completions",
                                  json=payload, headers=headers, timeout=120)
                if r.status_code == 429 or r.status_code >= 500:
                    raise RuntimeError(f"retryable status {r.status_code}")
                r.raise_for_status()
                return r.json()["choices"][0]["message"]["content"]
            except Exception as e:  # noqa: BLE001
                last_err = e
                time.sleep(2 ** attempt)
        raise RuntimeError(f"LLM call failed after {self.max_retries} retries: {last_err}")

    # ------------------------------------------------------------------
    # Demo backend: deterministic, offline. Produces plausible-shaped JSONL
    # so dedup/QC/decontamination code paths can be exercised for real.
    # ------------------------------------------------------------------
    _DEMO_TEMPLATES = {
        "search.general": ["how does {x} work in {d}", "compare {x} options for {d}",
                           "docs for {x}", "typical {x} setup for {d} teams"],
        "search.news": ["latest {x} announcement in {d}", "any {x} update this week",
                        "current {x} numbers for {d}", "did {x} ship yet"],
        "search.deep": ["papers on {x} for {d}", "companies similar to {x}",
                        "landscape of {x} tooling in {d}", "prior art for {x}"],
        "extract.url": ["pull the {x} section from the page i linked",
                        "get the full text of that {d} article",
                        "extract the {x} table from the vendor page"],
        "browse.dynamic": ["log in and screenshot the {x} panel",
                           "click through the {d} flow and note the {x}",
                           "scroll the {d} feed and collect {x} items"],
        "embed": ["embed the {x} docs for the {d} index",
                  "vectorize the {x} corpus"],
        "transcribe": ["transcribe the {d} call about {x}",
                       "get a transcript of the {x} recording"],
        "code.exec": ["run the {x} script and report output",
                      "execute the {x} benchmark for {d}"],
        "generate": ["summarize the {x} notes in two lines",
                     "rewrite the {x} doc for a {d} audience"],
        "multi_step": ["find the {x} paper then extract its abstract",
                       "search {d} vendors for {x} and pull each pricing page"],
        "out_of_scope": ["book a {d} trip for the {x} offsite",
                         "remind me about the {x} meeting"],
    }
    _DEMO_X = ["rate limiting", "usage ledger", "provider failover", "intent routing",
               "settlement", "quantization", "webhooks", "cache eviction", "sdk auth",
               "billing export", "schema diff", "token pricing"]

    def _demo(self, system, user):
        seed = int(hashlib.sha256((system + user).encode()).hexdigest()[:8], 16)
        rng = random.Random(seed)
        try:
            spec = json.loads(user.split("SPEC_JSON:")[-1])
        except (json.JSONDecodeError, ValueError):
            spec = {}
        label = spec.get("label", "search.general")
        domain = spec.get("domain", "software/devtools")
        n = int(spec.get("n", 5))
        templates = self._DEMO_TEMPLATES.get(label, self._DEMO_TEMPLATES["search.general"])
        out = []
        for _ in range(n):
            t = rng.choice(templates)
            text = t.format(x=rng.choice(self._DEMO_X), d=domain.split("/")[0])
            if spec.get("style") == "sloppy with typos" and len(text) > 12:
                i = rng.randrange(2, len(text) - 2)
                text = text[:i] + text[i + 1:]
            if spec.get("style") == "question form" and not text.endswith("?"):
                text = text + "?"
            out.append({"text": text, "label": label,
                        "definition_echo": spec.get("definition", "")})
        return json.dumps(out)
