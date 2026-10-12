package jina

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/routing"
)

// okBody is a trimmed copy of a real Reader response for a static page.
const okBody = `{"code":200,"status":20000,"data":{"title":"Example Domain","description":"","url":"https://example.com/","content":"# Example Domain\n\nThis domain is for use in documentation examples.\n\n[Learn more](https://iana.org/domains/example)","publishedTime":"Tue, 15 Sep 2026 23:38:37 GMT","httpStatus":200,"httpStatusText":"OK","usage":{"tokens":33}}}`

func TestExtract_HappyPathKeyless(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reader's URL form: endpoint + "/" + the full target URL.
		if r.URL.Path != "/https://example.com/article" {
			t.Errorf("path: got %q want /https://example.com/article", r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept: got %q", r.Header.Get("Accept"))
		}
		if r.Header.Get("X-Return-Format") != "markdown" {
			t.Errorf("X-Return-Format: got %q want markdown", r.Header.Get("X-Return-Format"))
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("keyless client must not send Authorization; got %q", r.Header.Get("Authorization"))
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "frugal") {
			t.Errorf("UA should identify as frugal; got %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okBody))
	}))
	defer srv.Close()

	c := New(srv.URL, "", 0)
	res, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/article"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Title != "Example Domain" {
		t.Errorf("Title: got %q", res.Title)
	}
	if !strings.Contains(res.Markdown, "# Example Domain") {
		t.Errorf("Markdown: got %q", res.Markdown)
	}
	if res.Text != res.Markdown {
		t.Errorf("Text should mirror Markdown for a markdown render; got %q", res.Text)
	}
	if res.HTML != "" {
		t.Errorf("HTML must be empty on a markdown render; got %q", res.HTML)
	}
	if res.CostUSD != 0 {
		t.Errorf("CostUSD: got %v want 0", res.CostUSD)
	}
	if c.Keyed() {
		t.Errorf("Keyed() must be false without a key")
	}
}

func TestExtract_KeySentAsBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jina_test" {
			t.Errorf("Authorization: got %q want %q", r.Header.Get("Authorization"), "Bearer jina_test")
		}
		_, _ = w.Write([]byte(okBody))
	}))
	defer srv.Close()

	c := New(srv.URL, " jina_test ", 0.0002)
	res, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !c.Keyed() {
		t.Errorf("Keyed() must be true with a key")
	}
	if res.CostUSD != 0.0002 {
		t.Errorf("CostUSD must echo the configured price; got %v", res.CostUSD)
	}
}

func TestExtract_ReturnFormatFollowsRequest(t *testing.T) {
	cases := []struct {
		formats []string
		want    string
	}{
		{nil, "markdown"},
		{[]string{"markdown"}, "markdown"},
		{[]string{"html", "markdown"}, "markdown"},
		{[]string{"html"}, "html"},
		{[]string{"text"}, "text"},
		{[]string{"html", "text"}, "markdown"},
		{[]string{"HTML"}, "html"},
	}
	for _, tc := range cases {
		if got := returnFormat(tc.formats); got != tc.want {
			t.Errorf("returnFormat(%v) = %q want %q", tc.formats, got, tc.want)
		}
	}

	// An html-only ask fills Result.HTML and leaves Markdown empty.
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Return-Format")
		_, _ = w.Write([]byte(`{"code":200,"data":{"title":"T","content":"<h1>T</h1><p>body</p>","httpStatus":200}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "", 0)
	res, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/", Formats: []string{"html"}})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if gotHeader != "html" {
		t.Errorf("X-Return-Format: got %q want html", gotHeader)
	}
	if !strings.Contains(res.HTML, "<h1>T</h1>") || res.Markdown != "" {
		t.Errorf("html render should populate HTML only; got %+v", res)
	}
}

func TestExtract_TargetGoneIsFatal(t *testing.T) {
	// Reader answers 200 with the target's status in data.httpStatus.
	// A dead page is dead for every extractor: Fatal stops the chain.
	for _, st := range []int{404, 410} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"code":200,"data":{"title":"Example Domain","content":"fallback chrome text","warning":"Target URL returned error 404: Not Found","httpStatus":` + itoa(st) + `}}`))
		}))
		c := New(srv.URL, "", 0)
		_, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/missing"})
		srv.Close()
		if err == nil {
			t.Fatalf("status %d: expected error", st)
		}
		if !routing.IsFatal(err) {
			t.Errorf("status %d must classify as fatal; got %v", st, err)
		}
		var re *routing.Error
		if !errors.As(err, &re) || re.Status != st {
			t.Errorf("status %d: error should carry the target status; got %v", st, err)
		}
		if !strings.Contains(err.Error(), "Not Found") {
			t.Errorf("status %d: error should surface Reader's warning; got %v", st, err)
		}
	}
}

func TestExtract_TargetForbiddenIsPermanentNotFatal(t *testing.T) {
	// A 403 is often an anti-bot wall a different renderer can pass, so
	// the chain must keep falling through rather than stop.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"data":{"title":"","content":"","httpStatus":403}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "", 0)
	_, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/walled"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if routing.IsFatal(err) {
		t.Errorf("403 must not be fatal; got %v", err)
	}
	if !routing.IsPermanent(err) {
		t.Errorf("403 must classify as permanent; got %v", err)
	}
	if !strings.Contains(err.Error(), "http 403") {
		t.Errorf("error should name the target status when Reader gives no warning; got %v", err)
	}
}

func TestExtract_EmptyContentIsPermanent(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"code":200,"data":{"title":"Blank","content":"   \n  ","httpStatus":200}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "", 0)
	_, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/blank"})
	if err == nil {
		t.Fatalf("expected error on empty content")
	}
	if !routing.IsPermanent(err) || routing.IsFatal(err) {
		t.Errorf("empty content must be permanent (fall through, no retry); got %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("permanent errors must not retry; got %d attempts", calls.Load())
	}
}

func TestExtract_429IsTransientWithStatus(t *testing.T) {
	// The routing guard opens its cooldown window on a *routing.Error
	// with Status 429, so the driver must preserve the code.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":429,"message":"rate limit exceeded"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "", 0)
	_, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/"})
	if err == nil {
		t.Fatalf("expected error on 429")
	}
	if !routing.IsTransient(err) {
		t.Errorf("429 must classify as transient; got %v", err)
	}
	var re *routing.Error
	if !errors.As(err, &re) || re.Status != http.StatusTooManyRequests {
		t.Errorf("error must carry status 429 for the cooldown guard; got %v", err)
	}
}

func TestExtract_5xxRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(okBody))
	}))
	defer srv.Close()
	c := New(srv.URL, "", 0)
	res, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/"})
	if err != nil {
		t.Fatalf("expected retry to recover; got %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 attempts; got %d", calls.Load())
	}
	if res.Title != "Example Domain" {
		t.Errorf("unexpected result after retry: %+v", res)
	}
}

func TestExtract_422IsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":422,"message":"invalid url"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "", 0)
	_, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/"})
	if err == nil || !routing.IsPermanent(err) || routing.IsFatal(err) {
		t.Errorf("422 from Reader must be permanent, not fatal; got %v", err)
	}
}

func TestExtract_BadJSONIsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	c := New(srv.URL, "", 0)
	_, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/"})
	if err == nil || !routing.IsPermanent(err) {
		t.Errorf("undecodable body must be permanent; got %v", err)
	}
}

func TestExtract_NetworkErrorIsTransient(t *testing.T) {
	c := New("http://127.0.0.1:1", "", 0)
	c.httpClient.Timeout = 200 * time.Millisecond
	_, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/"})
	if err == nil {
		t.Fatalf("expected network error")
	}
	if !routing.IsTransient(err) {
		t.Errorf("network failure must classify as transient; got %v", err)
	}
}

func TestExtract_UnusableURLIsFatal(t *testing.T) {
	c := New("http://127.0.0.1:1", "", 0)
	for _, u := range []string{"", "   ", "not a url", "example.com/no-scheme"} {
		_, err := c.Extract(context.Background(), extract.Query{URL: u})
		if err == nil || !routing.IsFatal(err) {
			t.Errorf("url %q must be fatal before any request; got %v", u, err)
		}
	}
}

func TestNew_DefaultsAndTrimming(t *testing.T) {
	c := New("", "", 0)
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL default = %q want %q", c.baseURL, DefaultBaseURL)
	}
	c = New("https://reader.internal/", "", 0)
	if c.baseURL != "https://reader.internal" {
		t.Errorf("trailing slash should be trimmed; got %q", c.baseURL)
	}
	if c.Name() != "jina" {
		t.Errorf("Name() = %q want jina", c.Name())
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
