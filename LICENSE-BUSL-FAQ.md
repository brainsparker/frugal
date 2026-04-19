# BUSL 1.1 FAQ

Frugal is distributed under the [Business Source License 1.1](./LICENSE).
This FAQ is a plain-English explanation of the practical effect. It is not
legal advice. The [LICENSE](./LICENSE) is the authoritative text.

## TL;DR

- Run Frugal yourself: **allowed** (production, internal, commercial).
- Modify Frugal and run it yourself: **allowed**.
- Build a product that uses Frugal internally: **allowed**.
- Sell Frugal (or a fork) as a hosted LLM-routing service that competes with
  frugal.sh: **not allowed** without a commercial license.
- Four years after a given version is published, that version becomes
  Apache 2.0.

## What is the Business Source License?

BUSL 1.1 is a source-available license. The source is public, you can read it,
modify it, and run it yourself, but one specific commercial use is carved
out for a limited time.

That carve-out for Frugal is: **you can't take Frugal and resell it as a hosted
routing service that competes with Frugal's hosted product**. Everything else
is fair game.

## What can I do today?

Yes, you can:

- Download, build, and run Frugal on your own machines for any purpose,
  including production and commercial use inside your own company.
- Use Frugal as a client-side tool (`frugal python app.py`) for routing your
  team's LLM traffic.
- Fork the repo, modify it, and deploy your fork internally.
- Use Frugal's output (completions) in commercial products.
- Include Frugal as a dependency of a larger product, so long as your product
  is not itself a competing hosted routing service.
- Run Frugal as an internal shared service within your organization, including
  affiliates under common control.

Not without a commercial license:

- Operate a paid, third-party-facing service that offers LLM request routing
  substantially similar to frugal.sh. (Running a free service is fine.
  Running a paid service that happens to use Frugal is fine if LLM routing
  is not the thing you're selling. What's restricted is reselling the
  routing itself.)
- Embed Frugal's code or binaries inside a competing product in a way that
  requires Frugal to operate.

## When does it become fully open source?

Each version of Frugal converts to the Apache License 2.0 four years after
that specific version is first published. Older versions therefore convert
first; the latest version is always under BUSL until its own four-year clock
runs out.

## Why not MIT or Apache 2.0 up front?

Frugal's taxonomy and cost router are the product. An MIT license would let
a larger cloud or AI company take the whole thing, run it as a managed
service, and undercut the ability of Frugal's authors to sustain the project.
BUSL 1.1 keeps the code open to individual developers and companies while
protecting the commercial path. The four-year conversion guarantees that the
code does eventually become fully OSS-licensed.

## What if I need a commercial license?

Email `licensing@frugal.sh`.

## Where can I read more about BUSL 1.1?

- [Official text at mariadb.com/bsl11](https://mariadb.com/bsl11/)
- [HashiCorp's BUSL FAQ](https://www.hashicorp.com/license-faq) — the
  phrasing in Frugal's Additional Use Grant is modeled on theirs.
