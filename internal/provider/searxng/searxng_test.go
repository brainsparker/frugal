package searxng

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

func TestNew_EmptyBaseURLReturnsNil(t *testing.T) {
	if c := New(""); c != nil {
		t.Errorf("New(\"\") = %v, want nil (no endpoint → no driver)", c)
	}
	if c := New("   "); c != nil {
		t.Errorf("New(whitespace) = %v, want nil", c)
	}
}

func TestNew_TrimsTrailingSlash(t *testing.T) {
	c := New("https://search.example.com/")
	if c.baseURL != "https://search.example.com" {
		t.Errorf("baseURL: got %q, want trimmed", c.baseURL)
	}
}

func TestSearch_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Errorf("path: got %s want /search", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if r.PostForm.Get("q") != "linux kernel news" {
			t.Errorf("q: got %q", r.PostForm.Get("q"))
		}
		if r.PostForm.Get("format") != "json" {
			t.Errorf("format: got %q want json", r.PostForm.Get("format"))
		}
		_, _ = w.Write([]byte(`{
		  "results":[
		    {"title":"LWN","url":"https://lwn.net","content":"kernel news"},
		    {"title":"phoronix","url":"https://phoronix.com","snippet":"benchmarks","publishedDate":"2026-05-01"}
		  ]
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.Search(context.Background(), search.Query{Text: "linux kernel news", MaxResults: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(res.Items))
	}
	if res.Items[0].Title != "LWN" || res.Items[0].Snippet != "kernel news" {
		t.Errorf("item[0] = %+v", res.Items[0])
	}
	if res.Items[1].Snippet != "benchmarks" || res.Items[1].PublishedAt != "2026-05-01" {
		t.Errorf("item[1] = %+v", res.Items[1])
	}
	if res.CostUSD != 0 {
		t.Errorf("CostUSD should be 0 for free local provider; got %v", res.CostUSD)
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
		_, _ = w.Write([]byte(`{"results":[{"title":"OK","url":"https://x"}]}`))
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

func TestSearch_NetworkErrorIsTransient(t *testing.T) {
	c := New("http://127.0.0.1:1")
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err == nil {
		t.Fatalf("expected network error")
	}
	if !routing.IsTransient(err) {
		t.Errorf("network failure should classify as transient; got %v", err)
	}
}

func TestSearch_ResponseBodyTooLarge(t *testing.T) {
	over := strings.Repeat(" ", maxResponseBodyBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(over + `{"results":[{"title":"Too late","url":"https://x"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err == nil {
		t.Fatalf("expected decode failure when response body exceeds cap")
	}
	if !routing.IsTransient(err) {
		t.Fatalf("expected transient error for oversized response, got %v", err)
	}
}

func TestNameAndCost(t *testing.T) {
	c := New("https://x.example")
	if c.Name() != "searxng" {
		t.Errorf("Name: got %q want searxng", c.Name())
	}
	if c.CostPerCall() != 0 {
		t.Errorf("CostPerCall must be 0 for the free/local provider; got %v", c.CostPerCall())
	}
}
