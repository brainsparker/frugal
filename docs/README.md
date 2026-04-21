# frugal.sh landing page

Static single-page site for `https://frugal.sh/`. Ships with one HTML file, one
CSS file, one SVG favicon, a `_redirects` rule that rewrites `/install` to the
installer script in this repo, and an `_headers` file that sets CSP + cache
headers. Total payload is under 30 KB.

> The HTML, `_redirects`, and sitemap all point at
> `github.com/brainsparker/frugal` — the current repo slug. If you rename the
> repo to `frugalsh/frugal` (to match `install.sh`'s `REPO="frugalsh/frugal"`
> and the README's other references), do a find/replace across this directory
> before the next deploy. `install.sh` itself also needs its `REPO=` line
> updated so the API call for the latest release targets the right repo.

Designed for Cloudflare Pages (primary) and GitHub Pages (fallback).

## Deploy on Cloudflare Pages (recommended)

Cloudflare Pages is the right choice for `frugal.sh` because `.sh` domains
default to Cloudflare DNS anyway, and Pages natively supports the `/install`
200-rewrite via `_redirects` — critical for `curl … | sh` to work without a
3xx hop.

1. Push `docs/` to GitHub on the `main` branch.
2. Cloudflare dashboard → **Workers & Pages → Create → Connect to Git**.
3. Select this repo. Configure:
   - **Production branch:** `main`
   - **Build command:** *(leave empty — no build step)*
   - **Build output directory:** `docs`
   - **Root directory:** `/` (default)
4. Deploy. The first build publishes to `<project>.pages.dev`.
5. Back in the project → **Custom domains → Set up a custom domain** →
   `frugal.sh` and also `www.frugal.sh`. Cloudflare will add the CNAME
   records automatically because the zone is already on Cloudflare.
6. After DNS propagates, verify:
   - `curl -sS https://frugal.sh/ | head` returns the HTML
   - `curl -sSL https://frugal.sh/install | head` returns `#!/usr/bin/env bash`
   - `curl -I https://frugal.sh/install` shows
     `content-type: text/x-shellscript` and a 200 status (not a 301/302)

The `_headers` file pins a strict CSP, `HSTS` with preload, and short
cache on the install script so operators always get the latest checksum-
verified bytes.

## Deploy on GitHub Pages (fallback)

Use this if you'd rather not bring Cloudflare Pages into the mix. Downsides:
GH Pages doesn't support the `/install` rewrite cleanly, and the `_headers`
file is ignored. You'd need to either commit the install script into this
directory (as `install.sh`) or accept that users would pull it from a
redirect to `raw.githubusercontent.com`.

1. Repo → **Settings → Pages**.
2. Source: **Deploy from a branch**. Branch: `main`, folder: `/docs`.
3. Custom domain: `frugal.sh`. Save; check **Enforce HTTPS** once the cert
   provisions.
4. Copy `../install.sh` into `docs/install.sh` (one-off; tag-updated) so
   `https://frugal.sh/install.sh` works. If you want the shorter
   `/install` URL you'd need to add a meta-refresh HTML stub, which doesn't
   pipe cleanly into `sh`. For that reason, Cloudflare Pages is preferred.

## Local preview

Any static server works. Built-in Python is fine:

```bash
cd docs
python3 -m http.server 8000
# open http://localhost:8000/
```

## Editing

- `index.html` — content. One file so the whole page is easy to review.
- `styles.css` — tokens at the top of the file (`:root`), dark-mode default
  with a `prefers-color-scheme: light` override. Change the accent by
  editing `--accent`.
- `favicon.svg` — rounded square with a monospace "f". Change the fill to
  retheme.
- `_redirects` — add convenience redirects here (e.g. `/talk-2026 → …`).
- `_headers` — cache and CSP policy.

Keep this page command-first. If you find yourself reaching for a third-party
font or an analytics script, push back — the page's value prop is that it
loads before the user has time to regret clicking.
