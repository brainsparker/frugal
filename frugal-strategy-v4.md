# Frugal product strategy (v4)

> **Superseded by [`frugal-strategy-v5.md`](./frugal-strategy-v5.md) as of 2026-05-18.** Kept for auditability.
>
> Original supersession: replaces [`frugal-strategy-v3.md`](./frugal-strategy-v3.md).
> As of 2026-05-18.

## 1. Thesis

**Stop picking models. Pick the cheapest toolchain that completes the job.**

Most AI tasks are over-routed. Frontier model when a local model plus search would have worked. Long-context stuffing when retrieval would have answered. Agent loops when a single tool call would have. The expensive part isn't the model — it's the wrong path.

Frugal's bet is that **decision quality matters more than model quality** at this point in the curve, and that the cheapest viable *toolchain* for each request is knowable from data, not opinion.

**The router IS the benchmark.** The same scorer that decides "did the agent pick the right tool on this prompt?" in CI is the routing engine running inside `frugal serve` on every live request. Bench wins ship; nothing routes that hasn't earned its spot.

## 2. The wedge

The model-router space is already crowded — OpenRouter, LiteLLM, Martian, Helicone, Portkey. It becomes a price/latency benchmark fight, and the differentiation is thin.

Toolchain routing is a defensible position. Frugal routes across:

- local models
- hosted models
- search APIs
- browser tools
- code execution
- extraction tools
- embeddings / vector search
- semantic cache
- multi-step agent workflows

The pitch isn't *"we route models"* — it's *"we route work."* A model alone isn't the product; the cheapest reliable combination of capabilities for the task at hand is.

## 3. Audience / ICP

**Primary persona: local-first AI builders.**

- Indie hackers and OSS maintainers tired of "$200 last week, what happened" provider bills.
- Homelab and local-first builders running their own model servers who want a single proxy that uses the local model when it's good enough and a hosted API only when it isn't.
- Agent builders fighting over-routed pipelines — one cheap tool call instead of an agent loop.
- Small teams who don't want a control plane, a SaaS account, or vendor lock-in.

These users are cost-conscious, technical, skeptical of waste, and already running their own infra. They are the natural audience for a router that picks across toolchains rather than across hosted models — they already have *more than one path* to try.

**Paid tier (downstream).** Three concentric rings, ship in order, all of which fold the local-first audience as their volume layer:

1. **Inner ring (v1):** AI teams in regulated industries (fintech, healthcare, gov) with explicit "no data leaves our VPC" requirements. Frugal as a way to consolidate provider spend visibility *and* satisfy security review in one motion.
2. **Middle ring (v1.5):** compliance-driven buyers wanting SOC 2 / HIPAA / GDPR posture as a procurement checkbox. Same architecture, layered compliance package.
3. **Outer ring (v2):** engineering-platform teams in mid-market companies who want an org-wide savings dashboard without ZDR pressure. Wait until v2 — by then the dashboard is mature, SSO/RBAC exists, segment is buyable on convenience.

## 4. The recipe model

The public-facing artifact bridging abstract "use cases" and concrete tasks is a recipe table — task → cheapest reliable default path. It is honest about what ships today vs what's planned, using the [§6 component status vocabulary](#6-component-status).

| Task | Cheapest reliable path | Status |
|---|---|---|
| Summarize a document | Local small model | Planned (local exec) |
| Fresh facts (news, prices, schedules) | Search + small hosted model | Stubbed (search slot; no executor) |
| Extract from a webpage | Browser/fetch + local model | Planned |
| Complex reasoning (planning, hard math, novel code) | Hosted frontier model | **Shipping** |
| Code generation, refactors | Local code model → hosted fallback | Partial (hosted shipping; local planned) |
| Repeated / near-duplicate questions | Semantic cache | Planned |
| Multi-source research | Search + rerank, hosted-if-needed | Stubbed (chat-only stand-in ships today as `research-synthesis`) |
| Structured extraction (text → JSON) | Smallest JSON-mode-reliable hosted model | **Shipping** |

Four of these recipes ship today as named use cases in `config/use_cases/*.yaml` — `research-synthesis`, `code-dev`, `factual-qa`, `structured-extraction`. The rest are planned and will land as their executors do (see §6).

A "recipe" is the user-facing framing; a "use case" is the runtime artifact. They are intentionally close-but-not-identical: the recipe table speaks to *what people are doing*, the use-case YAML defines *what the router does*. Recipes can land in copy before the use case ships; the use case ships once the eval supports the cheapest path.

## 5. Product surfaces

Three of them. Different audiences, different cadences, same data plane.

| Surface | Status | Audience | What it is |
|---|---|---|---|
| **Free proxy** | Shipping | Individual devs, small teams, local-first builders | Single binary, BYOK, no account. Routes every prompt to the cheapest toolchain that clears the quality bar. |
| **Public benchmark** | Page live, illustrative sample only | Anyone evaluating Frugal | Static page at `frugal.sh/benchmark`. Plan: monthly-refreshed aggregate from opt-in telemetry, plus the reproducible-by-construction sample run that's there today. |
| **Paid dashboard** | Plan | ZDR-grade enterprises | Customer-hosted dashboard fed by their own self-hosted receiver. Frugal-the-company never sees their data. |

## 6. Component status

The router's reach is bigger than what's wired today. Honesty over aspiration — every component carries one of three labels:

- **Shipping** — in the binary today, routes live traffic, covered by tests and the benchmark.
- **Stubbed** — API/schema slot exists so caller code doesn't break when it lands; no executor wired.
- **Planned** — on the roadmap; no schema or executor yet.

| Component | What it is | Status |
|---|---|---|
| Hosted chat models | OpenAI / Anthropic / Google chat completions, routed per use case | **Shipping** |
| Local models | Local-server-backed chat for the cheap path on summarize / code / extract | Planned |
| Search API | Cheapest web search provider per use case | Stubbed |
| Browser / fetch | Headless fetch + readable extraction for webpage tasks | Planned |
| Code execution | Sandboxed Python / shell for math, data-shaping, verification | Planned |
| Embeddings & vector search | Retrieval over user-supplied corpora to displace long-context | Planned |
| Semantic cache | Hash + similarity cache for repeated / near-duplicate questions | Planned |
| Multi-step agent | Cheapest plan-and-call loop when one tool alone isn't enough | Planned |

This matrix is the single source of truth. The README and homepage mirror it; if they disagree, this doc wins.

## 7. The OSS / paid split

| Component | Source | Status | Pricing |
|---|---|---|---|
| Proxy | OSS (BUSL 1.1 → Apache 2.0) | Shipping | Free |
| Receiver | OSS (BUSL 1.1 → Apache 2.0) | Plan, separate repo (`brainsparker/frugal-telemetry`) | Free for self-host |
| Dashboard | Proprietary | Plan | Paid; ships alongside the receiver |
| Support contract | n/a | Plan | Bundled with dashboard license |

The PostHog analog with one explicit deviation: PostHog open-sources its dashboard from day one. We're keeping ours proprietary at v1 to compress time-to-first-paid-customer; OSS-ing a dashboard properly is a multi-month polish project. Path forward: open-source the dashboard at v2, layer enterprise features (SSO, RBAC, longer retention, multi-instance grouping) on top as the new monetization vector. Same shape PostHog landed on, one cycle behind.

The data plane (proxy + receiver) is OSS top-to-bottom — the part of the stack any privacy-oriented buyer will demand to audit. The viewer is closed; that's accepted in the market (Datadog, Grafana Enterprise, Sentry's UI).

## 8. Free tier mechanics

- **BYOK.** The user provides their own provider keys (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GOOGLE_API_KEY`). Frugal issues nothing. There is no account to create.
- **Default state: pure local proxy.** `frugal serve` binds 127.0.0.1 by default and refuses to bind elsewhere without `FRUGAL_AUTH_TOKEN`. No data leaves the machine. The "no control plane" promise on the homepage is structural, not aspirational.
- **Optional opt-in telemetry** via `FRUGAL_TELEMETRY=1`. The only mechanism by which any data ever leaves the machine. Anonymized aggregates only; payload spec in §9.
- **Give-to-get for opt-in users:** your data improves the public benchmark, which improves the routing logic in your next release. Same loop as Homebrew analytics or PostHog telemetry-back-to-PostHog. **There is no free dashboard.** The proxy IS the value; users see savings on their provider bill directly. Free users are paying for nothing — there's no value exchange beyond the OSS proxy itself, and we're honest about that.

## 9. Telemetry data plane

The bridge between the free proxy and the public benchmark, and the channel paid customers stream their own usage on for the dashboard.

**Submission shape.** The proxy maintains in-memory counters (already does this for Prometheus `/metrics`). Once an hour it freezes a rollup to `~/.frugal/telemetry/pending-<timestamp>.json`. Once a day the file is uploaded; the local copy is kept 30 days for audit. `frugal telemetry preview` prints the next pending rollup so users can inspect before sending.

**Payload contents** — per `(use-case, quality, model, provider)` tuple:

- request count, input/output token totals, cost USD total
- latency p50 / p95
- tool-use accuracy (correct calls / expected calls)
- error counts by class (`rate_limit`, `context_length`, `invalid_api_key`)
- instance_id (random UUIDv4, generated at first telemetry-on, stored at `~/.frugal/instance_id`)
- `frugal_version`, hour-bucket period

**Explicitly excluded:** prompts, responses, message content of any kind, provider keys, `FRUGAL_AUTH_TOKEN`, headers other than known `X-Frugal-Use-Case` values (unknown values bucket to `"custom"`), source IP, hostname, OS username, hardware fingerprint, error message bodies, exact request timestamps.

**Free path vs paid path:**

| Mode | Endpoint | Auth | Per-instance retention |
|---|---|---|---|
| Free + `FRUGAL_TELEMETRY=1` | `https://telemetry.frugal.sh` (default) | None | None — aggregated immediately on receipt, instance row dropped |
| Paid + `FRUGAL_API_KEY=…` | `https://telemetry.frugal.sh` (or override) | Bearer | 90 days, then aggregated and dropped |
| Paid + self-hosted (ZDR) | `FRUGAL_TELEMETRY_ENDPOINT=…` | Per customer | Per customer |

The free path's no-per-instance-retention rule is the quiet-but-important one: contributing telemetry doesn't create a record of *your* instance anywhere on Frugal infra. Only the aggregate survives.

**Public benchmark refresh: monthly or ad-hoc.** No live route, no client-side fetch of a live JSON. Maintainer pulls the receiver's aggregate, regenerates `BENCHMARKS.md` and the headline numbers in `docs/benchmark/index.html`. A live pane gets layered on once volume justifies it — at early-adopter scale, daily or hourly refresh would just publish noisy averages from a handful of instances.

## 10. Paid tier v1 — ZDR enterprise

The buyer: regulated industries (fintech, healthcare, gov), enterprises with strict security review, AI teams whose security posture forbids "any data leaving our VPC."

The product:

- **Customer self-hosts the receiver + dashboard inside their VPC.** The receiver is OSS; the dashboard is a proprietary container we ship them.
- **`FRUGAL_TELEMETRY_ENDPOINT` points at their receiver, not ours.** Frugal-the-company never receives a single byte of paid customer data.
- **ZDR is automatic by architecture, not by policy.** No promises to keep, no audit to fail, no incident scenario where data could leak from us — there's no "us" in the data path.

The contract:

- License + support contract for the proprietary dashboard.
- Optional compliance package on top: DPA, SOC 2 attestation, HIPAA BAA, GDPR DPA — standard procurement-checklist items, layered as paid SKUs once the first customer requests each.

The sales motion: license sale, not managed service. The buyer takes operational complexity in exchange for total data isolation. That trade is the value prop. Buyers who want managed convenience without ZDR are a v2 product (single-tenant managed) — explicitly out of scope today.

## 11. Roadmap

Three threads, sequenced. Each ships only when the eval supports it.

### a. Toolchain expansion

Component status lives in [§6](#6-component-status). Sequencing rationale:

- **Web search** ships first — already has stubbed slots in every use-case YAML, executor wiring is the smallest delta. Initial integration plan: cheapest provider per eval, picked from a small candidate set.
- **Browser / fetch** ships next — unlocks the "extract from a webpage" recipe, paired with local model on the cheap path.
- **Local models** lights up the largest swath of the recipe table (summarize, code, extract). Lands once the eval has data on how often a local model is the cheapest reliable path.
- **Reranking, content extraction, semantic cache, code execution, embeddings, multi-step agent** — later rings, in order of recipe-table impact. Each gates on its eval before it touches the live router — same pattern as the chat tier.

### b. Benchmark scoring evolution

Today's ranking signals: **cost** (primary), **tool-selection correctness** (secondary). Latency and answer quality are captured but not yet ranking inputs.

Future: cost / latency / quality become a Pareto frontier exposed as a per-request control. Build the benchmark scoring out first; route based on the slider once the scoring is trusted across enough live data.

### c. Personalized routing

The slider becomes a paid-dashboard feature where users tune cost vs latency vs quality preferences per use-case. Lives in the dashboard, not in headers — paid tier already has account state.

Examples of the personalization the slider enables:

- *"Classify intent: cheapest, don't care about quality."*
- *"Research synthesis: don't care about latency, let it think."*

Per-user, per-intent. Frugal as a routing layer that adapts to the workload, not a one-size-fits-all averaging engine.

## 12. TBD

**Q8 — Pricing model and free→paid transition flow.** Open. Resolve before first paid customer:

- Pricing structure (per-seat? per-instance? flat license? volume-based?)
- Account creation flow (web signup? sales contact? GitHub OAuth?)
- License key distribution (`FRUGAL_API_KEY` env var generation and rotation)
- Upgrade path from free instance to paid (does the same `instance_id` carry over? what about historical data the receiver doesn't have?)

Does not block anything in §1–§11. Becomes load-bearing the moment a real prospect wants to buy.

---

*This document is the canonical positioning for Frugal as of 2026-05-18. Changes go through revision (v4 → v5) rather than in-place edits, so the conversation history stays auditable.*
