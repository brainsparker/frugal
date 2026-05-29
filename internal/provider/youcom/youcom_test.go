package youcom

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

func TestSearch_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-API-Key") != "ydc-test" {
			t.Errorf("X-API-Key: got %q want ydc-test", r.Header.Get("X-API-Key"))
		}
		q := r.URL.Query()
		if q.Get("query") != "best ramen in tokyo" {
			t.Errorf("query: got %q", q.Get("query"))
		}
		if q.Get("num_web_results") != "3" {
			t.Errorf("num_web_results: got %q want 3", q.Get("num_web_results"))
		}
		_, _ = w.Write([]byte(`{
		  "hits": [
		    {"title":"Tokyo Ramen Guide","url":"https://example.com/ramen","description":"Top 10 ramen shops..."},
		    {"title":"Eater","url":"https://eater.com","snippets":["Best ramen in Tokyo for 2026"],"page_age":"2026-04-01"}
		  ]
		}`))
	}))
	defer srv.Close()

	c := New("ydc-test", srv.URL, 0.005)
	res, err := c.Search(context.Background(), search.Query{Text: "best ramen in tokyo", MaxResults: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(res.Items))
	}
	if res.Items[0].Title != "Tokyo Ramen Guide" || res.Items[0].Snippet != "Top 10 ramen shops..." {
		t.Errorf("item[0] = %+v", res.Items[0])
	}
	// snippets[] fallback when description is empty
	if res.Items[1].Snippet != "Best ramen in Tokyo for 2026" {
		t.Errorf("item[1] snippet should fall back to snippets[0]; got %q", res.Items[1].Snippet)
	}
	if res.Items[1].PublishedAt != "2026-04-01" {
		t.Errorf("item[1] published: got %q", res.Items[1].PublishedAt)
	}
	if res.CostUSD != 0.005 {
		t.Errorf("CostUSD: got %v want 0.005", res.CostUSD)
	}
}

func TestSearch_FreshnessPassesThrough(t *testing.T) {
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.URL.Query().Get("freshness")
		_, _ = w.Write([]byte(`{"hits":[]}`))
	}))
	defer srv.Close()
	c := New("k", srv.URL, 0.005)
	if _, err := c.Search(context.Background(), search.Query{Text: "x", Freshness: "week"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if captured != "week" {
		t.Errorf("freshness param: got %q want week", captured)
	}
}

func TestSearch_401IsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()
	c := New("bad", srv.URL, 0.005)
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err == nil {
		t.Fatalf("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should include status; got: %v", err)
	}
	if !routing.IsPermanent(err) {
		t.Errorf("401 must classify as permanent; got %v", err)
	}
}

func TestSearch_429ClassifiedTransient(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()
	c := New("k", srv.URL, 0.005)
	c.httpClient.Timeout = 2 * time.Second
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err == nil {
		t.Fatalf("expected transient error after retries exhausted")
	}
	if !routing.IsTransient(err) {
		t.Errorf("429 must classify as transient; got %v", err)
	}
	if calls.Load() < 2 {
		t.Errorf("expected >=2 attempts on transient 429; got %d", calls.Load())
	}
}

func TestSearch_NetworkErrorIsTransient(t *testing.T) {
	c := New("k", "http://127.0.0.1:1", 0.005)
	c.httpClient.Timeout = 200 * time.Millisecond
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err == nil {
		t.Fatalf("expected network error")
	}
	if !routing.IsTransient(err) {
		t.Errorf("network failure must classify as transient; got %v", err)
	}
}

func TestSearch_OversizedJSONBodyIsTransient(t *testing.T) {
	oversizedHit := `{"title":"t","url":"https://x","description":"` + strings.Repeat("a", (2<<20)+1024) + `"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"hits":[` + oversizedHit + `]}`))
	}))
	defer srv.Close()

	c := New("k", srv.URL, 0.005)
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err == nil {
		t.Fatalf("expected decode error for oversized response body")
	}
	if !routing.IsTransient(err) {
		t.Errorf("oversized decode error must classify as transient; got %v", err)
	}
}

func TestSearch_RetrySucceedsAfterTransient(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`upstream blip`))
			return
		}
		_, _ = w.Write([]byte(`{"hits":[{"title":"OK","url":"https://x","description":"hit"}]}`))
	}))
	defer srv.Close()
	c := New("k", srv.URL, 0.005)
	res, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err != nil {
		t.Fatalf("expected retry to recover; got %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected exactly 2 attempts (1 fail + 1 success); got %d", calls.Load())
	}
	if len(res.Items) != 1 || res.Items[0].Title != "OK" {
		t.Errorf("unexpected results after retry: %+v", res.Items)
	}
}

func TestSearch_EmptyQueryIsPermanent(t *testing.T) {
	c := New("k", "http://example.invalid", 0.005)
	_, err := c.Search(context.Background(), search.Query{})
	if err == nil {
		t.Fatalf("expected error for empty query")
	}
	if !routing.IsPermanent(err) {
		t.Errorf("empty-query error should be permanent; got %v", err)
	}
	var typed *routing.Error
	if !errors.As(err, &typed) || typed.Provider != "youcom" {
		t.Errorf("expected *routing.Error from youcom; got %v", err)
	}
}

func TestNameAndCost(t *testing.T) {
	c := New("k", "", 0.005)
	if c.Name() != "youcom" {
		t.Errorf("Name: got %q want youcom", c.Name())
	}
	if c.CostPerCall() != 0.005 {
		t.Errorf("CostPerCall: got %v want 0.005", c.CostPerCall())
	}
}

func TestNew_DefaultsBaseURL(t *testing.T) {
	c := New("k", "", 0.005)
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL default: got %q want %q", c.baseURL, DefaultBaseURL)
	}
}
