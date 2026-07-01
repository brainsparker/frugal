package goreadability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/routing"
)

// articleHTML is a minimal but Readability-friendly page (title, byline,
// real prose). go-readability needs at least a few paragraphs of real
// content to pull out the article body, so we use a substantive blob.
const articleHTML = `<!doctype html><html><head><title>Test Article</title></head><body>
<header>nav and stuff that Readability should strip</header>
<article>
  <h1>Test Article</h1>
  <p class="byline">By Jane Doe</p>
  <p>This is the first substantive paragraph of the test article. It contains enough real-looking prose that Readability's heuristic considers it the main content block. Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore.</p>
  <p>This is the second paragraph. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum.</p>
  <p>Third paragraph follows. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum.</p>
</article>
<footer>copyright nav cruft</footer>
</body></html>`

func TestExtract_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("User-Agent"), "frugal") {
			t.Errorf("UA should identify as frugal; got %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(articleHTML))
	}))
	defer srv.Close()

	c := New()
	res, err := c.Extract(context.Background(), extract.Query{URL: srv.URL + "/article"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Title != "Test Article" {
		t.Errorf("Title: got %q want %q", res.Title, "Test Article")
	}
	if !strings.Contains(res.Text, "first substantive paragraph") {
		t.Errorf("Text should contain article body; got %q", res.Text)
	}
	// Chrome (nav, footer) should be stripped from the extracted HTML.
	if strings.Contains(res.HTML, "copyright nav cruft") {
		t.Errorf("Readability should strip footer chrome; HTML still contains it")
	}
	if res.CostUSD != 0 {
		t.Errorf("CostUSD must be 0; got %v", res.CostUSD)
	}
}

func TestExtract_EmptyContentIsPermanent(t *testing.T) {
	// A page with only a JS hook and no rendered text body — Readability
	// will return empty TextContent. Classify as Permanent so the router
	// falls through.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>SPA</title></head><body><div id="root"></div><script>renderApp()</script></body></html>`))
	}))
	defer srv.Close()

	c := New()
	_, err := c.Extract(context.Background(), extract.Query{URL: srv.URL})
	if err == nil {
		t.Fatalf("expected permanent error for empty-content page")
	}
	if !routing.IsPermanent(err) {
		t.Errorf("empty-content should classify as permanent (router fallthrough); got %v", err)
	}
}

func TestExtract_NonHTMLContentTypeIsPermanent(t *testing.T) {
	// A 200 PDF/binary body would parse into non-empty garbage text and
	// "win" the chain. Permanent (not fatal) so the router falls through
	// to a rendering provider that can handle the format.
	for _, ct := range []string{"application/pdf", "image/png", "application/json; charset=utf-8"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", ct)
			_, _ = w.Write([]byte("%PDF-1.7 binary-ish payload"))
		}))
		c := New()
		_, err := c.Extract(context.Background(), extract.Query{URL: srv.URL})
		srv.Close()
		if err == nil {
			t.Fatalf("expected error for Content-Type %q", ct)
		}
		if !routing.IsPermanent(err) {
			t.Errorf("Content-Type %q must classify as permanent; got %v", ct, err)
		}
		if routing.IsFatal(err) {
			t.Errorf("Content-Type %q must NOT be fatal (a rendering provider may handle it); got %v", ct, err)
		}
	}
}

func TestExtract_MissingContentTypeStillParses(t *testing.T) {
	// No Content-Type header at all: don't reject — let the parse step
	// judge. (httptest sniffs a type unless we set one explicitly, so
	// blank it via the header map before WriteHeader.)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header()["Content-Type"] = nil
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(articleHTML))
	}))
	defer srv.Close()

	c := New()
	res, err := c.Extract(context.Background(), extract.Query{URL: srv.URL})
	if err != nil {
		t.Fatalf("Extract without Content-Type: %v", err)
	}
	if res.Title != "Test Article" {
		t.Errorf("Title: got %q want %q", res.Title, "Test Article")
	}
}

func TestExtract_RelativeLinksResolveAgainstRedirectTarget(t *testing.T) {
	// A relative href in the article must resolve against the FINAL URL
	// after a redirect, not the URL the caller passed in.
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		page := strings.Replace(articleHTML,
			"<p>This is the second paragraph.",
			`<p><a href="/relative/page">relative link</a> This is the second paragraph.`, 1)
		_, _ = w.Write([]byte(page))
	}))
	defer final.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/article", http.StatusMovedPermanently)
	}))
	defer origin.Close()

	c := New()
	res, err := c.Extract(context.Background(), extract.Query{URL: origin.URL})
	if err != nil {
		t.Fatalf("Extract through redirect: %v", err)
	}
	if !strings.Contains(res.HTML, final.URL+"/relative/page") {
		t.Errorf("relative link should resolve against redirect target %s; HTML:\n%s", final.URL, res.HTML)
	}
	if strings.Contains(res.HTML, origin.URL+"/relative/page") {
		t.Errorf("relative link resolved against pre-redirect host %s", origin.URL)
	}
}

func TestExtract_5xxRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(articleHTML))
	}))
	defer srv.Close()

	c := New()
	res, err := c.Extract(context.Background(), extract.Query{URL: srv.URL})
	if err != nil {
		t.Fatalf("expected retry to recover; got %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 attempts; got %d", calls.Load())
	}
	if res.Title != "Test Article" {
		t.Errorf("unexpected title after retry: %q", res.Title)
	}
}

func TestExtract_4xxIsPermanent(t *testing.T) {
	// 403 is typically an anti-bot block — permanent for THIS extractor
	// (no retry) but NOT fatal: a rendering provider may get past it, so
	// the router must keep falling through.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := New()
	_, err := c.Extract(context.Background(), extract.Query{URL: srv.URL})
	if err == nil {
		t.Fatalf("expected error on 403")
	}
	if !routing.IsPermanent(err) {
		t.Errorf("403 must classify as permanent; got %v", err)
	}
	if routing.IsFatal(err) {
		t.Errorf("403 must NOT be fatal (a rendering provider may pass the block); got %v", err)
	}
}

func TestExtract_DeadTargetIsFatal(t *testing.T) {
	// 404/410 from the target site is a fact about the URL, not this
	// extractor — fatal stops the router from spending a paid scrape on
	// a dead link.
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		c := New()
		_, err := c.Extract(context.Background(), extract.Query{URL: srv.URL})
		srv.Close()
		if err == nil {
			t.Fatalf("expected error on %d", status)
		}
		if !routing.IsFatal(err) {
			t.Errorf("%d from the target must classify as fatal; got %v", status, err)
		}
	}
}

func TestExtract_NetworkErrorIsTransient(t *testing.T) {
	c := New()
	c.httpClient.Timeout = 200 * 1e6 // 200ms
	_, err := c.Extract(context.Background(), extract.Query{URL: "http://127.0.0.1:1"})
	if err == nil {
		t.Fatalf("expected network error")
	}
	if !routing.IsTransient(err) {
		t.Errorf("network failure must classify as transient; got %v", err)
	}
}

func TestExtract_EmptyURLIsFatal(t *testing.T) {
	c := New()
	_, err := c.Extract(context.Background(), extract.Query{})
	if err == nil {
		t.Fatalf("expected error for empty URL")
	}
	if !routing.IsFatal(err) {
		t.Errorf("empty URL should be fatal (no provider can extract nothing); got %v", err)
	}
}

func TestNameAndCost(t *testing.T) {
	c := New()
	if c.Name() != "goreadability" {
		t.Errorf("Name: got %q want goreadability", c.Name())
	}
	if c.CostPerCall() != 0 {
		t.Errorf("CostPerCall must be 0; got %v", c.CostPerCall())
	}
}
