# Frugal product strategy (v5)

> **Superseded by [`frugal-strategy-v6.md`](./frugal-strategy-v6.md) as of 2026-05-18.** Kept for auditability.
>
> Original supersession: replaces [`frugal-strategy-v4.md`](./frugal-strategy-v4.md).
> As of 2026-05-18.

## 1. Thesis

**Stop picking models. Pick the cheapest toolchain that completes the job.**

Most AI tasks are over-routed. Frontier model when a local model plus search would have worked. Long-context stuffing when retrieval would have answered. Agent loops when a single tool call would have. The expensive part isn't the model — it's the wrong path.

Frugal is **the routed-tool layer for agent stacks**. A CLI (`frugal run`, `frugal route`, `frugal compare`) that dispatches tasks to the cheapest reliable toolchain, and an MCP server (`frugal mcp serve`) that any agent client — Claude Code, Claude Desktop, Cursor, custom — can install to get the same routing behind their existing tool-use loop.

Decision quality matters more than model quality at this point in the curve, and the cheapest viable toolchain for each task is knowable from data, not opinion. **The router IS the benchmark.** The same scorer that decides "did the agent pick the right tool on this prompt?" in CI is the routing engine running inside `frugal mcp serve` on every live request. Bench wins ship; nothing routes that hasn't earned its spot.

## 2. The wedge

The model-router space is crowded (OpenRouter, LiteLLM, Martian, Helicone, Portkey). Routed-search MCP servers also already exist — `web-search-plus-mcp` and `kindly-web-search-mcp-server` both route across 5–7 search providers. We are not first to "one MCP tool, many providers."

Frugal's defensibility comes from four things together:

1. **Breadth.** One router across search + extract + browse + code-exec + embeddings + cache + chat — not just search. The bundled view is the product.
2. **Cost-first routing decision quality.** Every routing pick is backed by an eval scorecard, refreshed monthly. Not an opinion; not a leaderboard average.
3. **Go single-binary distribution.** One self-contained `frugal` binary, signed releases, `frugal mcp install` writes the configs. No Python venv, no `npx`, no daemon to manage.
4. **CLI as a second integration surface.** `frugal run "task"` works for scripts, humans, CI, Makefiles, ad-hoc — places where installing an agent client isn't the right move. Same routing engine the MCP server uses.

The pitch isn't *"we route models"* — it's *"we route work."*

## 3. Audience / ICP

**Primary persona: local-first AI builders.**

- Indie hackers and OSS maintainers tired of "$200 last week, what happened" provider bills.
- Homelab and local-first builders running their own model servers who want a single router that uses the local model when it's good enough and a hosted API only when it isn't.
- Agent builders fighting over-routed pipelines — one cheap tool call instead of an agent loop.
- Claude Code / Cursor / Claude Desktop users with custom MCP servers in their stack already.
- Small teams who don't want a control plane, a SaaS account, or vendor lock-in.

The MCP integration surface tightens this fit, not loosens it. The people running custom MCP servers in their agent stack today are exactly the audience this product is for.

**Paid tier (downstream).** Three concentric rings, ship in order, all folding the local-first audience as their volume layer:

1. **Inner ring (v1):** AI teams in regulated industries (fintech, healthcare, gov) with explicit "no data leaves our VPC" requirements. Frugal as a way to consolidate provider spend visibility *and* satisfy security review in one motion.
2. **Middle ring (v1.5):** compliance-driven buyers wanting SOC 2 / HIPAA / GDPR posture as a procurement checkbox. Same architecture, layered compliance package.
3. **Outer ring (v2):** engineering-platform teams in mid-market companies who want an org-wide savings dashboard without ZDR pressure. Wait until v2 — by then the dashboard is mature, SSO/RBAC exists, segment is buyable on convenience.

## 4. The recipe model

Recipes are the product. A recipe is a deterministic step list — one or more tool calls and/or model calls — that solves a category of task at the cheapest reliable cost. The recipe is what `frugal run` executes, what `frugal route` previews, what `frugal compare` benchmarks against a baseline.

The public-facing artifact is the recipe table — task → cheapest reliable path. Status tags are honest, using the [§6 component status vocabulary](#6-component-status):

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

Five recipes ship today: `research-synthesis`, `code-dev`, `factual-qa`, `structured-extraction` (migrated to the new schema in Phase 1 PR 2), and `fresh-facts` (Phase 1 PR 4, alongside the search tool).

## 5. Product surfaces

**Two primary surfaces, one routing engine underneath.**

| Surface | Status | Audience | What it is |
|---|---|---|---|
| **CLI (`frugal run/route/compare`)** | Phase 1 | Scripts, humans, CI, Makefiles, ad-hoc | Deterministic recipe dispatch from a task description. Same routing engine the MCP server uses (dogfooded via internal MCP loopback). |
| **MCP server (`frugal mcp serve`)** | Phase 1 | Agents — Claude Code, Claude Desktop, Cursor, custom | Routed tools exposed via the MCP protocol. `frugal__search` first; `frugal__extract`, `frugal__browse`, `frugal__cache_lookup`, `frugal__chat` follow. `frugal mcp install` writes the right config for each client. |
| **Public benchmark** | Page live, illustrative sample only | Anyone evaluating Frugal | Static page at `frugal.sh/benchmark`. Plan: monthly-refreshed aggregate from opt-in telemetry, plus the reproducible-by-construction sample run that's there today. Reframes from "model bake-off" to "recipe bake-off." |
| **Paid dashboard** | Plan | ZDR-grade enterprises | Customer-hosted dashboard fed by their own self-hosted receiver. Frugal-the-company never sees their data. |

The OpenAI-compatible HTTP proxy that anchored v3/v4 is **removed in v1.0**. See §10 for the breaking-change rationale.

## 6. Component status

The router's reach is bigger than what's wired today. Honesty over aspiration — every component carries one of three labels:

- **Shipping** — in the binary today, routes live traffic, covered by tests and the benchmark.
- **Stubbed** — API/schema slot exists so caller code doesn't break when it lands; no executor wired.
- **Planned** — on the roadmap; no schema or executor yet.

| Component | What it is | Status |
|---|---|---|
| Hosted chat models | OpenAI / Anthropic / Google chat completions, routed per use case | **Shipping (internal)** — recipe chat steps; `frugal__chat` MCP tool lands Phase 2 |
| Search API | Routed cheapest web search provider per use case (Tavily, Serper) | **Shipping** — `frugal__search` MCP tool (Phase 1 PR 4) |
| Local models | Local-server-backed chat for the cheap path on summarize / code / extract | Planned (Phase 3) |
| Browser / fetch | Headless fetch + readable extraction for webpage tasks | Planned (Phase 2) |
| Content extraction | URL → clean text routed across multiple providers | Planned (Phase 2) |
| Semantic cache | Hash + similarity cache for repeated / near-duplicate questions | Planned (Phase 2) |
| Embeddings & vector search | Retrieval over user-supplied corpora to displace long-context | Planned (Phase 3) |
| Code execution | Sandboxed Python / shell for math, data-shaping, verification | Planned (Phase 3) |
| Multi-step agent | Cheapest plan-and-call loop when one tool alone isn't enough (`frugal run --agent`) | Planned (Phase 3) |

This matrix is the single source of truth. The README and homepage mirror it; if they disagree, this doc wins.

## 7. Telemetry data plane

The bridge between the free CLI/MCP server and the public benchmark, and the channel paid customers stream their own usage on for the dashboard.

**Submission shape.** The binary maintains in-memory counters per `(recipe, step, provider)` tuple. Once an hour it freezes a rollup to `~/.frugal/telemetry/pending-<timestamp>.json`. Once a day the file is uploaded; the local copy is kept 30 days for audit. `frugal telemetry preview` prints the next pending rollup so users can inspect before sending.

**Payload contents** — per `(recipe, step, provider)` tuple:

- request count, input/output token totals (where applicable), cost USD total
- latency p50 / p95
- tool-use accuracy (correct calls / expected calls)
- error counts by class (`rate_limit`, `context_length`, `invalid_api_key`)
- instance_id (random UUIDv4, generated at first telemetry-on, stored at `~/.frugal/instance_id`)
- `frugal_version`, hour-bucket period

**Explicitly excluded:** prompts, responses, message content of any kind, tool inputs/outputs, provider keys, search queries, URLs fetched, source IP, hostname, OS username, hardware fingerprint, error message bodies, exact request timestamps.

**Free path vs paid path:**

| Mode | Endpoint | Auth | Per-instance retention |
|---|---|---|---|
| Free + `FRUGAL_TELEMETRY=1` | `https://telemetry.frugal.sh` (default) | None | None — aggregated immediately on receipt, instance row dropped |
| Paid + `FRUGAL_API_KEY=…` | `https://telemetry.frugal.sh` (or override) | Bearer | 90 days, then aggregated and dropped |
| Paid + self-hosted (ZDR) | `FRUGAL_TELEMETRY_ENDPOINT=…` | Per customer | Per customer |

The free path's no-per-instance-retention rule is the quiet-but-important one: contributing telemetry doesn't create a record of *your* instance anywhere on Frugal infra. Only the aggregate survives.

**Public benchmark refresh: monthly or ad-hoc.** No live route, no client-side fetch of a live JSON. Maintainer pulls the receiver's aggregate, regenerates `BENCHMARKS.md` and the headline numbers in `docs/benchmark/index.html`. A live pane gets layered on once volume justifies it.

## 8. OSS / paid split

| Component | Source | Status | Pricing |
|---|---|---|---|
| CLI + MCP server | OSS (BUSL 1.1 → Apache 2.0) | Phase 1 | Free |
| Receiver | OSS (BUSL 1.1 → Apache 2.0) | Plan, separate repo (`brainsparker/frugal-telemetry`) | Free for self-host |
| Dashboard | Proprietary | Plan | Paid; ships alongside the receiver |
| Support contract | n/a | Plan | Bundled with dashboard license |

The PostHog analog with one explicit deviation: PostHog open-sources its dashboard from day one. We're keeping ours proprietary at v1 to compress time-to-first-paid-customer; OSS-ing a dashboard properly is a multi-month polish project. Path forward: open-source the dashboard at v2, layer enterprise features (SSO, RBAC, longer retention, multi-instance grouping) on top as the new monetization vector.

The data plane (binary + receiver) is OSS top-to-bottom — the part of the stack any privacy-oriented buyer will demand to audit. The viewer is closed; that's accepted in the market (Datadog, Grafana Enterprise, Sentry's UI).

## 9. Paid tier v1 — ZDR enterprise

The buyer: regulated industries (fintech, healthcare, gov), enterprises with strict security review, AI teams whose security posture forbids "any data leaving our VPC."

The product:

- **Customer self-hosts the receiver + dashboard inside their VPC.** The receiver is OSS; the dashboard is a proprietary container we ship them.
- **`FRUGAL_TELEMETRY_ENDPOINT` points at their receiver, not ours.** Frugal-the-company never receives a single byte of paid customer data.
- **ZDR is automatic by architecture, not by policy.** No promises to keep, no audit to fail, no incident scenario where data could leak from us — there's no "us" in the data path.

The contract:

- License + support contract for the proprietary dashboard.
- Optional compliance package on top: DPA, SOC 2 attestation, HIPAA BAA, GDPR DPA — standard procurement-checklist items, layered as paid SKUs once the first customer requests each.

The sales motion: license sale, not managed service. The buyer takes operational complexity in exchange for total data isolation. That trade is the value prop. Buyers who want managed convenience without ZDR are a v2 product — explicitly out of scope today.

## 10. v1.0 breaking change

**Frugal v1.0 ships without the OpenAI-compatible HTTP proxy.** The `frugal serve` and `frugal <cmd>` wrap entry points are removed. The product surface inverts to CLI + MCP server.

**Why.** A toolchain product can't be honestly delivered behind an OpenAI chat-completions wire spec. Search, browser, code-exec, embeddings, and semantic cache have no chat-completion shape. We could fake it (encode tool calls as tool-calling messages, route them inside the proxy) but the result is a half-implementation of MCP wrapped in a different wire format — confusing for users, harder to integrate with the agent stacks where the audience already lives. Better to commit to the protocol that fits.

**Who breaks.** Anyone who pointed `OPENAI_BASE_URL` at a Frugal proxy. There is no upgrade path that keeps the OpenAI-compatible surface — the migration is structural:

- If you used Frugal to route chat completions and your client speaks MCP (Claude Code, Cursor, custom MCP host): `frugal mcp install`, then call `frugal__chat` (lands Phase 2) from the agent.
- If you used Frugal in scripts and need a one-shot dispatch: `frugal run "<task>"`.
- If you need OpenAI-compatible routing specifically: stay on v0.x and pin `FRUGAL_VERSION=v0.2.x` in the installer. v0.x is in maintenance mode (security fixes only); the routing logic and pricing tables remain shared.

This is the only breaking change in v1.0. Capability scores, pricing tables, eval methodology, telemetry shape, license trajectory, and the recipe model all carry forward.

## 11. Roadmap

Three threads. Each ships only when the eval supports it.

### a. Phase 1 — clean break to CLI + MCP

Ships as six sequenced PRs:

1. **Demolition + scaffolds.** Delete the proxy, write v5 strategy doc, rewrite CLI dispatch with stubs for new subcommands.
2. **Recipe schema + loader.** New `internal/recipe/` package; migrate the four use-case YAMLs.
3. **MCP server scaffold.** `frugal mcp serve` (stdio + Streamable HTTP) using `github.com/modelcontextprotocol/go-sdk`. Empty tool registry.
4. **First tool: `frugal__search`.** Tavily + Serper provider drivers. Search component promotes Stubbed → Shipping. New `fresh-facts` recipe.
5. **`frugal run` + `frugal route`.** Deterministic recipe dispatcher executes through internal MCP loopback.
6. **`frugal mcp install`** (Claude Desktop, Cursor, Claude Code). Stretch: `frugal compare` for side-by-side cost+quality vs baseline.

### b. Phase 2 — Tool breadth + `frugal__chat`

- `frugal__extract` (URL → clean text; routed across providers).
- `frugal__browse` (headless fetch + readable extraction).
- `frugal__cache_lookup` (sqlite-backed semantic cache).
- `frugal__chat` (chat completion as an MCP tool — turns the dead proxy's routing into a tool any MCP client can call).
- `frugal compare` if dropped from Phase 1.
- `.mcpb` bundle for Claude Desktop one-click install.
- MCP registry publication.

### c. Phase 3 — Local + advanced

- `frugal__exec` (sandboxed code execution).
- `frugal__embed` (embeddings as a routed tool).
- Local model executor (Ollama / LM Studio detection + routing).
- `frugal run --agent` mode (agent-loop alternative to the deterministic dispatcher, for open-ended tasks not covered by recipes).
- ZDR enterprise dashboard (§9 carries forward unchanged).

## 12. TBD

**Q8 — Pricing model and free→paid transition flow.** Open. Resolve before first paid customer:

- Pricing structure (per-seat? per-instance? flat license? volume-based?)
- Account creation flow (web signup? sales contact? GitHub OAuth?)
- License key distribution (`FRUGAL_API_KEY` env var generation and rotation)
- Upgrade path from free instance to paid (does the same `instance_id` carry over? what about historical data the receiver doesn't have?)

Does not block anything in §1–§11. Becomes load-bearing the moment a real prospect wants to buy.

---

*This document is the canonical positioning for Frugal as of 2026-05-18. Changes go through revision (v5 → v6) rather than in-place edits, so the conversation history stays auditable.*
