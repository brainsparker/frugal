# Frugal product strategy (v3)

> **Superseded by [`frugal-strategy-v4.md`](./frugal-strategy-v4.md) as of 2026-05-18.** Kept for auditability.
>
> Original supersession: replaces [`frugal-positioning-v2.md`](./frugal-positioning-v2.md) and [`benchmark-landing-page.md`](./benchmark-landing-page.md).
> As of 2026-05-05.

## 1. Thesis

**Find the cheapest path to a good answer.**

AI apps don't fail because models are bad. They fail because they choose the wrong tools — overpaying for capability they don't need on 60–80% of requests. Frugal's bet is that **decision quality matters more than model quality** at this point in the curve, and that the cheapest viable bundle for each request is knowable from data, not opinion.

**The router IS the benchmark.** The same scorer that decides "did the agent pick the right tool on this prompt?" in CI is the routing engine running inside `frugal serve` on every live request. Bench wins ship; nothing routes that hasn't earned its spot.

## 2. Product surfaces

Three of them. Different audiences, different cadences, same data plane.

| Surface | Status | Audience | What it is |
|---|---|---|---|
| **Free proxy** | Shipping | Individual devs, small teams | Single binary, BYOK, no account. Routes every prompt to the cheapest bundle that clears the quality bar. |
| **Public benchmark** | Page live, illustrative sample only | Anyone evaluating Frugal | Static page at `frugal.sh/benchmark`. v3 plan: monthly-refreshed aggregate from opt-in telemetry, plus the reproducible-by-construction sample run that's there today. |
| **Paid dashboard** | Plan | ZDR-grade enterprises | Customer-hosted dashboard fed by their own self-hosted receiver. Frugal-the-company never sees their data. |

## 3. The OSS / paid split

| Component | Source | Status | Pricing |
|---|---|---|---|
| Proxy | OSS (BUSL 1.1 → Apache 2.0) | Shipping | Free |
| Receiver | OSS (BUSL 1.1 → Apache 2.0) | Plan, separate repo (`brainsparker/frugal-telemetry`) | Free for self-host |
| Dashboard | Proprietary | Plan | Paid; ships alongside the receiver |
| Support contract | n/a | Plan | Bundled with dashboard license |

The PostHog analog with one explicit deviation: PostHog open-sources its dashboard from day one. We're keeping ours proprietary at v1 to compress time-to-first-paid-customer; OSS-ing a dashboard properly is a multi-month polish project. Path forward: open-source the dashboard at v2, layer enterprise features (SSO, RBAC, longer retention, multi-instance grouping) on top as the new monetization vector. Same shape PostHog landed on, just one cycle behind.

The data plane (proxy + receiver) is OSS top-to-bottom — the part of the stack that any privacy-oriented buyer will demand to audit. The viewer is closed; that's accepted in the market (Datadog, Grafana Enterprise, Sentry's UI).

## 4. Free tier mechanics

- **BYOK.** The user provides their own provider keys (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GOOGLE_API_KEY`). Frugal issues nothing. There is no account to create.
- **Default state: pure local proxy.** `frugal serve` binds 127.0.0.1 by default and refuses to bind elsewhere without `FRUGAL_AUTH_TOKEN`. No data leaves the machine. The "no control plane" promise on the homepage is structural, not aspirational.
- **Optional opt-in telemetry** via `FRUGAL_TELEMETRY=1`. The only mechanism by which any data ever leaves the machine. Anonymized aggregates only; payload spec in §5.
- **Give-to-get for opt-in users:** your data improves the public benchmark, which improves the routing logic in your next release. Same loop as Homebrew analytics or PostHog telemetry-back-to-PostHog. **There is no free dashboard.** The proxy IS the value; users see savings on their provider bill directly. Free users are paying for nothing — there's no value exchange beyond the OSS proxy itself, and we're honest about that.

## 5. Telemetry data plane (the bridge)

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

## 6. Paid tier v1 — ZDR enterprise

The buyer: regulated industries (fintech, healthcare, gov), enterprises with strict security review, AI teams whose security posture forbids "any data leaving our VPC."

The product:
- **Customer self-hosts the receiver + dashboard inside their VPC.** The receiver is OSS; the dashboard is a proprietary container we ship them.
- **`FRUGAL_TELEMETRY_ENDPOINT` points at their receiver, not ours.** Frugal-the-company never receives a single byte of paid customer data.
- **ZDR is automatic by architecture, not by policy.** No promises to keep, no audit to fail, no incident scenario where data could leak from us — there's no "us" in the data path.

The contract:
- License + support contract for the proprietary dashboard.
- Optional compliance package on top: DPA, SOC 2 attestation, HIPAA BAA, GDPR DPA — standard procurement-checklist items, layered as paid SKUs once the first customer requests each.

The sales motion: license sale, not managed service. The buyer takes operational complexity in exchange for total data isolation. That trade is the value prop. Buyers who want managed convenience without ZDR are a v2 product (single-tenant managed) — explicitly out of scope today.

## 7. ICP

**Free tier.** Individual devs and small teams running LLM workloads who care about cost. The "drop your AI bill 40–70%" pitch on the homepage. Volume audience.

**Paid tier.** Three concentric rings, ship in order:

1. **Inner ring (early customers, v1):** AI teams in regulated industries with explicit "no data leaves our VPC" requirements. Selling Frugal as a way to consolidate their own provider spend visibility *and* satisfy the security team in one motion.
2. **Middle ring (v1.5):** Compliance-driven buyers who don't have hard ZDR requirements but want SOC 2 / HIPAA / GDPR posture as a procurement checkbox. Same architecture, layered compliance package.
3. **Outer ring (v2):** Engineering platform teams in mid-market companies who want a savings dashboard for their org but don't have the buyer-side security pressure that drives ring 1. Wait until v2 — by then the dashboard will be more mature, SSO/RBAC will exist, and the segment is buyable on convenience rather than security.

## 8. Roadmap

Three threads, sequenced. Each ships only when the eval supports it.

### a. Toolchain expansion

Web search first. Browse next. Each capability gates on its eval before it touches the live router — same pattern as the chat tier.

- **Web search** (Ring 1b, next). Initial integration: **you.com search**. Eval-gate decides which provider routes per use-case.
- **Browse** (Ring 1c). Initial integration: **browserless**. Same eval-gate pattern.
- **Reranking, content extraction** — later rings; design space already in `config/use_cases/*.yaml` schema.

### b. Benchmark scoring evolution

Today's ranking signals: **cost** (primary), **tool-selection correctness** (secondary). Latency and answer quality are captured but not yet ranking inputs.

Future: cost / latency / quality become a Pareto frontier exposed as a per-request control. Build the benchmark scoring out first; route based on the slider once the scoring is trusted across enough live data.

### c. Personalized routing

The slider becomes a paid-dashboard feature where users tune cost vs latency vs quality preferences per use-case. Lives in the dashboard, not in headers — paid tier already has account state.

Examples of the personalization the slider enables:
- *"Classify intent: cheapest, don't care about quality."*
- *"Research synthesis: don't care about latency, let it think."*

Per-user, per-intent. Frugal as a routing layer that adapts to the workload, not a one-size-fits-all averaging engine.

## TBD

**Q8 — Pricing model and free→paid transition flow.** Open. Resolve before first paid customer:

- Pricing structure (per-seat? per-instance? flat license? volume-based?)
- Account creation flow (web signup? sales contact? GitHub OAuth?)
- License key distribution (`FRUGAL_API_KEY` env var generation and rotation)
- Upgrade path from free instance to paid (does the same `instance_id` carry over? what about historical data the receiver doesn't have?)

Does not block anything in §1–§7. Becomes load-bearing the moment a real prospect wants to buy.

---

*This document is the canonical positioning for Frugal as of 2026-05-05. Changes go through revision (v3 → v4) rather than in-place edits, so the conversation history stays auditable.*
