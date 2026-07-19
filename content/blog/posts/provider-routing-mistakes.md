---
title: "5 provider routing mistakes that quietly burn your budget"
slug: provider-routing-mistakes
date: 2026-08-13T15:50
description: "Five API routing mistakes that inflate agent bills: premium-first defaults, paying twice for zero hits, global timeouts, and untyped errors."
excerpt: "None of these show up as errors. They show up as an invoice that's 3–6× what the same traffic should cost, one reasonable-looking call at a time."
cluster: provider-routing
type: listicle
keyword: "API routing mistakes"
related:
  - provider-fallback-chains
  - free-first-default-provider
  - zero-hits-empty-results
---
The expensive API routing mistakes don't look like mistakes. Every individual call in the log looks reasonable — a real provider, a real response, a real price — and the waste only exists in aggregate, where nobody is looking. These are the five I see most when people show me their routing setups, and the five I built Frugal to make hard to commit. Each one is invisible in the logs and obvious on the invoice.

The pattern across all five: routing decisions made by default instead of on purpose. The fix, in every case, costs less than a week and pays forever.

## 1. Premium-first defaults

**The mistake.** Every call goes to the most capable, most expensive provider — $0.005/call search for queries a free index answers identically. Not some calls. Every call, including "what is TCP" and the company's own homepage.

**Why it happens.** The premium provider had the best docs and the cleanest SDK, so it got wired in during week one, and week one never got revisited. Nobody chose to overpay; someone chose convenience once, and the meter has run since. The gap this leaves on the table is not small — the same web search costs $0 to $0.005 depending on who answers, a 5× spread between the two paid rungs alone before the free rungs enter.

**The fix.** Invert the default. Order providers cheapest-first and make each paid rung earn its traffic by the free rungs coming up empty. I've made [the full case for free-first defaults](/blog/free-first-default-provider/) — the short version is that the free rungs (Wikipedia, Marginalia, self-hosted SearXNG) catch a real share of agent queries, and every query they catch is a paid call that never happened.

## 2. Retrying a paid rung after zero hits

**The mistake.** A paid provider returns zero results, and the router — treating "empty" as "failed" — escalates to a pricier provider. Which also returns zero, because the query genuinely has no hits. You paid twice to learn "no results."

**Why it happens.** The retry logic has one branch for "didn't get results," and errors and empties both land in it. It's a type error, conceptually: an outage is a fact about the *provider*, but zero hits from a full-index provider is a fact about the *query*.

**The fix.** Split the semantics by rung. Free provider returns zero hits → fall through; partial indexes miss things and falling through costs nothing. Paid provider returns zero hits → end the chain; the query is empty and confirming that at a higher price is the purest waste in the whole system. This distinction is load-bearing enough that I gave it [its own post](/blog/zero-hits-empty-results/). Encode it in the router, not in reviewer comments.

## 3. No per-call cost attribution

**The mistake.** The router works, the fallbacks fire, and nobody can say what any of it costs, because cost exists only as a monthly invoice aggregated past usefulness. Routing without receipts means every other mistake on this list runs undetected — you can't see the premium-first default or the double-paid zero hits, because no call carries a price.

**Why it happens.** Attribution feels like bookkeeping, and bookkeeping loses the sprint-planning fight to features. Every team plans to add it later. Later is when the invoice doubles.

**The fix.** Stamp every result at the router: `provider_used`, `cost_usd`, `latency_ms`, appended to a local ledger. A JSONL file is the whole MVP — mine lives at `~/.frugal/usage` and never leaves the machine. Once calls carry prices, waste becomes greppable, and the other four mistakes on this list show up in an afternoon of analysis. The longer build-out is in [my post on agent cost observability](/blog/agent-cost-observability/).

## 4. One global timeout across all rungs

**The mistake.** A single timeout — say 10 seconds — wrapped around the entire routing chain. The first rung has a slow day, eats 9.8 seconds, and the chain dies before the rungs that would have answered ever run. Or the inverse: each of four rungs is allowed the full 10 seconds, and the user waits 40 for a failure you knew about at second 12.

**Why it happens.** The timeout was configured where it was easiest to add — around the outermost call — instead of where the decision lives. Chains multiply latency in a way single calls don't, and a global constant can't express "Marginalia gets 2 seconds, the paid rung gets 5, the whole chain gets 8."

**The fix.** Per-rung budgets inside a chain-level budget, with each rung's timeout treated as a fall-through trigger, not a failure. A rung that blows its budget is skipped, the chain continues, and the user-facing deadline holds. Sizing those numbers is a product call, not an infra default — the chain budget is exactly "how long will a user wait for this feature," and that number belongs to whoever owns the feature, not to a YAML file.

## 5. Treating every non-200 the same

**The mistake.** One catch-all error branch: anything that isn't a clean success gets the same retry-then-escalate treatment. But a 429, a 500, and a clean-but-empty 200 are three different facts demanding three different moves — and the catch-all picks the wrong move for at least two of them. Retrying into a rate limit makes the rate limit worse. Hammering a provider mid-outage buys you nothing and delays the fallback that would have worked.

**Why it happens.** Error handling gets written last, against the happy path, usually as one `catch`. Distinguishing failure types requires knowing each provider's actual behavior under stress — knowledge nobody has until they've operated through a real provider outage.

**The fix.** Type your failures and give each a policy. Rate limit → back off *this rung*, fall through now, return later. Server error / timeout → skip the rung immediately, maybe retry once on the next pass. Auth error → page a human; no retry will fix a revoked key. Empty 200 → apply the zero-hit semantics from mistake 2. Four failure types, four policies, maybe fifty lines of code. It's the difference between a router and a switch statement with hope.

## The theme, and where to start

All five mistakes are the same mistake at different altitudes: the router makes a decision — who's first, what empty means, what an error means, how long to wait — and nobody decided it. Defaults decided it.

Start with mistake 3, the ledger, because it's the one that makes the other four visible in *your* traffic instead of my telling you they're there. Then read your own week of stamped calls and fix whichever of the other four is costing you most; for most search-heavy agents it's mistake 1 by a wide margin. The mechanics of building the chain properly are in [the fallback chains post](/blog/provider-fallback-chains/), and the rest of the series lives on the [provider routing](/blog/topics/provider-routing/) hub. Routing is cheap to get right. It's only expensive to not look at.
