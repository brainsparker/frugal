# frugal

**Open-source AI toolchain cost optimizer. Route every prompt to the cheapest model + toolchain bundle that won't compromise quality.**

[frugal.sh](https://frugal.sh) · [GitHub](https://github.com/brainsparker/frugal)

No account. No code changes. One command.

```bash
curl -fsSL https://frugal.sh/install | bash
```

```bash
frugal python my_app.py
```

Frugal wraps any command with a local OpenAI-compatible proxy, classifies each
request by use case, and routes to the cheapest (model + toolchain) bundle that
clears your quality bar. Your app doesn't change. Your API keys stay local.

---

## Why Frugal isn't just another model router

A model alone isn't the product — the use case is. Legal research wants a strong
reasoner *and* good web search. Code work wants a code-aware model *and* targeted
retrieval. Structured extraction wants the smallest model that passes the schema
and nothing else.

Frugal's wedge: classify each prompt's use case, then route to the cheapest
**bundle** (model × toolchain) that clears the quality bar for that use case.
Every bundle is grounded in the eval harness — routing isn't opinion, it's what
the data says wins for your workload.

| Concept | What it is |
|---|---|
| **Capability** | A primitive: chat, web search, reranking, content extraction, browser. |
| **Use case** | A named class of work (`research-synthesis`, `code-dev`, `factual-qa`, `structured-extraction`). Ships with a labeled benchmark workload. |
| **Bundle** | The recommended (capability → provider) map for a use case at a quality tier. |

## How much does it actually save?

Run the benchmark on your own keys:

```bash
frugal bench --quality balanced --out bench.md
```

Frugal runs every problem in `config/workloads/starter.yaml` twice — once
through the router (cheapest model that clears your quality bar) and once
pinned to the baseline model. Each output is scored against a deterministic
scorer (exact match, substring, JSON schema, or numeric tolerance):

```
# starter (quality=balanced, baseline=gpt-4o)

Problems: 20  ·  Frugal pass: 90.0%  ·  Baseline pass: 95.0%  ·  Δ: -5.0pp
Cost: frugal $0.0043  ·  baseline $0.0118  ·  savings 63.6%
```

No judge LLMs, no simulated numbers — these are the bytes your provider billed
you for. See [`config/workloads/starter.yaml`](./config/workloads/starter.yaml)
for the problem set and [`config/CAPABILITIES.md`](./config/CAPABILITIES.md)
for the methodology behind the capability scores the router uses.

## Use cases routed today

The starter catalog ships four use cases in [`config/use_cases/`](./config/use_cases/).
Set the `X-Frugal-Use-Case` header and the request routes to that bundle.

| Use case | What it matches | Balanced tier chat model |
|---|---|---|
| `research-synthesis` | Long-form multi-source research | Claude Sonnet 4 |
| `code-dev` | Code generation, debugging, review | GPT-4.1 mini |
| `factual-qa` | Short factual lookups, trivia | GPT-4.1 nano |
| `structured-extraction` | Free text → JSON | Gemini 2.5 Flash |

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "X-Frugal-Use-Case: research-synthesis" \
  -H "X-Frugal-Quality: balanced" \
  -H "Content-Type: application/json" \
  -d '{"model":"auto","messages":[{"role":"user","content":"..."}]}'
```

Inspect the bundles directly:

```bash
curl http://localhost:8080/v1/bundles                           # every use case
curl http://localhost:8080/v1/bundles/research-synthesis        # default balanced tier
curl http://localhost:8080/v1/bundles/research-synthesis?quality=high
```

Bundles today are **curated** (`source: curated` in the YAML) with `as_of` dates
tracking when each was last refreshed. When `X-Frugal-Use-Case` is absent, the
classifier/router path runs as before — use-case routing is opt-in.

## Toolchain capabilities

| Capability | Status |
|---|---|
| Chat (model routing) | **Shipping** |
| Web search | **Shipping** (Ring 1) |
| Reranking | **Shipping** (Ring 1) |
| Content extraction | Next |
| Browser use | Next |

Capabilities roll out one at a time, each gated by the eval harness. Bundles
declare the full toolchain for a use case, not just the chat model.

---

## How it works

Frugal wraps any command, spins up a lightweight local proxy, sets
`OPENAI_BASE_URL` to point at it, and routes every request through the router.

```
frugal python app.py
       │
       ├─ starts proxy on a free port
       ├─ injects OPENAI_BASE_URL into your command's environment
       ├─ classifies each request (use case + required capabilities)
       ├─ picks the cheapest (model + toolchain) bundle that clears the bar
       ├─ calls bundled tools — web search, rerank, extract, browse — as needed
       └─ shuts down proxy when your command exits
```

You're paying for capability you don't use on 60–80% of your AI calls, and when
you *do* need capability, a bare chat model is rarely the answer.

### What the classifier detects

| Signal | How |
|---|---|
| Code | Regex for code blocks, `function`/`def`/`class` keywords |
| Math | LaTeX patterns, equation keywords |
| Reasoning depth | System prompt complexity, conversation length |
| Output format | JSON mode, tool/function calling |
| Domain | Keyword detection (coding, creative, analysis, math) |
| Use case | Explicit header (`X-Frugal-Use-Case`) or inferred from signals above |

Signals combine into a complexity score and a capability set. The router picks
the cheapest bundle that clears every threshold.

## Install

```bash
curl -fsSL https://frugal.sh/install | bash
```

Downloads a single signed binary (~10MB), verifies it with `cosign` if present,
detects your API keys, adds `frugal` to your `PATH`. Pin a version with
`FRUGAL_VERSION=v0.2.1 curl -fsSL … | bash`.

### From source

```bash
git clone https://github.com/brainsparker/frugal.git
cd frugal
make build
```

## Usage

### Wrap any command

```bash
frugal python my_app.py
frugal node server.js
frugal go run ./cmd/myservice
frugal pytest tests/
frugal bash -c 'curl https://api.openai.com/v1/...'
```

Frugal picks a free port, starts the proxy, sets `OPENAI_BASE_URL` in your
command's environment, and cleans up on exit. Works with any OpenAI-compatible
SDK — Python, Node, Go, Rust, curl.

### Run as a server

```bash
frugal serve
# or just: frugal (with no arguments)

export OPENAI_BASE_URL=http://localhost:8080/v1
```

Optional hardening timeouts (Go duration syntax):

- `FRUGAL_READ_HEADER_TIMEOUT` (default `5s`)
- `FRUGAL_READ_TIMEOUT` (default `15s`)
- `FRUGAL_WRITE_TIMEOUT` (default `120s`)
- `FRUGAL_IDLE_TIMEOUT` (default `60s`)
- `FRUGAL_MAX_HEADER_BYTES` (default `1048576`)

### Auth, rate limits, logging (serve mode)

| Env var | Default | Purpose |
|---|---|---|
| `FRUGAL_ADDR` | `127.0.0.1:8080` | Listen address. Non-loopback binds require `FRUGAL_AUTH_TOKEN` or `FRUGAL_ALLOW_UNAUTH=1`. |
| `FRUGAL_AUTH_TOKEN` | *(unset)* | Shared bearer token. When set, every `/v1/*` call must send `Authorization: Bearer $FRUGAL_AUTH_TOKEN`. |
| `FRUGAL_ALLOW_UNAUTH` | `0` | Escape hatch: setting to `1` allows unauthenticated binds on non-loopback. |
| `FRUGAL_RPS` | `30` | Global token-bucket rate in requests/sec. `0` disables. |
| `FRUGAL_BURST` | `60` | Token-bucket burst capacity. Clamped to `>= FRUGAL_RPS`. |
| `FRUGAL_MAX_COST_PER_REQUEST_USD` | `1.00` | Reject requests whose routing-time estimate exceeds this cap. Pinned requests bypass. `0` disables. |
| `FRUGAL_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error`. |
| `FRUGAL_LOG_FORMAT` | `text` | `text` for human-readable, `json` for structured ingestion. |
| `FRUGAL_DECISION_BUFFER` | `1000` | Capacity of the async routing-decision ring buffer. |

Prometheus metrics are served at `/metrics` behind the same auth as `/v1/*`.
All responses carry `X-Request-ID`; generate one client-side if you want to
correlate logs across your app and the proxy.

### Quality tiers

Control cost vs. quality per request:

```python
headers = {"X-Frugal-Quality": "cost"}  # high | balanced | cost
```

| Tier | Behavior |
|---|---|
| `high` | Top-tier bundles only. Planners, complex reasoning, novel code. |
| `balanced` | Default. Best cost-quality tradeoff; right ~80% of the time. |
| `cost` | Cheapest viable bundle. Classification, extraction, simple summaries. |

### Model pinning

Skip routing for specific calls:

```python
response = client.chat.completions.create(
    model="gpt-4o-mini",  # goes straight to this model
    messages=[...]
)
```

### Fallback chains

```python
headers = {"X-Frugal-Fallback": "gpt-4o,claude-sonnet-4-20250514,gemini-2.5-flash"}
```

If the routed model errors, Frugal walks the chain. At most the first 3
fallback models are attempted, to bound latency and cost. Duplicate entries
and duplicates of the routed model are skipped.

## Supported models

Pricing synced from [models.dev](https://models.dev) on every startup.

| Provider | Models |
|---|---|
| OpenAI | GPT-4o, GPT-4o-mini, GPT-4.1, GPT-4.1-mini, GPT-4.1-nano |
| Anthropic | Claude Opus 4, Claude Sonnet 4, Claude Haiku 3.5 |
| Google | Gemini 2.5 Pro, Gemini 2.5 Flash, Gemini 2.0 Flash |

Set `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, and/or `GOOGLE_API_KEY`. Frugal
registers whichever providers have keys.

Add models by editing `~/.frugal/config/models.yaml`.

## Commands

| Command | What it does |
|---|---|
| `frugal <cmd>` | Wrap a command with the routing proxy |
| `frugal serve` | Run the proxy as a persistent server |
| `frugal sync` | Update model pricing from models.dev |
| `frugal bench` | Run the benchmark workload and print a cost/quality report |

## API

When running as a server, Frugal exposes an OpenAI-compatible API plus a few
Frugal-specific endpoints:

| Endpoint | Description |
|---|---|
| `POST /v1/chat/completions` | Routed chat (streaming + non-streaming) |
| `GET /v1/models` | List available models |
| `GET /v1/bundles` | List every use case and its bundle |
| `GET /v1/bundles/{use-case}` | Bundle for a use case at a given quality tier |
| `GET /v1/routing/explain` | Last routing decision — model, toolchain, why |
| `GET /health` | Health check |
| `GET /metrics` | Prometheus metrics (same auth as `/v1/*`) |

## Development

```bash
make build    # build binary
make test     # run tests
make run      # build + run server
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
