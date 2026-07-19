---
title: "The MCP ecosystem in 2026: what's actually getting used"
slug: mcp-ecosystem-2026
date: 2026-05-05T14:25
description: "An operator's read on the MCP ecosystem in 2026: what stuck, which transports won, where discovery is heading, and what's still messy."
excerpt: "Forget the spec tour. This is what MCP looks like from the inside of a server that runs every day: the parts that stuck and the parts that didn't."
cluster: mcp-ecosystem
type: analysis
keyword: "MCP ecosystem"
related:
  - provider-routing-for-ai-agents
  - ai-agents-provider-outages
---
The MCP ecosystem in 2026 looks nothing like the spec suggested it would. I say that as someone who ships and operates an MCP server daily — Frugal, which routes agent tool calls down a price ladder — and who reads the protocol from the unglamorous side: logs, transports, and the tool descriptions that models actually see. The spec describes a rich surface of tools, resources, prompts, and sampling. Usage describes something much narrower and, honestly, better.

This is an operator's inventory: what stuck, what the plumbing settled on, where discovery is heading, and the three areas that are still a mess.

## What stuck: tools, stdio, and doing one thing

**Tools won. Resources mostly didn't.** MCP offers several primitives, but in practice the ecosystem collapsed onto one: the tool call. A model sees a named function with a description and a schema, fills the arguments, gets a result. Resources — the protocol's mechanism for exposing browsable context — see a fraction of that use, and prompts and sampling less still. The reason is unsentimental: tools map exactly onto what current models are trained to do. Function calling was already the native gesture; MCP standardized its wire format. Everything in the protocol that required clients to build new UX around it lagged; everything that slotted into an existing model behavior spread. When I look at what actually gets exercised on my own server, it's the three tools — search, extract, browse — and essentially nothing else. I suspect most server authors would report the same shape.

**Stdio servers are the workhorse.** The deployment story that dominates real usage is almost embarrassingly simple: a local process, spawned by the client, speaking JSON-RPC over stdin and stdout. No ports, no TLS, no auth handshake — process isolation is the security model, and your keys never transit a network. For the single-developer, single-machine case that still describes most agent work, stdio is close to unbeatable, and its friction profile explains a lot about which servers get adopted. A server you can wire in with one line of config gets tried; a server that needs infrastructure gets bookmarked.

**Single-purpose servers beat platforms.** The servers that get real traction do one thing: query this database, search the web, control this browser. The kitchen-sink servers — twenty tools spanning five domains — mostly don't, and there's a mechanical reason beyond taste: every tool definition you expose is context the model must carry and a choice it can get wrong. Tool selection accuracy degrades as the menu grows. A focused server is a better prompt. This mirrors an argument I've made about [provider routing](/blog/provider-routing-for-ai-agents/) — the useful abstraction is a capability, narrowly defined, with the messy vendor variety hidden underneath it, not a sprawling surface that pushes the complexity up to the model.

## Transports: two survivors, one clear division of labor

The transport story consolidated. Stdio for local, Streamable HTTP for remote, and the older SSE arrangement fading into compatibility-shim status. That's the right outcome — Streamable HTTP's single-endpoint design is dramatically easier to operate and to put standard infrastructure in front of than its predecessor was.

What the spec doesn't say, and operators learn quickly, is that the transports imply different trust models, not just different sockets. A stdio server inherits its caller's environment and trust. An HTTP server is a network service, full stop, and needs everything network services need. Frugal ships both, and the HTTP side carries bearer-token auth, per-IP rate limiting, and a Prometheus `/metrics` endpoint — none of which the protocol requires and all of which running the thing does. The gap between "speaks the transport" and "safe to expose" is where most of the remaining engineering lives. It's also where reliability practice belongs: a remote MCP server is one more provider that [will eventually be down](/blog/ai-agents-provider-outages/), and clients should treat it accordingly.

## Discovery is embryonic, and .well-known is the interesting bet

How does an agent find servers? In 2026, the honest answer is: a human pastes a config block. Registries and curated directories exist and help humans browse, but agent-legible discovery — where software finds and evaluates a capability without a person in the loop — barely exists.

The early surface worth watching is `.well-known`. The pattern of a service self-describing its MCP endpoint at a predictable URL is the same move that made TLS certificates automatable, and it's the first step toward an agent resolving "I need search" into a live endpoint on its own. I'd bet on it precisely because it's boring: no consortium, no token, just a convention and a JSON file.

But discovery without evaluation is a trap. The moment agents can find arbitrary servers, they need a basis for choosing among them — capability, trust, and price. Today a client connects to whatever it was configured with and pays whatever that server's upstream costs, invisibly. An ecosystem of discoverable servers with no price signal reproduces, at the protocol layer, the exact pattern where providers get picked by availability rather than cost. It's why I think per-call cost metadata — a `cost_usd` on results — belongs in server responses as a convention, well before any registry standardizes anything.

## What's still messy

**Auth is the biggest gap.** Stdio dodges the question; remote MCP has to answer it, and the answers remain uneven. OAuth-based flows exist in the spec and work when implemented well, but implementation quality varies enormously, and many production deployments quietly settle for static bearer tokens because the full flow is more machinery than a small server team wants to own. Fine for BYOK single-tenant setups like mine. Not fine as the ecosystem's long-term answer for multi-tenant servers holding real credentials.

**Security variance is wide and invisible.** Two servers that look identical in a client's config can differ wildly in what they'll do with a hostile input: one sanitizes what it fetches, another happily returns attacker-controlled text that the model will read as instructions. Prompt injection through tool results is the ecosystem's most underpriced risk, and nothing in the protocol surface tells you which servers take it seriously. Reputation is currently carried by word of mouth, which does not scale.

**Tool descriptions are load-bearing and mostly bad.** The description string on a tool is not documentation — it's the interface the model reasons over. Vague descriptions produce wrong tool choices and malformed arguments, and the failure is silent: the call "works," the agent just used it badly. Writing these well is closer to prompt engineering than to API docs, and almost nobody treats it that way yet. It's the cheapest quality win available to any server author, which is exactly why it's everywhere neglected.

## Where this leaves an operator

The MCP ecosystem in 2026 is in its useful adolescence: the core gesture — tools over a standard wire — has clearly stuck, the transports have settled, and the hard unsolved problems are the eternal ones of auth, trust, and discovery rather than anything exotic. If you're building a server, the playbook the ecosystem has already voted for is short: expose few tools, describe them like the model's life depends on it, ship stdio first, and treat HTTP as a production service when you get there. If you're running agents against servers, inventory what you're connected to and what each call costs you — the protocol won't tell you, so your logs have to.

I'll keep unpacking the pieces — transports, hardening, evaluation checklists — on the [MCP ecosystem](/blog/topics/mcp-ecosystem/) hub. The protocol is settling. The operations are just getting started.
