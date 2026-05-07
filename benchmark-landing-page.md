# frugal.sh/benchmark

> **Superseded by [`frugal-strategy-v3.md`](./frugal-strategy-v3.md)** as of 2026-05-05.
> The live benchmark page lives at [`docs/benchmark/index.html`](./docs/benchmark/index.html); strategy and positioning are captured in v3.
> Kept for historical reference; do not edit.

---


## Find the cheapest path to a good answer

AI apps don’t fail because models are bad.  
They fail because they choose the wrong tools.

The frugal.sh benchmark measures how AI systems decide:
- when to use search
- which model to use
- when to escalate to something more expensive
- when to do nothing at all

Then it shows the tradeoffs across cost, latency, and answer quality.

---

## What this is

The frugal benchmark is a lightweight evaluation system for agent decision-making.

It runs the same set of prompts through different strategies and compares:
- **Baseline:** Always use the best model + search
- **Frugal:** Use the cheapest viable option, escalate only when needed

You get a clear answer to a simple question:

**Are you overpaying for your AI stack?**

---

## Why this matters

Most teams optimize for model quality.  
Almost no one optimizes for decision quality.

That leads to:
- overusing expensive models
- unnecessary tool calls
- higher latency
- unpredictable behavior in production

The result is simple:

**you spend more money for the same answers.**

---

## What gets measured

Every run produces a structured output across four dimensions:

### 1) Answer Quality
- LLM-as-judge scoring
- task completion accuracy
- hallucination detection (basic)

### 2) Cost
- per-request cost
- total run cost
- cost vs baseline comparison

### 3) Latency
- time to first token
- total response time
- impact of tool calls

### 4) Tool Use Accuracy
- did the agent use search when it should?
- did it avoid tools when unnecessary?
- did it select the right model?

---

## How it works

### Step 1: Define prompts
A curated set of tasks:
- factual (requires fresh info → search)
- reasoning (no search needed)
- hybrid (requires both)

### Step 2: Run strategies
**Baseline**
- always uses top-tier model
- always uses search

**Frugal Router**
- starts with a cheap model
- decides if search is needed
- escalates only when confidence is low

### Step 3: Compare outcomes
You get side-by-side results:

| Strategy | Quality | Cost | Latency | Tool Accuracy |
|---|---|---|---|---|
| Baseline | High | $$$ | Slow | Medium |
| Frugal | High | $ | Faster | Higher |

---

## Example insight

In early tests:
- ~40–60% of prompts did not need the best model
- search was overused in simple reasoning tasks
- routing reduced cost significantly with minimal quality loss

The takeaway:

**Most AI systems are overbuilt for the average request.**

---

## What this unlocks

With a simple routing + eval layer, teams can:
- reduce model spend without sacrificing quality
- improve latency for end users
- make agent behavior more predictable
- move from static pipelines to adaptive systems

---

## Who this is for

- teams building AI products with APIs
- engineers working on agents or tool use
- product managers thinking about cost + UX tradeoffs
- anyone shipping LLM-powered workflows at scale

---

## Why frugal.sh exists

We’re entering a world where:
- models are getting better and cheaper
- tools are everywhere
- agents are making decisions in real time

The next bottleneck isn’t capability.

**It’s choosing the right path for each request.**

---

## Run the benchmark

Coming soon:
- CLI to run your own prompts
- configurable routing strategies
- exportable reports

---

## Follow along

This is an early exploration into:
- agent decision systems
- model routing
- cost-aware AI infrastructure

If you’re working on similar problems, reach out.
