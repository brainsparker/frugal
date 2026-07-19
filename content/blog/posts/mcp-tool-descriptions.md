---
title: "What makes a good MCP tool description: 7 patterns"
slug: mcp-tool-descriptions
date: 2026-09-25T16:30
description: "MCP tool design that models actually follow: 7 description patterns — cost and limits up front, argument examples, when not to call, failure shapes."
excerpt: "The tool description is the only documentation the model will ever read, and most are written like marketing copy. Seven patterns from operating three tools in production."
cluster: mcp-ecosystem
type: listicle
keyword: "MCP tool design"
related:
  - evaluate-mcp-server-checklist
  - agent-discovery-well-known
  - zero-hits-empty-results
---
A tool description is a prompt. Not documentation, not marketing copy — a prompt, injected into every conversation the model has, deciding whether your tool gets called, with what arguments, and what the model does when the result comes back weird. Most MCP tool design effort goes into the implementation, and then the description gets one throwaway sentence. That's backwards. The description is the only part the model reads.

These seven patterns come from operating `frugal__search`, `frugal__extract`, and `frugal__browse` in production and watching, in logs, what models actually do with them. Every one of these earned its place by fixing a misuse I could see in the ledger.

## 1. Name the capability, not the brand

`frugal__search` searches the web. It is not called `frugal__serper_search`, because the model shouldn't know or care that Serper exists — behind that one name sits a ladder of providers, and which rung answers is the router's business. Brand-named tools leak implementation into the interface: the model develops opinions about the vendor, prompts get written against one provider's quirks, and swapping rungs becomes a breaking change to your agent's brain.

Name tools for what they do — `search`, `extract`, `browse` — and keep the provider in the response metadata where it belongs. This is the same portability discipline I apply everywhere else, and it's also what makes the tool legible to [discovery surfaces](/blog/agent-discovery-well-known/): a crawler can understand "search," but a brand name is just a string.

## 2. State cost and limits in the description

Models economize when you give them something to economize with. A description that says "searches the web" gives the model no reason to prefer one call over three. One that says results may cost between $0 and $0.005 per call depending on which provider answers, and that every response stamps the actual `cost_usd`, gives the model a reason to write one good query instead of five sloppy ones.

Limits belong here too: result caps, page-size ceilings, timeout behavior, rate limits. A model that knows the tool returns at most N results stops asking for more. A model that knows nothing pushes until something breaks, and then improvises about what breaking meant.

## 3. Give argument examples

Schemas constrain; examples teach. A `query` parameter typed as `string` is satisfied equally by `"acme corp pricing"` and by a 40-word conversational question, and models produce the second constantly unless shown otherwise. One line — good: `"golang readability library"`, bad: `"can you find me some information about..."` — does more than a paragraph of prose about query construction.

The highest-value examples mark the boundaries. For `frugal__extract`, the description shows what a URL argument should look like *and* says that search-result URLs can be passed straight through — because the failure I actually saw was models re-searching for a page they already had the URL for, burning a call to rediscover known information.

## 4. Say when NOT to use the tool

The costliest confusion in a multi-tool server is between adjacent tools, and only the description can draw the line. `frugal__extract` pulls readable content from a static page; `frugal__browse` drives a real browser at ~$0.002 per 30-second unit. Without guidance, a model will cheerfully browse a page that extract would have handled free — the capability overlaps, so the model needs the *economics* to choose.

So the descriptions say it directly: don't browse when extract will do; try extract first and escalate only if the page needs JavaScript or interaction. Don't search when you already hold the URL. Negative space is part of the interface. A tool description that only says what the tool is good at reads like a resume, and the model, like any hiring manager, ends up learning the gaps the hard way.

## 5. Make outputs predictable

Every response from all three tools carries the same trailer: `provider_used`, `cost_usd`, `latency_ms`, alongside a result body whose shape doesn't change based on which rung answered. Wikipedia hits and Serper hits come back in one normalized structure. The description promises this shape, and the server keeps the promise.

Predictability is what lets a model chain calls without re-inspecting everything — it can take `url` fields from search results and hand them to extract because the description guaranteed they'd be there. Every conditional in your output shape ("sometimes includes X when...") becomes a fork in the model's reasoning, and forks are where hallucinated field names creep in.

## 6. Document failure shapes — empty is not error

There are two ways a tool call yields nothing, and a model that can't tell them apart will do the wrong thing with both. An *error* means the attempt failed: retrying or trying another approach is reasonable. An *empty result* means the attempt succeeded and the answer is "nothing there": retrying the identical call is waste, and treating it as a crash makes agents abandon good plans.

The description should say what each looks like. For `frugal__search`: an empty result set after the ladder has been walked is a real answer — [zero hits carries information](/blog/zero-hits-empty-results/), and by the time the model sees it, the router has already applied the fall-through rules, so re-querying with synonyms beats re-calling with the same string. Errors, by contrast, come back as explicit error payloads, not as empty lists. Blur that line in your API and the model inherits the blur at $0.005 a confusion.

## 7. One tool, one job

The temptation is the mega-tool: `web_helper` with a `mode` parameter and twelve behaviors. Every mode makes the description longer, the argument space blurrier, and the model's tool choice worse — you've moved the routing problem *into the prompt*, where it costs context tokens on every turn and gets decided fresh by a model instead of once by you.

Three tools with one job each — search finds, extract reads, browse interacts — let each description be short, sharp, and mostly about boundaries. If you're staring at a `mode` enum, that's usually N tools wearing a trenchcoat. Split them. The model will pick between three well-described tools far more reliably than between twelve modes of one vague one.

## Where to start

Rewrite your most-called tool's description against this list, then do what almost nobody does: read your logs and check what changed. Wrong-tool calls, redundant searches, retries against empty results — these are all visible in a decent ledger, and they're the eval that matters. A description is a prompt, and prompts earn their keep empirically. It's the same standard I'd hold [any MCP server you're evaluating](/blog/evaluate-mcp-server-checklist/) to, and the cheapest quality lever in the whole [MCP ecosystem](/blog/topics/mcp-ecosystem/): the implementation took you weeks, and the description that decides whether it gets used well takes an afternoon.
