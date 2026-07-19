---
title: "MCP servers are the new CLI tools"
slug: mcp-servers-new-cli-tools
date: 2026-08-27T13:55
description: "MCP servers are this decade's CLI tools: small, single-purpose, composable by an agent instead of a shell. The analogy predicts where MCP goes next."
excerpt: "Unix won with small tools composed by a shell. Swap the shell for an agent and the pattern repeats — except pipes were free, and tool calls have a meter."
cluster: mcp-ecosystem
type: strategic
keyword: "MCP servers"
related:
  - mcp-ecosystem-2026
  - byok-agent-tooling
  - evaluate-mcp-server-checklist
---
In 1978, the way to give your computer a new capability was to write a small program that read text, wrote text, did one thing well, and trusted something else to wire it together. In 2026, the way to give your agent a new capability is exactly that, plus a JSON-RPC handshake. MCP servers are the new CLI tools — not as a slogan, but as a structural claim about how this ecosystem works, and I think the analogy is precise enough to make predictions with. Including predictions about where it breaks.

The Unix bet was that composition beats integration: many small tools, one universal interface (text streams), and a shell to combine them in ways no tool author anticipated. Nobody who wrote `grep` imagined every pipeline it would end up in. That's the point. The intelligence lived in the composer — a human at a shell — and the tools stayed simple because they could.

Swap the human for a model and the shell for an agent loop, and you have the MCP ecosystem's actual architecture. The agent is the shell now. It reads your intent, picks tools, sequences them, pipes one tool's output into the next tool's input, and handles the failures. MCP servers are the coreutils: small, single-purpose, indifferent to what they're combined with.

## What translates cleanly

**Do one thing well.** The best MCP servers I've used are embarrassingly narrow — search, extract, one SaaS API, one database. The worst are the kitchen-sink servers exposing forty tools across unrelated domains, and they fail for a mechanical reason Unix never had to articulate: every tool description occupies context, and a bloated toolset taxes the agent's attention on *every* call, relevant or not. Unix tolerated bloated tools; the agent-as-shell actively punishes them. I keep a whole [checklist for evaluating MCP servers](/blog/evaluate-mcp-server-checklist/), and half of it is variations on "does this do one thing."

**Text as the universal interface.** Unix tools compose because everything is a byte stream. MCP tools compose because everything is a JSON result a model can read. Same property, one level up: the format is universal enough that tools need no knowledge of each other. The tool that searches doesn't know about the tool that extracts. The agent glues them, the way the pipe glued `cat` to `sort`.

**Composition the author never imagined.** This is the test that matters. My own server exposes three tools — search, extract, browse — and agents chain them in orders I didn't design for, interleave them with other servers' tools, and occasionally use extract on things I'd have called a search problem. When users of your tool build pipelines you didn't anticipate, the composition model is working. That, more than any directory listing, is [what the MCP ecosystem got right](/blog/mcp-ecosystem-2026/).

**Even the transport rhymes.** stdio is literally the Unix inheritance — an MCP server on stdio is a child process reading stdin and writing stdout, closer to a filter than to a service. The [stdio-versus-HTTP split](/blog/mcp-transport-stdio-vs-http/) is the old local-versus-network split with better framing.

## What doesn't translate

The analogy earns its keep where it breaks, because each break marks work the ecosystem still has to do — work Unix never did.

**Auth.** `grep` never needed credentials. A useful MCP server usually fronts an API that does, which drags identity into a composition model that was designed without one. Whose key is it? The [BYOK answer](/blog/byok-agent-tooling/) — your keys, held locally, no intermediary account — is the one I've bet on, but the ecosystem hasn't settled, and every unsettled option (server-held keys, OAuth flows, platform-brokered tokens) creates a different trust and billing topology. Pipes never had a login step.

**Metering and cost.** This is the deepest break. Composing Unix tools was free — once `awk` was on disk, running it a million times cost nothing you could see. Composing MCP tools spends money: a search rung costs $0 or $0.001 or $0.005 per call depending on the provider behind it, and the composer is a model with no innate price sense. A shell script that ran `grep` in a loop wasted nothing; an agent that loops a paid tool wastes real dollars. The Unix philosophy has no chapter on this because it never needed one — which is exactly why cost-awareness has to be built into the tool layer now. It's why Frugal stamps `cost_usd` on every result: if the shell is a spender, the tools owe it a receipt.

**Trust.** Unix tools were vetted by distribution maintainers and ran with your own eyeballs on the pipeline. MCP servers are installed from directories of varying rigor and composed by a model that reads — and obeys — text. A malicious tool description or a poisoned result is an injection channel with no `man page` review culture to catch it. The trust infrastructure that made `apt install` boring took two decades. MCP is in year three.

**The shell doesn't read the man page — it *is* the man page's audience.** A subtle inversion: Unix documentation was for humans; the program ignored it. An MCP tool description is *executed*, in the sense that the model conditions on it for every routing decision. Tool docs are now an interface, with all the compatibility and quality obligations interfaces carry.

## What the analogy predicts

If MCP servers are the new CLI tools, the ecosystem's trajectory is legible from Unix history, with corrections for the breaks.

Expect a coreutils moment: a small canon of servers — filesystem, search, browser, the top SaaS APIs — that everyone assumes present, the way every Unix box has `ls`. The long tail stays long, but defaults consolidate. Expect the pipefitters to matter more than the pipes: as servers commoditize into single-purpose filters, the value moves to whatever composes and governs them — the agent harness, the router, the policy layer. In Unix terms, the money was never in `cat`. And expect the missing chapters to get written as infrastructure, not convention: metering, auth brokering, and provenance will become layers that wrap servers, because the servers themselves should stay small. The Unix answer to a cross-cutting concern was never "make every tool bigger."

The prediction I hold with most conviction follows from the cost break: composition with a meter running selects for tools that are cheap by default. In a free-composition world, capability wins. In a metered one, the tool that answers at $0 gets composed first, and the $0.005 tool gets composed when it must — a selection pressure Unix never exerted and MCP exerts on every call.

If you're writing an MCP server, the actionable version is old advice with a new addressee: do one thing, document it as if the doc were code (it is), stay cheap to call, and let the agent do the composing. If you're choosing servers, favor the small sharp ones and be suspicious of kitchen sinks. Forty-eight years on, the design pressure that produced the Unix toolbox is operating again — same forces, new shell, and this time the pipe has a price on it. More along these lines in the [MCP ecosystem](/blog/topics/mcp-ecosystem/) series.
