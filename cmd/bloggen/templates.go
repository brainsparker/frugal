package main

// templateSrc holds every page template. The chrome (head/header/footer)
// mirrors docs/index.html so blog pages feel native to the site; article
// styles live in docs/blog.css.
const templateSrc = `
{{define "head"}}<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{{.Title}}</title>
  <meta name="description" content="{{.Description}}" />
  <meta name="theme-color" content="#fafafa" />
  <meta name="color-scheme" content="light" />

  <meta property="og:type" content="{{.OGType}}" />
  <meta property="og:url" content="{{.Canonical}}" />
  <meta property="og:title" content="{{.Title}}" />
  <meta property="og:description" content="{{.Description}}" />
  <meta property="og:site_name" content="frugal.sh" />
{{- if eq .OGType "article"}}
  <meta property="article:published_time" content="{{isoTime .Published}}" />
{{- if notZero .Updated}}
  <meta property="article:modified_time" content="{{isoTime .Updated}}" />
{{- end}}
  <meta property="article:author" content="https://frugal.sh/author/brian-sparker/" />
{{- end}}

  <meta name="twitter:card" content="summary" />
  <meta name="twitter:title" content="{{.Title}}" />
  <meta name="twitter:description" content="{{.Description}}" />

  <link rel="canonical" href="{{.Canonical}}" />
  <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
  <link rel="alternate" type="application/rss+xml" title="frugal blog" href="/blog/feed.xml" />
  <link rel="stylesheet" href="/styles.css" />
  <link rel="stylesheet" href="/blog.css" />
{{- range .JSONLD}}
  <script type="application/ld+json">{{.}}</script>
{{- end}}
</head>
<body>
  <a class="skip" href="#main">Skip to content</a>

  <header class="top">
    <div class="top-inner">
      <a class="brand" href="/" aria-label="frugal home">
        <span class="brand-mark" aria-hidden="true">🎺</span>
        <span class="brand-name">frugal</span>
      </a>
      <nav class="top-nav" aria-label="Primary">
        <a href="/blog/">Blog</a>
        <a href="/#routing">Routing</a>
        <a href="/#install">Install</a>
        <a class="nav-cta" href="https://github.com/brainsparker/frugal" rel="noopener">GitHub →</a>
      </nav>
    </div>
  </header>

  <main id="main">
{{end}}

{{define "foot"}}
  </main>

  <footer class="bottom">
    <div class="bottom-inner">
      <div>
        <span class="brand-mark small" aria-hidden="true">🎺</span>
        <span>frugal.sh</span>
        <span class="bottom-sep" aria-hidden="true">·</span>
        <span>Created by <a href="/author/brian-sparker/">Brian Sparker</a></span>
      </div>
      <nav aria-label="Footer">
        <a href="/blog/">Blog</a>
        <a href="https://github.com/brainsparker/frugal" rel="noopener">GitHub</a>
        <a href="https://github.com/brainsparker/frugal#readme" rel="noopener">Docs</a>
        <a href="https://github.com/brainsparker/frugal/blob/main/SECURITY.md" rel="noopener">Security</a>
        <a href="https://github.com/brainsparker/frugal/blob/main/LICENSE-BUSL-FAQ.md" rel="noopener">License</a>
      </nav>
    </div>
  </footer>
</body>
</html>
{{end}}

{{define "postcard"}}
        <article class="blog-card">
          <div class="blog-card-meta">
            <a class="blog-chip" href="/blog/topics/{{.Cluster}}/">{{.ClusterName}}</a>
            <time datetime="{{isoDate .PublishedAt}}">{{fmtDate .PublishedAt}}</time>
            <span class="blog-card-kind">{{typeName .Type}}</span>
          </div>
          <h3 class="blog-card-title"><a href="/blog/{{.Slug}}/">{{.Title}}</a></h3>
          <p class="blog-card-excerpt">{{.Excerpt}}</p>
        </article>
{{end}}

{{define "post"}}{{template "head" .}}
    <article class="post">
      <div class="shell post-shell">
        <nav class="crumbs" aria-label="Breadcrumb">
          <a href="/">frugal</a><span aria-hidden="true">/</span><a href="/blog/">Blog</a><span aria-hidden="true">/</span><span aria-current="page">{{.Post.Title}}</span>
        </nav>

        <header class="post-header">
          <div class="blog-card-meta">
            <a class="blog-chip" href="/blog/topics/{{.Post.Cluster}}/">{{.Cluster.Name}}</a>
            <span class="blog-card-kind">{{typeName .Post.Type}}</span>
          </div>
          <h1>{{.Post.Title}}</h1>
          <p class="post-byline">
            By <a rel="author" href="/author/brian-sparker/">Brian Sparker</a>
            · <time datetime="{{isoDate .Post.PublishedAt}}">{{fmtDate .Post.PublishedAt}}</time>
{{- if notZero .Post.UpdatedAt}}
            · Updated <time datetime="{{isoDate .Post.UpdatedAt}}">{{fmtDate .Post.UpdatedAt}}</time>
{{- end}}
            · {{.Post.Minutes}} min read
          </p>
        </header>

        <div class="post-body">
{{.Post.Body}}
        </div>

        <aside class="author-box">
          <div class="author-box-mark" aria-hidden="true">BS</div>
          <div>
            <p class="author-box-name"><a rel="author" href="/author/brian-sparker/">Brian Sparker</a></p>
            <p class="author-box-bio">Creator of <a href="/">Frugal</a>, the open routing layer for AI tools — an MCP server that routes agent tool calls per policy: cost, latency, pinning, failover. Writes about provider routing, AI tool costs, and agent infrastructure.</p>
          </div>
        </aside>

{{- if .Related}}
        <section class="related" aria-labelledby="related-heading">
          <h2 id="related-heading">Keep reading</h2>
          <div class="blog-grid">
{{- range .Related}}
{{template "postcard" .}}
{{- end}}
          </div>
        </section>
{{- end}}

        <div class="cta-banner-card post-cta">
          <div>
            <h2>The open routing layer for AI tools.</h2>
            <p>Frugal is one Go binary: every tool call routed per policy — cheapest by default — with the decision and <code>cost_usd</code> on every result. BYOK, no account.</p>
          </div>
          <div class="cta-banner-actions">
            <a class="btn btn-primary" href="/#install">Install →</a>
            <a class="btn btn-ghost" href="https://github.com/brainsparker/frugal" rel="noopener">GitHub</a>
          </div>
        </div>
      </div>
    </article>
{{template "foot" .}}{{end}}

{{define "index"}}{{template "head" .}}
    <section class="blog-hero">
      <div class="shell">
        <p class="eyebrow">the frugal blog</p>
        <h1>Tool calls are the new cloud bill.</h1>
        <p class="lede">Provider routing, AI tool-call costs, model selection, and the unglamorous work of keeping agents reliable. Written by <a href="/author/brian-sparker/">Brian Sparker</a>, creator of Frugal.</p>
        <div class="blog-topics" role="list">
{{- range .Site.Clusters}}
          <a role="listitem" class="blog-chip" href="/blog/topics/{{.Key}}/">{{.Name}}</a>
{{- end}}
        </div>
      </div>
    </section>

    <section class="blog-list">
      <div class="shell">
        <div class="blog-grid">
{{- range .Posts}}
{{template "postcard" .}}
{{- end}}
        </div>
      </div>
    </section>
{{template "foot" .}}{{end}}

{{define "topic"}}{{template "head" .}}
    <section class="blog-hero">
      <div class="shell">
        <nav class="crumbs" aria-label="Breadcrumb">
          <a href="/">frugal</a><span aria-hidden="true">/</span><a href="/blog/">Blog</a><span aria-hidden="true">/</span><span aria-current="page">{{.Cluster.Name}}</span>
        </nav>
        <h1>{{.Cluster.Title}}</h1>
        <div class="post-body topic-intro">
{{.Cluster.IntroHTML}}
        </div>
      </div>
    </section>

    <section class="blog-list">
      <div class="shell">
        <div class="blog-grid">
{{- range .Posts}}
{{template "postcard" .}}
{{- end}}
        </div>
      </div>
    </section>
{{template "foot" .}}{{end}}

{{define "author"}}{{template "head" .}}
    <section class="blog-hero author-hero">
      <div class="shell">
        <nav class="crumbs" aria-label="Breadcrumb">
          <a href="/">frugal</a><span aria-hidden="true">/</span><a href="/blog/">Blog</a><span aria-hidden="true">/</span><span aria-current="page">{{.Site.Author.Name}}</span>
        </nav>
        <div class="author-head">
          <div class="author-box-mark large" aria-hidden="true">BS</div>
          <div>
            <h1>{{.Site.Author.Name}}</h1>
            <p class="author-role">{{.Site.Author.Role}}</p>
          </div>
        </div>
        <div class="post-body author-bio">
{{.Site.Author.Bio}}
        </div>
{{- if .Site.Author.Expertise}}
        <div class="blog-topics" role="list">
{{- range .Site.Author.Expertise}}
          <span role="listitem" class="blog-chip static">{{.}}</span>
{{- end}}
        </div>
{{- end}}
{{- if .Site.Author.Links}}
        <p class="author-links">
{{- range .Site.Author.Links}}
          <a href="{{.URL}}" rel="me noopener">{{.Label}} →</a>
{{- end}}
        </p>
{{- end}}
      </div>
    </section>

    <section class="blog-list" aria-labelledby="byline-heading">
      <div class="shell">
        <h2 id="byline-heading" class="author-posts-heading">All posts by {{.Site.Author.Name}}</h2>
        <div class="blog-grid">
{{- range .Posts}}
{{template "postcard" .}}
{{- end}}
        </div>
      </div>
    </section>
{{template "foot" .}}{{end}}
`
