# frugal

**Tool calls are the new tokens.**

For most agentic workloads, the tool bill exceeds the model bill. A
Firecrawl scrape costs more than the gpt-4o-mini call that consumes its
output. A Tavily search costs 25× a Serper search and 100× a self-hosted
SearXNG. Frugal is the cost-arbitrage layer for those tool calls —
search, extract, browse, code-exec, embeddings — routed to a free/local
provider first, falling back to the cheapest paid only when needed.
Works with any model. One signed Go binary. Your keys. No account.
Source-available (BUSL 1.1 → Apache 2.0).

[frugal.sh](https://frugal.sh) · [GitHub](https://github.com/brainsparker/frugal) · [Strategy](./frugal-strategy-v6.md)

```
search    ·  SearXNG    → Serper       → Tavily
extract   ·  Trafilatura → —            → Firecrawl
browse    ·  Playwright  → Browserless  → Browserbase
embed     ·  nomic-embed → 3-small      → 3-large
code-exec ·  local Docker → E2B         → Modal
chat      ·  Ollama       → mini/haiku  → frontier
```

The leftmost configured provider wins. Free/local first, cheap paid as
fallback, premium paid only when explicitly opted in. See the full
rack-rate table on [frugal.sh](https://frugal.sh).

## Quickstart

**1. Install**

```bash
curl -fsSL https://frugal.sh/install | bash
```

**2. Set your keys** (BYOK — at least one chat-model key; add a search key if you want `fresh-facts`)

```bash
export OPENAI_API_KEY=sk-...
export TAVILY_API_KEY=tvly-...     # also accepts SERPER_API_KEY, ANTHROPIC_API_KEY, GOOGLE_API_KEY
```

**3a. Add Frugal to your agent** (Claude Desktop, Cursor, Claude Code)

```bash
frugal mcp install
# auto-detects each agent client and merges 'frugal' into its MCP config
```

**3b. Or, run a task from the CLI**

```bash
frugal run "current iPhone prices"
# → recipe: fresh-facts
# → step 1: frugal__search routed to serper @ $0.0003
# → step 2: gpt-4.1-nano(search results + question)
# done. cost: $0.0008
```

---

## Why Frugal isn't just another model router

The model-router space is crowded — OpenRouter, LiteLLM, Martian, Helicone,
Portkey. It collapses into a price/latency benchmark fight and the
differentiation is thin. Routed-*search* MCP servers also exist — at least
two open-source projects already route across 5–7 search providers.

Frugal's wedge: **breadth × cost-first × single binary × CLI as a peer
surface to MCP.** One router across search + extract + browse + code-exec +
embeddings + cache + chat — not just search. Every routing pick backed by
the eval scorecard. Single signed Go binary, no Python venv. `frugal run`
for scripts and humans, `frugal mcp serve` for agents.

The pitch isn't "we route models" — it's "we route work."

| Concept | What it is |
|---|---|
| **Capability** | A toolchain primitive: local model, hosted model, search, browser/fetch, code execution, extraction, embeddings & vector search, semantic cache. |
| **Recipe** | A deterministic step list — one or more tool calls and/or model calls — that solves a category of task at the cheapest reliable cost. |
| **Use case** | The named runtime artifact for a recipe (`research-synthesis`, `code-dev`, `factual-qa`, `structured-extraction`, …) with its eval workload. |

## Recipes — common tasks, cheapest reliable paths

Most AI tasks have a cheaper path than "call the frontier model." Each row is
the cheapest reliable default Frugal aims to route. Status tags are honest —
[component status](#toolchain-components) is the canonical source of truth.

| Task | Cheapest reliable path | Status |
|---|---|---|
| Summarize a document | Local small model | Planned (Phase 3) |
| Fresh facts (news, prices, schedules) | Search + small hosted model | **Shipping** (`fresh-facts` recipe + `frugal__search`) |
| Extract from a webpage | Browser/fetch + local model | Planned (Phase 2 browser; Phase 3 local) |
| Complex reasoning (planning, hard math, novel code) | Hosted frontier model | Phase 1 (recipe step uses internal chat router) |
| Code generation, refactors | Local code model → hosted fallback | Partial (Phase 1 hosted; Phase 3 local) |
| Repeated / near-duplicate questions | Semantic cache | Planned (Phase 2) |
| Multi-source research | Search + rerank, hosted-if-needed | **Shipping (search)** + Phase 2 (rerank) |
| Structured extraction (text → JSON) | Smallest JSON-mode-reliable hosted model | Phase 1 (recipe step uses internal chat router) |

Five recipes ship today as named use cases in `config/use_cases/`:
[`research-synthesis`](./config/use_cases/research-synthesis.yaml),
[`code-dev`](./config/use_cases/code-dev.yaml),
[`factual-qa`](./config/use_cases/factual-qa.yaml),
[`structured-extraction`](./config/use_cases/structured-extraction.yaml),
and [`fresh-facts`](./config/use_cases/fresh-facts.yaml) — the latter
lights up alongside `frugal__search`.

## Two surfaces, one router

| Surface | Audience | What it does |
|---|---|---|
| **CLI** — `frugal run/route/compare` | Scripts, humans, CI, Makefiles, ad-hoc | Dispatches a natural-language task to the cheapest reliable recipe and executes it. |
| **MCP server** — `frugal mcp serve` | Agent clients (Claude Code, Claude Desktop, Cursor, custom) | Exposes routed tools (`frugal__search`, later `frugal__extract`, `frugal__browse`, `frugal__chat`, …) over the [Model Context Protocol](https://modelcontextprotocol.io). |

The MCP server speaks **stdio** by default (what Claude Desktop / Cursor /
Claude Code consume for locally-installed servers) and **Streamable HTTP**
behind `--http :PORT` for remote deployments. `frugal mcp install` writes
the right config for each detected client.

The CLI dispatches through the same routing engine — `frugal run` calls
the same code path as an agent calling `frugal__search` via MCP, by
loopback through the in-process server. One code path, one set of routing
decisions, one set of metrics.

### CLI

```bash
$ frugal run "current iPhone prices"
→ recipe: fresh-facts
→ step 1: frugal__search("current iPhone prices")
→ step 2: gpt-4.1-nano(search results + question)
done. cost: $0.0008

$ frugal route "extract tables from this PDF"
→ recipe: pdf-extract (planned)
→ would call: frugal__extract(file://…)
→ would call: gemini-2.5-flash
estimated cost: $0.0009
no execution. add --execute to run.

$ frugal compare "write tests for this function"
recipe path: gpt-4.1-mini   → $0.0021 → 5 tests, all pass
baseline:    gpt-4o          → $0.0182 → 5 tests, all pass
→ recipe path is 88% cheaper, same correctness.
```

### MCP server

```bash
$ frugal mcp install
detected: Claude Desktop, Cursor
write 'frugal' MCP server to both configs? [Y/n] y
✓ wrote ~/Library/Application Support/Claude/claude_desktop_config.json
✓ wrote ~/.cursor/mcp.json
note: for Claude Code, run: claude mcp add frugal -- frugal mcp serve

$ frugal mcp serve
mcp ready on stdio. tools: frugal__search
```

In an MCP-aware client, `frugal__search` appears in the tool picker. The
agent decides *when* to search; Frugal decides *which provider* (cheapest
that clears the use-case eval) and bills it to your provider key.

## Toolchain components

Frugal's reach is bigger than what's wired today. Honesty over aspiration —
each component carries one of three labels:

- **Shipping** — in the binary today, routes live traffic, covered by tests and the benchmark.
- **Stubbed** — API/schema slot exists so caller code doesn't break when it lands; no executor wired.
- **Planned** — on the roadmap; no schema or executor yet.

| Component | What it is | Status |
|---|---|---|
| Hosted chat models | OpenAI / Anthropic / Google chat completions, routed per use case | **Shipping (internal)** — used by recipe chat steps; exposed as `frugal__chat` MCP tool in Phase 2 |
| Search API | Routed cheapest web search provider per use case (Tavily, Serper) | **Shipping** — `frugal__search` MCP tool |
| Local models | Local-server-backed chat for the cheap path on summarize / code / extract | Planned (Phase 3) |
| Browser / fetch | Headless fetch + readable extraction for webpage tasks | Planned (Phase 2) |
| Content extraction | URL → clean text routed across multiple providers | Planned (Phase 2) |
| Semantic cache | Hash + similarity cache for repeated / near-duplicate questions | Planned (Phase 2) |
| Embeddings & vector search | Retrieval over user-supplied corpora to displace long-context | Planned (Phase 3) |
| Code execution | Sandboxed Python / shell for math, data-shaping, verification | Planned (Phase 3) |
| Multi-step agent | Cheapest plan-and-call loop when one tool alone isn't enough (`frugal run --agent`) | Planned (Phase 3) |

Each component ships only when the eval harness has data saying a toolchain
built around it clears the quality bar. The canonical source of truth for
component status is [`frugal-strategy-v6.md`](./frugal-strategy-v6.md) §6 —
if this table disagrees, the strategy doc wins.

## How it works

```
$ frugal run "summarize this repo"
       │
       ├─ classifies task → recipe ID + quality tier
       ├─ loads recipe step list from config/use_cases/<id>.yaml
       ├─ for each step:
       │     ├─ tool step  → call MCP tool (loopback) → routed provider → result
       │     └─ chat step  → router picks cheapest model meeting threshold
       ├─ binds {task}, {step1.results}, … into subsequent step inputs
       └─ prints final result + cost + recipe path
```

The same recipe runs whether invoked via the CLI (`frugal run`) or via an
MCP client calling `frugal__chat` with the matching use-case hint. The
router, eval scoring, and pricing data are shared between both surfaces.

## How much does it actually save?

Run the recipe-bake-off benchmark on your own keys:

```bash
frugal bench --quality balanced --out bench.md
```

Frugal runs every problem in `config/workloads/starter.yaml` twice — once
through the routed recipe (cheapest path that clears your quality bar) and
once pinned to the baseline model. Each output is scored against a
deterministic scorer (exact match, substring, JSON schema, or numeric
tolerance):

```
# starter (quality=balanced, baseline=gpt-4o)

Problems: 20  ·  Frugal pass: 90.0%  ·  Baseline pass: 95.0%  ·  Δ: -5.0pp
Cost: frugal $0.0043  ·  baseline $0.0118  ·  savings 63.6%
```

No judge LLMs, no simulated numbers — these are the bytes your provider
billed you for. See [`config/workloads/starter.yaml`](./config/workloads/starter.yaml)
for the problem set and [`config/CAPABILITIES.md`](./config/CAPABILITIES.md)
for the methodology behind the capability scores the router uses.

## Quality tiers

Each recipe ships three quality tiers. Default is `balanced`. Override per
invocation:

```bash
frugal run "extract the speakers from this PDF" --quality cost
frugal route "plan a database migration" --quality high
```

| Tier | Behavior |
|---|---|
| `high` | Top-tier steps only. Planners, complex reasoning, novel code. |
| `balanced` | Default. Best cost-quality tradeoff; right ~80% of the time. |
| `cost` | Cheapest viable recipe path. Classification, extraction, simple summaries. |

## Install

```bash
curl -fsSL https://frugal.sh/install | bash
```

Downloads a single signed binary (~10MB), verifies it with `cosign` if
present, detects your API keys, adds `frugal` to your `PATH`. Pin a version
with `FRUGAL_VERSION=v1.0.0 curl -fsSL … | bash`.

### From source

```bash
git clone https://github.com/brainsparker/frugal.git
cd frugal
make build
```

## Supported providers

Model pricing synced from [models.dev](https://models.dev) on every startup.

| Provider | Used as |
|---|---|
| OpenAI | Hosted chat (GPT-4o, GPT-4o-mini, GPT-4.1, GPT-4.1-mini, GPT-4.1-nano) |
| Anthropic | Hosted chat (Claude Opus 4, Claude Sonnet 4, Claude Haiku 3.5) |
| Google | Hosted chat (Gemini 2.5 Pro, Gemini 2.5 Flash, Gemini 2.0 Flash) |
| Tavily | Routed search (LLM-tuned recall) |
| Serper | Routed search (cheap per-call) |

Set the matching env vars: `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`,
`GOOGLE_API_KEY`, `TAVILY_API_KEY`, `SERPER_API_KEY`. Frugal registers
whichever providers have keys.

Add models or providers by editing `~/.frugal/config/models.yaml`.

## Free vs paid

| Component | Source | Status | Pricing |
|---|---|---|---|
| CLI + MCP server | OSS (BUSL 1.1 → Apache 2.0) | Phase 1 | Free |
| Receiver *(planned)* | OSS (BUSL 1.1 → Apache 2.0) | Plan | Free for self-host |
| Dashboard *(planned)* | Proprietary | Plan | Paid; ships alongside the receiver |

Free is BYOK (your own provider keys), no account. The paid tier is a
**ZDR enterprise dashboard**: customer self-hosts the receiver + dashboard
inside their own VPC, Frugal-the-company never receives a single byte of
their data. Aimed at regulated industries and security-conscious teams.
Full positioning in [`frugal-strategy-v6.md`](./frugal-strategy-v6.md).

## v1.0 — breaking change from v0.x

Frugal v1.0 removes the OpenAI-compatible HTTP proxy (`frugal serve`) and
the command-wrap mode (`frugal <cmd>`). A toolchain product can't be
honestly delivered behind a chat-completions wire spec — search, browser,
code-exec, and cache have no chat-completion shape.

If you used Frugal as `OPENAI_BASE_URL=…` in front of an OpenAI client,
migrate to one of:

- **`frugal mcp install`** if your client speaks MCP (Claude Code, Cursor,
  Claude Desktop, custom MCP host). The `frugal__chat` MCP tool (Phase 2)
  replaces the proxy's routed chat completions.
- **`frugal run "<task>"`** for scripts and one-shot dispatch.
- **Pin to v0.x** if you specifically need OpenAI-compatible routing.
  v0.x is in maintenance mode (security fixes only); routing logic and
  pricing tables remain shared.

See [`frugal-strategy-v6.md`](./frugal-strategy-v6.md) §10 for the full
rationale.

## Development

```bash
make build    # build binary
make test     # run tests
make release  # cross-compile for all platforms
```

## Security

Release artifacts are cosign-signed (keyless, GitHub OIDC). The installer
verifies `SHA256SUMS` and, when `cosign` is present, the signature before
moving the binary into place. See [`SECURITY.md`](./SECURITY.md) for the
threat model and disclosure process.

## License

[Business Source License 1.1](./LICENSE) — self-hosting and internal
production use are permitted; offering Frugal as a competing hosted routing
service is not. Each version converts to Apache 2.0 four years after release.
See the [BUSL FAQ](./LICENSE-BUSL-FAQ.md) for a plain-English summary.

## Contributing

Issues, bug reports, and PRs are welcome. See [`CONTRIBUTING.md`](./CONTRIBUTING.md)
for how the project is structured, the testing expectations, and the commit
style.
