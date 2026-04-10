# frugal

**Open-source LLM proxy that routes every request to the cheapest model that won't compromise quality.**

No account. No code changes. One command.

```bash
curl -fsSL https://frugal.sh/install | sh
```

```bash
frugal python my_app.py
```

That's it. Frugal starts a local proxy, injects `OPENAI_BASE_URL`, runs your command, and shuts down when it exits. Your app doesn't change. Your API keys stay local. Your bill drops 40-70%.

---

## How it works

Frugal wraps any command. It spins up a lightweight local proxy, sets `OPENAI_BASE_URL` to point at it, and routes every LLM request to the cheapest model that won't degrade quality.

```
frugal python app.py
       │
       ├─ starts proxy on a free port
       ├─ injects OPENAI_BASE_URL into your command's environment
       ├─ classifies each request (complexity, domain, capabilities)
       ├─ routes to cheapest model that clears the quality bar
       └─ shuts down proxy when your command exits
```

A creative brainstorm doesn't need `o3`. A simple extraction doesn't need `claude-opus`. You're paying for capability you don't use on 60-80% of your LLM calls.

### What the classifier detects

| Signal | How |
|--------|-----|
| Code | Regex for code blocks, `function`/`def`/`class` keywords |
| Math | LaTeX patterns, equation keywords |
| Reasoning depth | System prompt complexity, conversation length |
| Output format | JSON mode, tool/function calling |
| Domain | Keyword detection (coding, creative, analysis, math) |

These signals combine into a complexity score. The router picks the cheapest model that exceeds the quality threshold for that score.

## Install

```bash
curl -fsSL https://frugal.sh/install | sh
```

Downloads a single binary (~10MB), detects your API keys, adds `frugal` to your PATH.

### From source

```bash
git clone https://github.com/frugalsh/frugal.git
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

Frugal picks a free port, starts the proxy, sets `OPENAI_BASE_URL` in your command's environment, and cleans up on exit. Works with any OpenAI-compatible SDK — Python, Node, Go, Rust, curl.

### Run as a server

If you want a persistent proxy (e.g., shared across terminals or in Docker):

```bash
frugal serve
# or just: frugal (with no arguments)
```

Then set the env var yourself:

```bash
export OPENAI_BASE_URL=http://localhost:8080/v1
```

Optional server hardening knobs (safe defaults are applied automatically):

| Env var | Default | Purpose |
|---------|---------|---------|
| `FRUGAL_HTTP_READ_HEADER_TIMEOUT` | `5s` | Limit time to read request headers |
| `FRUGAL_HTTP_READ_TIMEOUT` | `15s` | Limit total request read time |
| `FRUGAL_HTTP_WRITE_TIMEOUT` | `30s` | Limit response write time |
| `FRUGAL_HTTP_IDLE_TIMEOUT` | `60s` | Limit keep-alive idle connections |
| `FRUGAL_HTTP_MAX_HEADER_BYTES` | `1048576` | Cap request header size (1 MiB) |

### Quality thresholds

Control cost vs. quality per request:

```python
headers = {"X-Frugal-Quality": "cost"}  # high | balanced | cost
```

| Threshold | Behavior |
|-----------|----------|
| `high` | Top-tier models only. |
| `balanced` | Default. Best cost-quality tradeoff. |
| `cost` | Cheapest viable model. Maximum savings. |

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

If the routed model errors, Frugal walks the chain.

## Supported models

Pricing synced from [models.dev](https://models.dev) on every startup.

| Provider | Models |
|----------|--------|
| OpenAI | GPT-4o, GPT-4o-mini, GPT-4.1, GPT-4.1-mini, GPT-4.1-nano |
| Anthropic | Claude Opus 4, Claude Sonnet 4, Claude Haiku 3.5 |
| Google | Gemini 2.5 Pro, Gemini 2.5 Flash, Gemini 2.0 Flash |

Set `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, and/or `GOOGLE_API_KEY`. Frugal registers whichever providers have keys.

Add models by editing `~/.frugal/config/models.yaml`.

## Commands

| Command | What it does |
|---------|-------------|
| `frugal <cmd>` | Wrap a command with the routing proxy |
| `frugal serve` | Run the proxy as a persistent server |
| `frugal sync` | Update model pricing from models.dev |

## API

When running as a server, Frugal exposes an OpenAI-compatible API:

| Endpoint | Description |
|----------|-------------|
| `POST /v1/chat/completions` | Routed chat (streaming + non-streaming) |
| `GET /v1/models` | List available models |
| `GET /v1/routing/explain` | Last routing decision |
| `GET /health` | Health check |

## Development

```bash
make build    # build binary
make test     # run tests
make run      # build + run server
make release  # cross-compile for all platforms
```

## License

MIT
