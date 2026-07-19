package main

import (
	"testing"
	"time"
)

func TestSplitFrontMatter(t *testing.T) {
	raw := []byte("---\ntitle: Hi\n---\nbody here\n")
	fm, body, err := splitFrontMatter(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(fm) != "title: Hi" {
		t.Errorf("fm = %q", fm)
	}
	if string(body) != "body here\n" {
		t.Errorf("body = %q", body)
	}
	if _, _, err := splitFrontMatter([]byte("no front matter")); err == nil {
		t.Error("expected error for missing front matter")
	}
}

func TestParseDate(t *testing.T) {
	for _, s := range []string{"2026-05-12", "2026-05-12T14:30", "2026-05-12T14:30:00Z"} {
		if _, err := parseDate(s); err != nil {
			t.Errorf("parseDate(%q): %v", s, err)
		}
	}
	if _, err := parseDate("May 12"); err == nil {
		t.Error("expected error for unparseable date")
	}
}

func TestSplitPublishedVsScheduled(t *testing.T) {
	now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	posts := []*Post{
		{Slug: "future", PublishedAt: now.Add(48 * time.Hour)},
		{Slug: "past", PublishedAt: now.Add(-48 * time.Hour)},
	}
	pub, sched := split(posts, now, false)
	if len(pub) != 1 || pub[0].Slug != "past" {
		t.Errorf("published = %v", pub)
	}
	if len(sched) != 1 || sched[0].Slug != "future" {
		t.Errorf("scheduled = %v", sched)
	}
	pub, sched = split(posts, now, true)
	if len(pub) != 2 || len(sched) != 0 {
		t.Errorf("with -future: published=%d scheduled=%d", len(pub), len(sched))
	}
}

func TestValidateLinkChronology(t *testing.T) {
	clusters := []*Cluster{{Key: "routing"}}
	early := &Post{
		Title: "Early", Slug: "early", Cluster: "routing",
		Description: "d", Excerpt: "e", SourcePath: "early.md",
		PublishedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}
	late := &Post{
		Title: "Late", Slug: "late", Cluster: "routing",
		Description: "d", Excerpt: "e", SourcePath: "late.md",
		PublishedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Related:     []string{"early"},
	}
	site := &Site{Posts: []*Post{late, early}, Clusters: clusters}
	if errs := validate(site); len(errs) != 0 {
		t.Fatalf("valid site rejected: %v", errs)
	}

	// A post must not reference one that publishes after it.
	early.Related = []string{"late"}
	if errs := validate(site); len(errs) == 0 {
		t.Error("expected chronology error for early → late link")
	}
	early.Related = nil

	// Inline body links are checked too.
	early.Body = `<a href="/blog/late/">later post</a>`
	if errs := validate(site); len(errs) == 0 {
		t.Error("expected chronology error for inline early → late link")
	}
	early.Body = ""

	// Unknown slugs are rejected.
	late.Related = []string{"nope"}
	if errs := validate(site); len(errs) == 0 {
		t.Error("expected unknown-slug error")
	}
}

func TestValidateFrontMatter(t *testing.T) {
	clusters := []*Cluster{{Key: "routing"}}
	good := func() *Post {
		return &Post{
			Title: "T", Slug: "ok-slug", Cluster: "routing",
			Description: "d", Excerpt: "e", SourcePath: "p.md",
			PublishedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		}
	}
	p := good()
	p.Slug = "Bad Slug!"
	if errs := validate(&Site{Posts: []*Post{p}, Clusters: clusters}); len(errs) == 0 {
		t.Error("expected bad-slug error")
	}
	p = good()
	p.Cluster = "nope"
	if errs := validate(&Site{Posts: []*Post{p}, Clusters: clusters}); len(errs) == 0 {
		t.Error("expected unknown-cluster error")
	}
	a, b := good(), good()
	b.SourcePath = "q.md"
	if errs := validate(&Site{Posts: []*Post{a, b}, Clusters: clusters}); len(errs) == 0 {
		t.Error("expected duplicate-slug error")
	}
}
