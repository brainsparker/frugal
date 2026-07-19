// Command bloggen renders the frugal.sh blog from markdown sources in
// content/blog/ into static HTML under docs/. Cloudflare Pages serves docs/
// directly, so generated output is committed.
//
// Publishing model: posts carry a `date` in front matter. Posts dated in the
// future are parsed and validated but not rendered, so a scheduled post ships
// in the repo and goes live when the daily blog-publish workflow re-runs the
// generator after its date passes. Pass -future to preview unpublished posts
// locally.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"gopkg.in/yaml.v3"
)

const (
	baseURL    = "https://frugal.sh"
	authorName = "Brian Sparker"
	authorPath = "/author/brian-sparker/"
)

// Post is one blog article: YAML front matter + markdown body.
type Post struct {
	Title       string   `yaml:"title"`
	Slug        string   `yaml:"slug"`
	Date        string   `yaml:"date"`    // RFC3339 or YYYY-MM-DD
	Updated     string   `yaml:"updated"` // optional
	Description string   `yaml:"description"`
	Excerpt     string   `yaml:"excerpt"`
	Cluster     string   `yaml:"cluster"`
	Type        string   `yaml:"type"` // listicle | educational | analysis | problem-solution | strategic
	Keyword     string   `yaml:"keyword"`
	Related     []string `yaml:"related"`
	Draft       bool     `yaml:"draft"`

	PublishedAt time.Time     `yaml:"-"`
	UpdatedAt   time.Time     `yaml:"-"` // zero if never updated
	Body        template.HTML `yaml:"-"`
	Minutes     int           `yaml:"-"`
	SourcePath  string        `yaml:"-"`
	ClusterName string        `yaml:"-"` // display name, filled in render()
}

// Cluster is a topic hub: a pillar-style landing page that links every post
// in the cluster.
type Cluster struct {
	Key         string `yaml:"key"`
	Name        string `yaml:"name"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Intro       string `yaml:"intro"` // markdown

	IntroHTML template.HTML `yaml:"-"`
	Posts     []*Post       `yaml:"-"`
}

// Author holds the author-page front matter and markdown bio.
type Author struct {
	Name        string   `yaml:"name"`
	Role        string   `yaml:"role"`
	Title       string   `yaml:"title"`
	Description string   `yaml:"description"`
	Links       []Link   `yaml:"links"`
	Expertise   []string `yaml:"expertise"`

	Bio template.HTML `yaml:"-"`
}

// Link is a labeled external profile URL (used for Person sameAs).
type Link struct {
	Label string `yaml:"label"`
	URL   string `yaml:"url"`
}

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(gmhtml.WithUnsafe()),
)

func main() {
	var (
		contentDir = flag.String("content", "content/blog", "content source directory")
		outDir     = flag.String("out", "docs", "output directory (served site root)")
		nowFlag    = flag.String("now", "", "override build time (RFC3339), for tests/previews")
		future     = flag.Bool("future", false, "render future-dated posts too (local preview)")
	)
	flag.Parse()

	now := time.Now().UTC()
	if *nowFlag != "" {
		t, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			fatal("bad -now: %v", err)
		}
		now = t.UTC()
	}

	site, err := load(*contentDir)
	if err != nil {
		fatal("%v", err)
	}
	if errs := validate(site); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "error:", e)
		}
		os.Exit(1)
	}
	published, scheduled := split(site.Posts, now, *future)
	if err := render(site, published, *outDir, now); err != nil {
		fatal("%v", err)
	}
	fmt.Printf("bloggen: %d published, %d scheduled (next: %s)\n",
		len(published), len(scheduled), nextDate(scheduled))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "bloggen: "+format+"\n", args...)
	os.Exit(1)
}

// Site is everything loaded from the content directory.
type Site struct {
	Posts    []*Post
	Clusters []*Cluster
	Author   *Author
}

func load(dir string) (*Site, error) {
	posts, err := loadPosts(filepath.Join(dir, "posts"))
	if err != nil {
		return nil, err
	}
	clusters, err := loadClusters(filepath.Join(dir, "clusters.yaml"))
	if err != nil {
		return nil, err
	}
	author, err := loadAuthor(filepath.Join(dir, "authors", "brian-sparker.md"))
	if err != nil {
		return nil, err
	}
	return &Site{Posts: posts, Clusters: clusters, Author: author}, nil
}

func loadPosts(dir string) ([]*Post, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read posts dir: %w", err)
	}
	var posts []*Post
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		fm, body, err := splitFrontMatter(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		p := &Post{SourcePath: path}
		if err := yaml.Unmarshal(fm, p); err != nil {
			return nil, fmt.Errorf("%s: front matter: %w", path, err)
		}
		if p.Draft {
			continue
		}
		p.PublishedAt, err = parseDate(p.Date)
		if err != nil {
			return nil, fmt.Errorf("%s: date: %w", path, err)
		}
		if p.Updated != "" {
			p.UpdatedAt, err = parseDate(p.Updated)
			if err != nil {
				return nil, fmt.Errorf("%s: updated: %w", path, err)
			}
		}
		var buf bytes.Buffer
		if err := md.Convert(body, &buf); err != nil {
			return nil, fmt.Errorf("%s: markdown: %w", path, err)
		}
		p.Body = template.HTML(buf.String())
		p.Minutes = readingMinutes(body)
		posts = append(posts, p)
	}
	sort.Slice(posts, func(i, j int) bool { return posts[i].PublishedAt.After(posts[j].PublishedAt) })
	return posts, nil
}

func loadClusters(path string) ([]*Cluster, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read clusters: %w", err)
	}
	var clusters []*Cluster
	if err := yaml.Unmarshal(raw, &clusters); err != nil {
		return nil, fmt.Errorf("clusters.yaml: %w", err)
	}
	for _, c := range clusters {
		var buf bytes.Buffer
		if err := md.Convert([]byte(c.Intro), &buf); err != nil {
			return nil, fmt.Errorf("cluster %s intro: %w", c.Key, err)
		}
		c.IntroHTML = template.HTML(buf.String())
	}
	return clusters, nil
}

func loadAuthor(path string) (*Author, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read author: %w", err)
	}
	fm, body, err := splitFrontMatter(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	a := &Author{}
	if err := yaml.Unmarshal(fm, a); err != nil {
		return nil, fmt.Errorf("%s: front matter: %w", path, err)
	}
	var buf bytes.Buffer
	if err := md.Convert(body, &buf); err != nil {
		return nil, fmt.Errorf("%s: markdown: %w", path, err)
	}
	a.Bio = template.HTML(buf.String())
	return a, nil
}

func splitFrontMatter(raw []byte) (fm, body []byte, err error) {
	const delim = "---\n"
	s := string(raw)
	if !strings.HasPrefix(s, delim) {
		return nil, nil, fmt.Errorf("missing front matter")
	}
	rest := s[len(delim):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return nil, nil, fmt.Errorf("unterminated front matter")
	}
	return []byte(rest[:end]), []byte(rest[end+len("\n---\n"):]), nil
}

func parseDate(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable date %q", s)
}

func readingMinutes(body []byte) int {
	words := len(strings.Fields(string(body)))
	m := (words + 229) / 230
	if m < 1 {
		m = 1
	}
	return m
}

var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// validate enforces the invariants that keep future cron builds green:
// unique slugs, known clusters, related/inline links only to posts that will
// already be live when the linking post publishes.
func validate(site *Site) []error {
	var errs []error
	byNum := map[string]*Post{}
	for _, p := range site.Posts {
		switch {
		case p.Title == "":
			errs = append(errs, fmt.Errorf("%s: missing title", p.SourcePath))
		case !slugRe.MatchString(p.Slug):
			errs = append(errs, fmt.Errorf("%s: bad slug %q", p.SourcePath, p.Slug))
		case p.Description == "":
			errs = append(errs, fmt.Errorf("%s: missing description", p.SourcePath))
		case len(p.Description) > 165:
			errs = append(errs, fmt.Errorf("%s: description too long (%d chars)", p.SourcePath, len(p.Description)))
		case p.Excerpt == "":
			errs = append(errs, fmt.Errorf("%s: missing excerpt", p.SourcePath))
		}
		if prev, dup := byNum[p.Slug]; dup {
			errs = append(errs, fmt.Errorf("%s: slug %q already used by %s", p.SourcePath, p.Slug, prev.SourcePath))
		}
		byNum[p.Slug] = p
		if clusterByKey(site.Clusters, p.Cluster) == nil {
			errs = append(errs, fmt.Errorf("%s: unknown cluster %q", p.SourcePath, p.Cluster))
		}
	}
	// Link discipline: a post may only reference posts published on or
	// before its own date; anything else 404s between the two publish
	// dates once the scheduler is live.
	linkRe := regexp.MustCompile(`href="/blog/([a-z0-9-]+)/"`)
	for _, p := range site.Posts {
		targets := append([]string{}, p.Related...)
		for _, m := range linkRe.FindAllStringSubmatch(string(p.Body), -1) {
			if m[1] != "topics" {
				targets = append(targets, m[1])
			}
		}
		for _, slug := range targets {
			t, ok := byNum[slug]
			if !ok {
				errs = append(errs, fmt.Errorf("%s: links to unknown post %q", p.SourcePath, slug))
				continue
			}
			if t.PublishedAt.After(p.PublishedAt) {
				errs = append(errs, fmt.Errorf("%s: links to %q which publishes later (%s > %s)",
					p.SourcePath, slug, t.Date, p.Date))
			}
		}
	}
	return errs
}

func clusterByKey(clusters []*Cluster, key string) *Cluster {
	for _, c := range clusters {
		if c.Key == key {
			return c
		}
	}
	return nil
}

func split(posts []*Post, now time.Time, includeFuture bool) (published, scheduled []*Post) {
	for _, p := range posts {
		if includeFuture || !p.PublishedAt.After(now) {
			published = append(published, p)
		} else {
			scheduled = append(scheduled, p)
		}
	}
	return published, scheduled
}

func nextDate(scheduled []*Post) string {
	if len(scheduled) == 0 {
		return "none"
	}
	next := scheduled[len(scheduled)-1] // sorted desc; earliest future post is last
	return next.PublishedAt.Format("2006-01-02")
}
