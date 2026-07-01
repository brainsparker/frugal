package wikipedia

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

func TestSearch_HappyPath(t *testing.T) {
	var capturedUA string
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedUA = r.Header.Get("User-Agent")
		capturedQuery = r.URL.Query().Get("q")
		if !strings.HasPrefix(r.URL.Path, "/w/rest.php/v1/search/page") {
			t.Errorf("path should be the REST search endpoint, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "3" {
			t.Errorf("limit: got %q want 3", r.URL.Query().Get("limit"))
		}
		_, _ = w.Write([]byte(`{
		  "pages": [
		    {"key":"CrewAI","title":"CrewAI","excerpt":"Crew<span class=\"searchmatch\">AI</span> is an open-source <span class=\"searchmatch\">framework</span> &amp; platform","description":"Open-source AI agent framework"},
		    {"key":"AI_agent","title":"AI agent","excerpt":"","description":"Autonomous artificial intelligence agent"}
		  ]
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.Search(context.Background(), search.Query{Text: "AI agent framework", MaxResults: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(res.Items))
	}
	// Highlight spans stripped, entities unescaped.
	if res.Items[0].Snippet != "CrewAI is an open-source framework & platform" {
		t.Errorf("snippet not cleaned: %q", res.Items[0].Snippet)
	}
	// Empty excerpt falls back to the short description.
	if res.Items[1].Snippet != "Autonomous artificial intelligence agent" {
		t.Errorf("empty excerpt should use description; got %q", res.Items[1].Snippet)
	}
	if res.Items[0].URL != srv.URL+"/wiki/CrewAI" {
		t.Errorf("URL: got %q", res.Items[0].URL)
	}
	if res.CostUSD != 0 {
		t.Errorf("CostUSD must be 0; got %v", res.CostUSD)
	}
	// Etiquette: User-Agent must be set and identify us.
	if !strings.Contains(capturedUA, "frugal") {
		t.Errorf("User-Agent should identify as frugal; got %q", capturedUA)
	}
	if capturedQuery != "AI agent framework" {
		t.Errorf("q param: got %q", capturedQuery)
	}
}

func TestSearch_SubpageKeyKeepsSlash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"pages":[{"key":"Foo/Bar","title":"Foo/Bar","excerpt":"x","description":""}]}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	res, err := c.Search(context.Background(), search.Query{Text: "foo"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Items[0].URL != srv.URL+"/wiki/Foo/Bar" {
		t.Errorf("subpage slash must survive escaping; got %q", res.Items[0].URL)
	}
}

func TestSearch_5xxRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"pages":[{"key":"OK","title":"OK","excerpt":"hit","description":""}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err != nil {
		t.Fatalf("expected retry to recover, got %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 attempts, got %d", calls.Load())
	}
	if len(res.Items) != 1 || res.Items[0].Title != "OK" {
		t.Errorf("unexpected result: %+v", res.Items)
	}
}

func TestSearch_4xxIsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`blocked`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err == nil {
		t.Fatalf("expected error on 403")
	}
	if !routing.IsPermanent(err) {
		t.Errorf("403 must classify as permanent; got %v", err)
	}
}

func TestSearch_NetworkErrorIsTransient(t *testing.T) {
	c := New("http://127.0.0.1:1")
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err == nil {
		t.Fatalf("expected network error")
	}
	if !routing.IsTransient(err) {
		t.Errorf("network failure must classify as transient; got %v", err)
	}
}

func TestSearch_EmptyQueryIsPermanent(t *testing.T) {
	c := New("http://example.invalid")
	_, err := c.Search(context.Background(), search.Query{})
	if err == nil {
		t.Fatalf("expected error for empty query")
	}
	if !routing.IsPermanent(err) {
		t.Errorf("empty-query error should be permanent; got %v", err)
	}
	var typed *routing.Error
	if !errors.As(err, &typed) || typed.Provider != "wikipedia" {
		t.Errorf("expected *routing.Error from wikipedia; got %v", err)
	}
}

func TestSearch_FreshnessServedBestEffortWithWarning(t *testing.T) {
	// The REST endpoint has no time-window parameter, so the query still
	// runs — a hard decline would break zero-key installs where this
	// driver and marginalia are the whole chain — and the dropped window
	// is disclosed via Results.Warnings for the agent to act on.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"pages":[{"key":"Go","title":"Go","excerpt":"language","description":""}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.Search(context.Background(), search.Query{Text: "latest go release", Freshness: "week"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("freshness query must still be served; got %d items", len(res.Items))
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "freshness window ignored") {
		t.Errorf("expected a freshness-ignored warning; got %v", res.Warnings)
	}

	// Without a freshness constraint there is nothing to warn about.
	res, err = c.Search(context.Background(), search.Query{Text: "latest go release"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("no warning expected without freshness; got %v", res.Warnings)
	}
}

func TestNameAndCost(t *testing.T) {
	c := New("")
	if c.Name() != "wikipedia" {
		t.Errorf("Name: got %q want wikipedia", c.Name())
	}
	if c.CostPerCall() != 0 {
		t.Errorf("CostPerCall must be 0; got %v", c.CostPerCall())
	}
}

func TestNew_DefaultsBaseURL(t *testing.T) {
	c := New("")
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL default: got %q want %q", c.baseURL, DefaultBaseURL)
	}
}

func TestNew_TrimsTrailingSlash(t *testing.T) {
	c := New("https://en.wikipedia.org/")
	if c.baseURL != "https://en.wikipedia.org" {
		t.Errorf("baseURL: got %q, want trimmed", c.baseURL)
	}
}
