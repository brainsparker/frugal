package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"
)

var funcs = template.FuncMap{
	"fmtDate":  func(t time.Time) string { return t.Format("Jan 2, 2006") },
	"isoDate":  func(t time.Time) string { return t.Format("2006-01-02") },
	"isoTime":  func(t time.Time) string { return t.Format(time.RFC3339) },
	"notZero":  func(t time.Time) bool { return !t.IsZero() },
	"typeName": typeName,
}

func typeName(t string) string {
	switch t {
	case "listicle":
		return "List"
	case "educational":
		return "Guide"
	case "analysis":
		return "Analysis"
	case "problem-solution":
		return "Playbook"
	case "strategic":
		return "Essay"
	}
	return "Post"
}

var tmpl = template.Must(template.New("blog").Funcs(funcs).Parse(templateSrc))

// page is the data handed to every template.
type page struct {
	// head
	Title       string // <title> text
	Description string
	Canonical   string // absolute URL
	OGType      string // website | article
	JSONLD      []template.HTML
	Published   time.Time // article pages only
	Updated     time.Time

	// body
	Site      *Site
	Post      *Post
	Cluster   *Cluster
	Posts     []*Post // page-specific post list
	Related   []*Post
	ClusterOf func(*Post) *Cluster
}

func render(site *Site, published []*Post, outDir string, now time.Time) error {
	clusterOf := func(p *Post) *Cluster { return clusterByKey(site.Clusters, p.Cluster) }
	for _, p := range published {
		if c := clusterOf(p); c != nil {
			p.ClusterName = c.Name
		}
	}
	for _, c := range site.Clusters {
		c.Posts = nil
		for _, p := range published {
			if p.Cluster == c.Key {
				c.Posts = append(c.Posts, p)
			}
		}
	}

	// Post pages.
	for _, p := range published {
		pg := &page{
			Title:       p.Title + " — frugal blog",
			Description: p.Description,
			Canonical:   baseURL + "/blog/" + p.Slug + "/",
			OGType:      "article",
			Published:   p.PublishedAt,
			Updated:     p.UpdatedAt,
			Site:        site,
			Post:        p,
			Cluster:     clusterOf(p),
			Related:     relatedPosts(p, published),
			ClusterOf:   clusterOf,
		}
		pg.JSONLD = []template.HTML{postJSONLD(p), breadcrumbJSONLD(p)}
		if err := writePage(outDir, filepath.Join("blog", p.Slug, "index.html"), "post", pg); err != nil {
			return err
		}
	}

	// Blog index.
	idx := &page{
		Title:       "Blog — provider routing, AI tool costs, and agent infrastructure | frugal",
		Description: "Essays and guides on provider routing, AI tool-call costs, model selection, and building reliable agent infrastructure. By Brian Sparker, creator of Frugal.",
		Canonical:   baseURL + "/blog/",
		OGType:      "website",
		Site:        site,
		Posts:       published,
		ClusterOf:   clusterOf,
		JSONLD:      []template.HTML{blogJSONLD()},
	}
	if err := writePage(outDir, filepath.Join("blog", "index.html"), "index", idx); err != nil {
		return err
	}

	// Topic hubs.
	for _, c := range site.Clusters {
		pg := &page{
			Title:       c.Title + " | frugal blog",
			Description: c.Description,
			Canonical:   baseURL + "/blog/topics/" + c.Key + "/",
			OGType:      "website",
			Site:        site,
			Cluster:     c,
			Posts:       c.Posts,
			ClusterOf:   clusterOf,
			JSONLD:      []template.HTML{clusterJSONLD(c)},
		}
		if err := writePage(outDir, filepath.Join("blog", "topics", c.Key, "index.html"), "topic", pg); err != nil {
			return err
		}
	}

	// Author page.
	author := &page{
		Title:       site.Author.Title,
		Description: site.Author.Description,
		Canonical:   baseURL + authorPath,
		OGType:      "profile",
		Site:        site,
		Posts:       published,
		ClusterOf:   clusterOf,
		JSONLD:      []template.HTML{authorJSONLD(site.Author)},
	}
	if err := writePage(outDir, filepath.Join("author", "brian-sparker", "index.html"), "author", author); err != nil {
		return err
	}

	if err := writeFeed(outDir, published); err != nil {
		return err
	}
	return writeSitemap(outDir, site, published, now)
}

func relatedPosts(p *Post, published []*Post) []*Post {
	bySlug := map[string]*Post{}
	for _, q := range published {
		bySlug[q.Slug] = q
	}
	var out []*Post
	for _, slug := range p.Related {
		if q, ok := bySlug[slug]; ok && q.Slug != p.Slug {
			out = append(out, q)
		}
	}
	// Top up from the same cluster, newest first.
	if len(out) < 3 {
		seen := map[string]bool{p.Slug: true}
		for _, q := range out {
			seen[q.Slug] = true
		}
		for _, q := range published {
			if len(out) >= 3 {
				break
			}
			if q.Cluster == p.Cluster && !seen[q.Slug] {
				out = append(out, q)
				seen[q.Slug] = true
			}
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func writePage(outDir, rel, name string, pg *page) error {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, pg); err != nil {
		return fmt.Errorf("render %s: %w", rel, err)
	}
	path := filepath.Join(outDir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// --- JSON-LD ----------------------------------------------------------

func jsonld(v any) template.HTML {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // static data; cannot fail at runtime
	}
	return template.HTML(b)
}

type m = map[string]any

func personRef() m {
	return m{
		"@type": "Person",
		"name":  authorName,
		"url":   baseURL + authorPath,
	}
}

func publisherRef() m {
	return m{
		"@type": "Organization",
		"name":  "frugal.sh",
		"url":   baseURL + "/",
		"logo": m{
			"@type": "ImageObject",
			"url":   baseURL + "/favicon.svg",
		},
	}
}

func postJSONLD(p *Post) template.HTML {
	d := m{
		"@context":         "https://schema.org",
		"@type":            "BlogPosting",
		"headline":         p.Title,
		"description":      p.Description,
		"datePublished":    p.PublishedAt.Format(time.RFC3339),
		"author":           personRef(),
		"publisher":        publisherRef(),
		"mainEntityOfPage": baseURL + "/blog/" + p.Slug + "/",
		"url":              baseURL + "/blog/" + p.Slug + "/",
	}
	if p.Keyword != "" {
		d["keywords"] = p.Keyword
	}
	if !p.UpdatedAt.IsZero() {
		d["dateModified"] = p.UpdatedAt.Format(time.RFC3339)
	} else {
		d["dateModified"] = p.PublishedAt.Format(time.RFC3339)
	}
	return jsonld(d)
}

func breadcrumbJSONLD(p *Post) template.HTML {
	return jsonld(m{
		"@context": "https://schema.org",
		"@type":    "BreadcrumbList",
		"itemListElement": []m{
			{"@type": "ListItem", "position": 1, "name": "frugal", "item": baseURL + "/"},
			{"@type": "ListItem", "position": 2, "name": "Blog", "item": baseURL + "/blog/"},
			{"@type": "ListItem", "position": 3, "name": p.Title},
		},
	})
}

func blogJSONLD() template.HTML {
	return jsonld(m{
		"@context":    "https://schema.org",
		"@type":       "Blog",
		"name":        "frugal blog",
		"url":         baseURL + "/blog/",
		"description": "Provider routing, AI tool-call costs, model selection, and agent infrastructure.",
		"publisher":   publisherRef(),
		"author":      personRef(),
	})
}

func clusterJSONLD(c *Cluster) template.HTML {
	return jsonld(m{
		"@context":    "https://schema.org",
		"@type":       "CollectionPage",
		"name":        c.Title,
		"url":         baseURL + "/blog/topics/" + c.Key + "/",
		"description": c.Description,
		"isPartOf":    m{"@type": "Blog", "name": "frugal blog", "url": baseURL + "/blog/"},
	})
}

func authorJSONLD(a *Author) template.HTML {
	sameAs := make([]string, 0, len(a.Links))
	for _, l := range a.Links {
		sameAs = append(sameAs, l.URL)
	}
	return jsonld(m{
		"@context": "https://schema.org",
		"@type":    "ProfilePage",
		"mainEntity": m{
			"@type":       "Person",
			"name":        a.Name,
			"url":         baseURL + authorPath,
			"jobTitle":    a.Role,
			"description": a.Description,
			"sameAs":      sameAs,
			"knowsAbout":  a.Expertise,
		},
	})
}

// --- feed --------------------------------------------------------------

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Atom    string     `xml:"xmlns:atom,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	AtomLink    atomLink  `xml:"atom:link"`
	Items       []rssItem `xml:"item"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
}

func writeFeed(outDir string, published []*Post) error {
	n := len(published)
	if n > 20 {
		n = 20
	}
	items := make([]rssItem, 0, n)
	for _, p := range published[:n] {
		u := baseURL + "/blog/" + p.Slug + "/"
		items = append(items, rssItem{
			Title:       p.Title,
			Link:        u,
			GUID:        u,
			PubDate:     p.PublishedAt.Format(time.RFC1123Z),
			Description: p.Excerpt,
		})
	}
	feed := rssFeed{
		Version: "2.0",
		Atom:    "http://www.w3.org/2005/Atom",
		Channel: rssChannel{
			Title:       "frugal blog",
			Link:        baseURL + "/blog/",
			Description: "Provider routing, AI tool-call costs, model selection, and agent infrastructure. By Brian Sparker.",
			AtomLink:    atomLink{Href: baseURL + "/blog/feed.xml", Rel: "self", Type: "application/rss+xml"},
			Items:       items,
		},
	}
	b, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		return err
	}
	out := append([]byte(xml.Header), b...)
	out = append(out, '\n')
	return os.WriteFile(filepath.Join(outDir, "blog", "feed.xml"), out, 0o644)
}

// --- sitemap ------------------------------------------------------------

func writeSitemap(outDir string, site *Site, published []*Post, now time.Time) error {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	entry := func(loc, lastmod, changefreq, priority string) {
		b.WriteString("  <url>\n")
		b.WriteString("    <loc>" + loc + "</loc>\n")
		if lastmod != "" {
			b.WriteString("    <lastmod>" + lastmod + "</lastmod>\n")
		}
		if changefreq != "" {
			b.WriteString("    <changefreq>" + changefreq + "</changefreq>\n")
		}
		if priority != "" {
			b.WriteString("    <priority>" + priority + "</priority>\n")
		}
		b.WriteString("  </url>\n")
	}
	entry(baseURL+"/", "", "weekly", "1.0")
	var newest string
	if len(published) > 0 {
		newest = published[0].PublishedAt.Format("2006-01-02")
	}
	entry(baseURL+"/blog/", newest, "weekly", "0.9")
	for _, c := range site.Clusters {
		var last string
		if len(c.Posts) > 0 {
			last = c.Posts[0].PublishedAt.Format("2006-01-02")
		}
		entry(baseURL+"/blog/topics/"+c.Key+"/", last, "weekly", "0.7")
	}
	entry(baseURL+authorPath, newest, "monthly", "0.6")
	for _, p := range published {
		lastmod := p.PublishedAt
		if !p.UpdatedAt.IsZero() {
			lastmod = p.UpdatedAt
		}
		entry(baseURL+"/blog/"+p.Slug+"/", lastmod.Format("2006-01-02"), "", "0.8")
	}
	b.WriteString("</urlset>\n")
	return os.WriteFile(filepath.Join(outDir, "sitemap.xml"), b.Bytes(), 0o644)
}
