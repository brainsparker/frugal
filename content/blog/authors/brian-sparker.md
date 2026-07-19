---
name: Brian Sparker
role: Creator of Frugal
title: "Brian Sparker — creator of Frugal | frugal.sh"
description: "Brian Sparker is the creator of Frugal, the MCP server that routes AI agent tool calls free-first. He writes about provider routing, AI tool costs, and agent infrastructure."
links:
  - label: GitHub
    url: https://github.com/brainsparker
  - label: sparker.co
    url: https://sparker.co
expertise:
  - Provider routing
  - AI tool-call economics
  - MCP servers
  - Agent infrastructure
  - Model selection
---
Brian Sparker is the creator of [Frugal](/), an MCP server that routes AI
agent tool calls — search, extract, browse — down a price ladder: free and
local providers first, paid providers only when nothing cheaper returns a
result. Every response carries `provider_used` and `cost_usd`, so the
routing decision is auditable in the agent's own trace.

Frugal came out of a simple observation: the same web search costs $0 on
Wikipedia or Marginalia, $0.001 on Serper, and $0.005 on You.com — and
agents pick providers by what's wired in, not by price. For search-heavy
workloads, that gap is most of the bill. Building the router meant living
inside the details this blog covers: fallback chains, zero-hit semantics,
latency budgets, MCP transports, and the ledger math of what an agent run
actually costs.

He writes here about provider routing, AI tool-call economics, model
selection, and the operational work of keeping agents reliable — aimed at
engineers and small teams building on AI APIs without a platform team to
lean on.

<!-- PLACEHOLDER: prior roles, companies, and years of experience.
     Add only verifiable biographical details — e.g. "Before Frugal,
     Brian spent N years doing X at Y." Leave out until confirmed. -->

<!-- PLACEHOLDER: education, talks, podcasts, or press mentions, with
     links, once available. -->

You can reach him through [GitHub](https://github.com/brainsparker) or
[sparker.co](https://sparker.co).
