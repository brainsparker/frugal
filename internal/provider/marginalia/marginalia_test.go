package marginalia

import (
	"context"
	"errors"
	"fmt"
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
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedUA = r.Header.Get("User-Agent")
		capturedPath = r.URL.Path
		if !strings.HasPrefix(r.URL.Path, "/search/") {
			t.Errorf("path should start with /search/, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("count") != "3" {
			t.Errorf("count: got %q want 3", r.URL.Query().Get("count"))
		}
		_, _ = w.Write([]byte(`{
		  "results": [
		    {"title":"Marginalia Search","url":"https://search.marginalia.nu","description":"An indie search engine"},
		    {"title":"LWN","url":"https://lwn.net","description":"Linux news"}
		  ]
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.Search(context.Background(), search.Query{Text: "linux kernel news", MaxResults: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(res.Items))
	}
	if res.Items[0].Title != "Marginalia Search" || res.Items[0].Snippet != "An indie search engine" {
		t.Errorf("item[0] = %+v", res.Items[0])
	}
	if res.CostUSD != 0 {
		t.Errorf("CostUSD must be 0; got %v", res.CostUSD)
	}
	// Etiquette: User-Agent must be set and identify us.
	if !strings.Contains(capturedUA, "frugal") {
		t.Errorf("User-Agent should identify as frugal; got %q", capturedUA)
	}
	// Query lands in the path (httptest decodes %20 → space when reading
	// r.URL.Path; we just check the decoded form contains the query).
	if !strings.Contains(capturedPath, "linux kernel news") {
		t.Errorf("path should contain the query; got %s", capturedPath)
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
		_, _ = w.Write([]byte(`{"results":[{"title":"OK","url":"https://x","description":"hit"}]}`))
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
	if !errors.As(err, &typed) || typed.Provider != "marginalia" {
		t.Errorf("expected *routing.Error from marginalia; got %v", err)
	}
}

func TestNameAndCost(t *testing.T) {
	c := New("")
	if c.Name() != "marginalia" {
		t.Errorf("Name: got %q want marginalia", c.Name())
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
	c := New("https://api.marginalia.nu/")
	if c.baseURL != "https://api.marginalia.nu" {
		t.Errorf("baseURL: got %q, want trimmed", c.baseURL)
	}
}

func TestSearch_OversizedResponseBodyIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fmt.Sprintf(`{"results":[{"title":"%s`, strings.Repeat("x", int(maxResponseBodyBytes)+2048))))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err == nil {
		t.Fatalf("expected decode error for oversized body")
	}
	if !routing.IsTransient(err) {
		t.Fatalf("expected transient error, got %v", err)
	}
}
