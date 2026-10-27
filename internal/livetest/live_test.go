//go:build live

// Package livetest holds opt-in smoke tests that call the real provider
// APIs. They only compile with `-tags live` and only run when
// FRUGAL_LIVE=1, so normal `go test ./...` and CI never touch the
// network. Each paid/keyed provider skips cleanly when its secret is
// absent. One minimal query per provider keeps cost tiny.
//
//	FRUGAL_LIVE=1 go test -tags live -v ./internal/livetest
package livetest

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/browse"
	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/provider/brave"
	"github.com/frugalsh/frugal/internal/provider/browserless"
	"github.com/frugalsh/frugal/internal/provider/deepseek"
	"github.com/frugalsh/frugal/internal/provider/firecrawl"
	"github.com/frugalsh/frugal/internal/provider/goreadability"
	"github.com/frugalsh/frugal/internal/provider/jina"
	"github.com/frugalsh/frugal/internal/provider/marginalia"
	"github.com/frugalsh/frugal/internal/provider/searxng"
	"github.com/frugalsh/frugal/internal/provider/serper"
	"github.com/frugalsh/frugal/internal/provider/wikipedia"
	"github.com/frugalsh/frugal/internal/provider/youcom"
	"github.com/frugalsh/frugal/internal/provider/youcomfree"
	"github.com/frugalsh/frugal/internal/search"
)

const (
	query     = "golang"
	targetURL = "https://example.com/"
)

func gate(t *testing.T, env ...string) []string {
	t.Helper()
	if os.Getenv("FRUGAL_LIVE") != "1" {
		t.Skip("set FRUGAL_LIVE=1 to run live provider smoke tests")
	}
	var vals []string
	for _, e := range env {
		v := os.Getenv(e)
		if v == "" {
			t.Skipf("%s not set; skipping", e)
		}
		vals = append(vals, v)
	}
	return vals
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return c
}

func checkSearch(t *testing.T, s search.Searcher) {
	t.Helper()
	res, err := s.Search(ctx(t), search.Query{Text: query, MaxResults: 1})
	if err != nil {
		t.Fatalf("%s: %v", s.Name(), err)
	}
	if len(res.Items) == 0 {
		t.Fatalf("%s: zero results for %q", s.Name(), query)
	}
	t.Logf("%s ok: %d result(s), cost_usd=%v, first=%q", s.Name(), len(res.Items), res.CostUSD, res.Items[0].URL)
}

func checkExtract(t *testing.T, e extract.Extractor) {
	t.Helper()
	res, err := e.Extract(ctx(t), extract.Query{URL: targetURL})
	if err != nil {
		t.Fatalf("%s: %v", e.Name(), err)
	}
	if len(res.Markdown)+len(res.Text)+len(res.HTML) == 0 {
		t.Fatalf("%s: empty extract", e.Name())
	}
	t.Logf("%s ok: cost_usd=%v", e.Name(), res.CostUSD)
}

func checkBrowse(t *testing.T, b browse.Browser) {
	t.Helper()
	res, err := b.Render(ctx(t), browse.Query{URL: targetURL})
	if err != nil {
		t.Fatalf("%s: %v", b.Name(), err)
	}
	if len(res.HTML)+len(res.Text) == 0 {
		t.Fatalf("%s: empty render", b.Name())
	}
	t.Logf("%s ok: cost_usd=%v", b.Name(), res.CostUSD)
}

// Keyless / free.
func TestLive_YouComFree(t *testing.T) {
	gate(t)
	checkSearch(t, youcomfree.New(youcomfree.DefaultBaseURL))
}
func TestLive_Marginalia(t *testing.T) {
	gate(t)
	checkSearch(t, marginalia.New(marginalia.DefaultBaseURL))
}
func TestLive_Wikipedia(t *testing.T) {
	gate(t)
	checkSearch(t, wikipedia.New(wikipedia.DefaultBaseURL))
}
func TestLive_GoReadability(t *testing.T) {
	gate(t)
	checkExtract(t, goreadability.New())
}
func TestLive_Jina(t *testing.T) {
	gate(t)
	checkExtract(t, jina.New(jina.DefaultBaseURL, os.Getenv("JINA_API_KEY"), 0))
}
func TestLive_SearXNG(t *testing.T) {
	v := gate(t, "SEARXNG_URL")
	checkSearch(t, searxng.New(v[0]))
}

// Keyed / paid: one call each. Prices are the config list prices.
func TestLive_Serper(t *testing.T) {
	v := gate(t, "SERPER_API_KEY")
	checkSearch(t, serper.New(v[0], serper.DefaultBaseURL, 0.001))
}
func TestLive_YouCom(t *testing.T) {
	v := gate(t, "YDC_API_KEY")
	checkSearch(t, youcom.New(v[0], youcom.DefaultBaseURL, 0.005))
}
func TestLive_Brave(t *testing.T) {
	v := gate(t, "BRAVE_API_KEY")
	checkSearch(t, brave.New(v[0], brave.DefaultBaseURL, 0.005))
}
func TestLive_DeepSeekSearch(t *testing.T) {
	v := gate(t, "DEEPSEEK_API_KEY")
	checkSearch(t, deepseek.NewSearch(v[0], deepseek.DefaultBaseURL, deepseek.DefaultCostPerCall))
}
func TestLive_DeepSeekFetch(t *testing.T) {
	v := gate(t, "DEEPSEEK_API_KEY")
	checkExtract(t, deepseek.NewFetch(v[0], deepseek.DefaultBaseURL, deepseek.DefaultCostPerCall))
}
func TestLive_Firecrawl(t *testing.T) {
	v := gate(t, "FIRECRAWL_API_KEY")
	checkExtract(t, firecrawl.New(v[0], firecrawl.DefaultBaseURL, 0.001))
}
func TestLive_Browserless(t *testing.T) {
	v := gate(t, "BROWSERLESS_TOKEN")
	checkBrowse(t, browserless.New(v[0], browserless.DefaultBaseURL, 0.002))
}
