"""Frugal router intent taxonomy v1.

Labels are semantic capabilities, never tool/provider names.
Version this file independently of model weights (taxonomy-v1).
"""

TAXONOMY_VERSION = "v1"

LABELS = {
    "search.general": {
        "definition": (
            "Open web lookup, factual or exploratory, with no freshness or "
            "specialist-domain constraint. Stale results are acceptable."
        ),
        "examples": [
            "who founded baseten",
            "best go yaml parser",
            "difference between busl and apache 2.0 license",
        ],
        "guidance": "Terse factual lookups, how-tos, definitions, comparisons.",
    },
    "search.news": {
        "definition": (
            "Freshness-sensitive search: current events, recent releases, prices, "
            "scores, anything where a stale answer is a wrong answer."
        ),
        "examples": [
            "latest x402 foundation news",
            "did qwen release a new model this week",
            "current usdc market cap",
        ],
        "guidance": "Implicit freshness cues matter: 'latest', 'this week', prices, ongoing events.",
    },
    "search.deep": {
        "definition": (
            "Semantic or conceptual research: find-similar, papers, company research, "
            "conceptual matches where keyword search underperforms."
        ),
        "examples": [
            "papers on llm routing with small encoders",
            "companies similar to browserbase",
            "prior art for pay-per-crawl business models",
        ],
        "guidance": "Research register. 'Similar to', 'papers on', 'landscape of', 'prior art'.",
    },
    "extract.url": {
        "definition": (
            "Content retrieval from a known or implied URL where a static fetch "
            "and parse is sufficient. No interaction or JS rendering needed."
        ),
        "examples": [
            "get the readme from that repo page",
            "extract the pricing table from the page i linked",
            "pull the full text of this article",
        ],
        "guidance": (
            "Include URL-less phrasings ('the page I mentioned'); explicit-URL cases "
            "are mostly caught upstream by deterministic rules."
        ),
    },
    "browse.dynamic": {
        "definition": (
            "Requires a real browser: JS rendering, clicking, form input, login "
            "walls, screenshots, or multi-step navigation."
        ),
        "examples": [
            "click through to the changelog and screenshot it",
            "check what the dashboard shows after login",
            "fill the demo form and capture the confirmation",
        ],
        "guidance": "Interaction verbs: click, scroll, log in, screenshot, navigate, fill.",
    },
    "embed": {
        "definition": "Produce vector embeddings of text or documents for indexing/similarity.",
        "examples": [
            "embed these chunks for the index",
            "vectorize the doc set for semantic search",
        ],
        "guidance": "Tier 2 (train now, route later).",
    },
    "transcribe": {
        "definition": "Speech-to-text on audio or video content.",
        "examples": [
            "transcribe this meeting recording",
            "get a transcript of the podcast episode",
        ],
        "guidance": (
            "Tier 2 (train now, route later). Convention (ruled 2026-08-13): "
            "speaker diarization ('mark the speakers', 'extract speakers') is "
            "part of transcription = single-step transcribe. Downstream work on "
            "the transcript ('pull action items', 'summarize it') = multi_step."
        ),
    },
    "code.exec": {
        "definition": "Run or evaluate code in a sandbox and return the output.",
        "examples": [
            "run this snippet and give me the output",
            "execute the benchmark script and report timings",
        ],
        "guidance": "Tier 2 (train now, route later).",
    },
    "generate": {
        "definition": (
            "Delegate text generation to a routed model, operating on provided or "
            "prior content, not on fresh external information."
        ),
        "examples": [
            "summarize this in two lines using the local model",
            "rewrite the changelog entry in plain english",
        ],
        "guidance": "If fresh external info is required, it is search, not generate.",
    },
    "multi_step": {
        "definition": (
            "Intent bundles two or more distinct capabilities in sequence "
            "(e.g. search then extract, browse then transcribe)."
        ),
        "examples": [
            "search for the arch-router paper then extract its abstract",
            "find the earnings call recording and transcribe it",
        ],
        "guidance": (
            "Conjunctions alone are NOT sufficient signal: 'search for cheap and "
            "fast yaml parsers' is single-step search.general."
        ),
    },
    "out_of_scope": {
        "definition": "Not a frugal capability at all. No route exists.",
        "examples": [
            "book me a flight to denver",
            "set a reminder for 6pm",
            "order more coffee pods",
        ],
        "guidance": "Prevents forced misclassification into the nearest tool.",
    },
}

# Boundary pairs that hard-negative generation (stage 3) and the eval set
# must specifically target.
CONFUSION_PAIRS = [
    ("search.general", "search.news"),
    ("search.deep", "search.general"),
    ("extract.url", "browse.dynamic"),
    ("multi_step", "search.general"),
    ("generate", "search.deep"),
]

PERSONAS = [
    "coding agent",
    "research agent",
    "ops/monitoring agent",
    "data pipeline agent",
    "personal assistant agent",
    "terse power user",
]

DOMAINS = [
    "software/devtools",
    "finance",
    "science/papers",
    "e-commerce",
    "news/politics",
    "legal",
    "health",
    "crypto",
    "media",
    "travel",
]

STYLES = [
    "terse fragment",
    "imperative sentence",
    "question form",
    "context-rich paragraph",
    "sloppy with typos",
]


def label_names():
    return sorted(LABELS.keys())


if __name__ == "__main__":
    print(f"taxonomy {TAXONOMY_VERSION}: {len(LABELS)} labels")
    for name in label_names():
        print(f"  {name}: {LABELS[name]['definition'][:70]}...")
