package serper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

func TestSearch_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-API-KEY") != "serper-test" {
			t.Errorf("X-API-KEY: got %q", r.Header.Get("X-API-KEY"))
		}
		body, _ := io.ReadAll(r.Body)
		var got serperRequest
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Q != "best ramen in tokyo" {
			t.Errorf("q: got %q", got.Q)
		}
		if got.Num != 3 {
			t.Errorf("num: got %d want 3", got.Num)
		}
		_, _ = w.Write([]byte(`{
		  "organic":[
		    {"title":"Tokyo Ramen Guide","link":"https://example.com/ramen","snippet":"Top 10 ramen shops..."},
		    {"title":"Eater","link":"https://eater.com","snippet":"Best ramen in Tokyo for 2026","date":"2026-04-01"}
		  ]
		}`))
	}))
	defer srv.Close()

	c := New("serper-test", srv.URL, 0.0003)
	res, err := c.Search(context.Background(), search.Query{Text: "best ramen in tokyo", MaxResults: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(res.Items))
	}
	if res.Items[1].URL != "https://eater.com" || res.Items[1].PublishedAt != "2026-04-01" {
		t.Errorf("item[1] = %+v", res.Items[1])
	}
	if res.CostUSD != 0.0003 {
		t.Errorf("cost: got %v want 0.0003", res.CostUSD)
	}
}

func TestSearch_FreshnessMapsToTBS(t *testing.T) {
	var captured serperRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_, _ = w.Write([]byte(`{"organic":[]}`))
	}))
	defer srv.Close()

	c := New("k", srv.URL, 0.0003)
	if _, err := c.Search(context.Background(), search.Query{Text: "x", Freshness: "week"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if captured.TBS != "qdr:w" {
		t.Errorf("TBS for week: got %q want qdr:w", captured.TBS)
	}
}

func TestSearch_HTTPErrorSurfacedWithSnippet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"rate limit exceeded"}`))
	}))
	defer srv.Close()
	c := New("k", srv.URL, 0.0003)
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err == nil {
		t.Fatalf("expected error on 403")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("error should include status and snippet, got: %v", err)
	}
	if !routing.IsPermanent(err) {
		t.Errorf("403 must classify as permanent; got %v", err)
	}
}

func TestSearch_5xxClassifiedTransientAndRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("upstream blip"))
			return
		}
		_, _ = w.Write([]byte(`{"organic":[{"title":"recovered","link":"https://x","snippet":"hit"}]}`))
	}))
	defer srv.Close()
	c := New("k", srv.URL, 0.0003)
	res, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err != nil {
		t.Fatalf("expected retry to recover; got %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 attempts; got %d", calls.Load())
	}
	if len(res.Items) != 1 || res.Items[0].Title != "recovered" {
		t.Errorf("unexpected result after retry: %+v", res.Items)
	}
}

func TestNameAndCost(t *testing.T) {
	c := New("k", "", 0.0003)
	if c.Name() != "serper" {
		t.Errorf("Name: got %q", c.Name())
	}
	if c.CostPerCall() != 0.0003 {
		t.Errorf("CostPerCall: got %v", c.CostPerCall())
	}
}

func TestSearch_OversizedResponseIsRejected(t *testing.T) {
	largeSnippet := strings.Repeat("a", maxResponseBodyBytes)
	payload := fmt.Sprintf(`{"organic":[{"title":"x","link":"https://x","snippet":"%s"}]}`, largeSnippet)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	c := New("k", srv.URL, 0.0003)
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err == nil {
		t.Fatalf("expected oversized response error")
	}
	if !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("expected size limit error, got: %v", err)
	}
	if !routing.IsTransient(err) {
		t.Fatalf("expected transient classification, got: %v", err)
	}
}
