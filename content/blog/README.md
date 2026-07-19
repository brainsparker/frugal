# frugal.sh blog content

Markdown sources for the blog at https://frugal.sh/blog/. Rendered into
`docs/` by `cmd/bloggen` (`make blog`); the generated HTML is committed
because Cloudflare Pages serves `docs/` with no build step.

## Layout

- `posts/*.md` — one file per article, YAML front matter + markdown body
- `clusters.yaml` — topic clusters; each renders a hub page at
  `/blog/topics/<key>/`
- `authors/brian-sparker.md` — author entity page at `/author/brian-sparker/`

## Front matter

```yaml
title: "Post title"                # also the H1 and <title>
slug: post-title                   # URL: /blog/<slug>/
date: 2026-05-12T14:30             # publish moment, UTC
updated: 2026-06-01                # optional; sets dateModified/lastmod
description: "Meta description."   # ≤165 chars, required
excerpt: "Card copy for indexes."  # required
cluster: provider-routing          # must exist in clusters.yaml
type: educational                  # listicle | educational | analysis |
                                   # problem-solution | strategic
keyword: "target keyword"          # informs schema keywords
related:                           # slugs surfaced under "Keep reading"
  - some-earlier-post
draft: false                       # true = ignored entirely
```

## Publishing model

`bloggen` only renders posts whose `date` is in the past, so future-dated
posts sit in the repo until the daily `blog-publish` workflow re-runs the
generator and commits the newly live pages. Preview scheduled posts locally
with `make blog-preview` (don't commit that output).

The generator validates before writing: unique slugs, known clusters,
required metadata, and link chronology — a post may only link to posts
published on or before its own date, which keeps every intermediate state
of the scheduled rollout 404-free.
