# frugal.sh Positioning v2

> **Superseded by [`frugal-strategy-v3.md`](./frugal-strategy-v3.md)** as of 2026-05-05.
> Kept for historical reference; do not edit.

---


## One-liner
frugal.sh helps AI apps choose the cheapest path to a good answer.

## Product framing
frugal.sh is an agent routing + eval layer that optimizes cost, latency, and answer quality.

### Router
- Chooses cheap model, premium model, search, or no tool
- Routes by prompt type, freshness needs, complexity, and confidence

### Benchmark
- Compares frugal routing vs baseline strategies
- Example baseline: always premium model + search

### Dashboard
- Shows savings, quality delta, latency, and tool-use accuracy

## Killer demo
Run 50 prompts through two systems:

1. Baseline: always use best model + search
2. frugal.sh: cheap model when possible, search only when needed, escalate only when confidence is low

Output claim format:
- “frugal.sh reduced cost by X% while preserving Y% answer quality.”

## ICP and role alignment
Target companies:
- Cloudflare, OpenAI, Anthropic, Databricks, Vercel, Notion, Perplexity, LangChain, Replit, Cursor

Target roles:
- PM for AI platform, developer platform, agents, model routing, evals, or PLG growth

## Landing page copy
Hero:
- Use the cheapest model that can do the job.

Subhead:
- frugal.sh routes AI requests across models and tools based on cost, latency, and quality, so teams can ship agents without lighting money on fire.

CTA:
- Run your first benchmark

## README opener
AI apps shouldn’t default to the most expensive model for every request. frugal.sh benchmarks and routes requests across models and tools to find the cheapest path to a good answer.

## First post draft
I’ve been working on frugal.sh, a small experiment around AI cost optimization.

The basic idea:

Most AI apps are overpaying because they route too much work to frontier models.

But not every request needs the best model.

Some requests need:
- a cheap model
- a better model
- search
- no tool at all

So I’m building a simple router that benchmarks each path across cost, latency, and quality.

The goal is simple:

Find the cheapest path to a good answer.

This feels like one of the next big layers in AI infrastructure. Not just better models, but better decisions about when to use them.
