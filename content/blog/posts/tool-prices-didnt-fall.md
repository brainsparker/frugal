---
title: "Token prices fell 90%. Tool prices didn't."
slug: tool-prices-didnt-fall
date: 2026-07-09T16:25
description: "AI tool pricing hasn't followed token prices down. Why search, scrape, and browse calls stay expensive — and the routing layer that fixes it."
excerpt: "Inference got an order of magnitude cheaper and everyone noticed. The per-call price of search, scrape, and browse barely moved, and almost nobody did."
cluster: ai-costs
type: strategic
keyword: "AI tool pricing"
related:
  - rack-rate-gap-web-search-costs
  - deep-research-cost-teardown
  - free-first-default-provider
---
Model inference has gotten an order of magnitude cheaper. The tokens that anchored every AI cost model two years ago now cost a tenth as much, sometimes less, and the industry has treated that decline as a law of nature. Meanwhile AI tool pricing — the per-call cost of search, scrape, and browse — has barely moved. A web search is still $0.001 on the cheap paid rung and $0.005 on the expensive one. A scrape is still around $0.001 a page. A browser session still runs about $0.002 per 30 seconds.

Two curves, one falling fast, one flat. Every agent budget lives at their intersection, and the intersection is moving.

## The bill has quietly changed shape

Everyone's mental model of agent costs was formed when tokens were the expensive part, and mental models outlive their facts. Optimize the prompts, cache the context, shrink the model — the whole cost-cutting canon is token-shaped.

But an agent is a loop that *acts*, and its actions are metered separately from its thinking. When the thinking gets 10× cheaper and the acting doesn't, the acting becomes the bill. In [one deep-research teardown I keep coming back to](/blog/deep-research-cost-teardown/), search alone was 54% of task cost. Not the model — the searches. And that share only grows as inference keeps falling, because the ratio between the curves compounds every time a model provider cuts prices and a tool provider doesn't.

If your cost dashboard still leads with tokens, it's auditing the shrinking half of the invoice.

## Why token prices fell

It's worth being precise about why inference got cheap, because none of the reasons transfer to tools.

Models compete on a shared substrate. Hardware improved and every provider rode it. Inference stacks got dramatically better — batching, quantization, distillation — and those wins ship as price cuts because a dozen labs sell a substitutable product into the same benchmark-literate market. When your customer can rerun their evals against a rival model in an afternoon, price is a weapon. So it got used, repeatedly, in public.

Cheap-to-serve, brutally competitive, easy to switch. Now compare the tool side.

## Why tool prices didn't

**The moat is the index, not the model.** A search provider's asset is a crawl and index of the web — years of infrastructure that doesn't get cheaper because GPUs did. Nobody distills a web index. The cost structure that forced model prices down simply doesn't operate here, and the few players who own real indexes know exactly how few they are.

**Less competition per capability.** Every capability has a dozen model providers. How many independent web-scale search indexes are there? A handful, with most "search APIs" reselling one of them. Scraping and browser automation are more crowded but differentiate on the annoying parts — anti-bot handling, rendering fidelity — which fragments them into mini-markets with little price pressure. I laid out just how wide the resulting spread is in [the rack-rate gap piece](/blog/rack-rate-gap-web-search-costs/): $0 to $0.005 for the same search, a 5× gap between the two paid rungs alone. Gaps like that don't survive in competitive markets. This one has held.

**Procurement inertia.** Tool calls are cheap *individually*, and that's exactly what protects their price. Nobody renegotiates a $0.005 line item. A team that would spend a quarter migrating models to save 40% on inference won't spend an afternoon re-evaluating search providers, because search is "basically free" — right up until an agent multiplies it by a few hundred calls per task. Per-call prices are small enough to be invisible and metered enough to compound. From a seller's perspective, that's the perfect price.

Put the three together and flat prices stop being surprising. There's no mechanism pushing them down. The suppliers won't cut what customers don't compare.

## What happens to markets like this

Not price collapse — arbitrage. When a market holds a wide spread between substitutable options ($0 free rungs, $0.001, $0.005) and buyers can't be bothered to compare, the correction arrives as an intermediary layer that compares automatically, per call.

We've run this exact play before. Origin bandwidth was expensive and variable, so the CDN emerged: a routing layer that serves what it can from cheap edges and falls back to origin only when it must. Nobody debates whether to put a CDN in front of a website anymore; it's ambient infrastructure, and origin egress became something you pay for only when the cheap path genuinely can't answer.

My prediction is that tool routing becomes the same kind of layer, as standard as a CDN. Free and local providers are the edge cache: Wikipedia, Marginalia, a self-hosted SearXNG, local extraction — $0 rungs that answer a real share of agent traffic. Paid APIs are origin: authoritative, metered, and called only on fall-through. The economics rhyme too well for this not to happen. When the same lookup costs $0 or $0.005 depending on who answers, and agents issue lookups by the hundred, a layer that walks the ladder cheapest-first pays for itself on contact — the [free-first argument](/blog/free-first-default-provider/) in infrastructure form.

The interesting second-order effect: routing layers create the price competition the market lacks. Once traffic flows through a ladder, providers compete for *position on the ladder*, and a rung that's 5× pricier than its neighbor needs to answer queries its neighbor can't. Rack rates stop being invisible defaults and start being bids. That's the mechanism CDNs applied to bandwidth pricing, and there's no reason it stops short of tool calls.

## What to do while prices stay flat

Betting on tool prices falling is not a strategy; nothing in the market structure suggests they will. The moves that work now are the ones a router would make for you:

Reprice your own traffic. Take last month's tool calls and compute what they'd cost at each rung's rack rate — the spread between what you paid and the cheapest rung that would have answered is your routing dividend, sitting unclaimed. Then make free the default and paid the exception, in that order of effort. The rest of my writing on the metered side of agents lives under [AI costs](/blog/topics/ai-costs/).

Token prices fell because that market punished anyone who didn't cut. Tool prices are flat because nobody's comparing. Be the buyer who compares — or run a layer that does it on every call.
