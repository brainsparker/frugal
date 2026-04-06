# frugal.sh

**Drop-in LLM proxy that routes every request to the cheapest model that won't compromise quality.**

One line. Same responses. Lower bill.

```bash
export OPENAI_BASE_URL=http://localhost:8080/v1
```

That's it. Your existing OpenAI SDK calls now route through Frugal. No SDK. No code changes. No new response format.

---

## How it works

Every request hits a lightweight query classifier before it reaches a model. The classifier analyzes complexity, domain, required reasoning depth, and output format — then routes to the lowest-cost model that meets the quality bar for that specific request.

A creative brainstorm doesn't need `o3`. A simple extraction doesn't need `claude-opus-4-6`. You're paying for capability you don't use on 60-80% of your LLM calls. Frugal fixes that.

### The routing stack

```
Your app → frugal → query classifier → model selection → LLM → response
                          ↓
                    eval taxonomy
                    (config/models.yaml)
```

**Query classifier** — Rule-based heuristics that classify each request across dimensions: reasoning complexity, code presence, math, output structure, context window needs.

**Eval taxonomy** — Model capabilities and costs defined in `config/models.yaml`. Every model is scored per-category so routing decisions are grounded in measured performance.

**Model selection** — Matches the classified request to the cheapest model that exceeds the quality threshold for that category.

## Quickstart

### 1. Set API keys

```bash
export OPENAI_API_KEY="sk-..."
export ANTHROPIC_API_KEY="sk-ant-..."
export GOOGLE_API_KEY="..."
```

Frugal registers providers based on which API keys are present. No key = provider skipped.

### 2. Run the proxy

```bash
make build
./bin/frugal
# frugal listening on :8080
```

### 3. Use normally

```python
from openai import OpenAI

client = OpenAI(
    api_key="sk-...",
    base_url="http://localhost:8080/v1"
)

response = client.chat.completions.create(
    model="auto",              # let Frugal choose
    messages=[{"role": "user", "content": "Summarize this PDF"}],
    temperature=0.3
)
```

Pass `model="auto"` to let Frugal route. Or pass a specific model name (e.g., `gpt-4o`, `claude-sonnet-4-20250514`) to pin to that model directly.

## Configuration

### Quality thresholds

```python
headers = {
    "X-Frugal-Quality": "high"    # high | balanced | cost
}
```

| Threshold | Behavior |
|-----------|----------|
| `high` | Routes to top-tier models. Best quality. |
| `balanced` | Default. Best cost-quality tradeoff. |
| `cost` | Aggressively routes to cheapest viable model. |

### Model pinning

```python
# Frugal passes through to the exact model
response = client.chat.completions.create(
    model="claude-sonnet-4-20250514",
    messages=[...]
)
```

### Fallback chains

```python
headers = {
    "X-Frugal-Fallback": "claude-sonnet-4-20250514,gpt-4o,gemini-2.5-flash"
}
```

If the routed model is down or errors, Frugal walks the fallback chain automatically.

### Model taxonomy

Model capabilities and costs are configured in `config/models.yaml`. Add or update models by editing this file — no code changes needed.

## API reference

Frugal is fully OpenAI-compatible:

- `POST /v1/chat/completions` — routed chat (streaming + non-streaming)
- `GET /v1/models` — list available models across all providers
- `GET /v1/routing/explain` — returns the last routing decision (debug)
- `GET /health` — health check

### Response headers

Every response includes:
- `X-Frugal-Model` — the model that handled the request
- `X-Frugal-Provider` — the provider that handled the request

## Supported providers

| Provider | Models |
|----------|--------|
| OpenAI | GPT-4o, GPT-4o-mini, GPT-4.1, GPT-4.1-mini, GPT-4.1-nano |
| Anthropic | Claude Opus 4, Claude Sonnet 4, Claude Haiku 3.5 |
| Google | Gemini 2.5 Pro, Gemini 2.5 Flash, Gemini 2.0 Flash |

## Development

```bash
make build    # build binary to bin/frugal
make test     # run all tests
make run      # build and run
make clean    # remove build artifacts
```

## Architecture

```
cmd/frugal/main.go           # entrypoint, wiring
internal/
  classifier/                 # rule-based query classification
  router/                     # cost-optimized model selection
  provider/                   # provider interface + implementations
    openai/                   # OpenAI API
    anthropic/                # Anthropic Messages API
    google/                   # Gemini API
  proxy/                      # HTTP handlers, middleware, SSE streaming
  types/                      # shared types (OpenAI-compatible)
config/models.yaml            # model taxonomy (capabilities, costs)
```

## License

MIT
