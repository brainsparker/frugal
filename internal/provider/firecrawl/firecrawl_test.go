package firecrawl

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/routing"
)

func TestExtract_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/scrape" {
			t.Errorf("path: got %s want /v1/scrape", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer fc-test" {
			t.Errorf("Authorization: got %q want %q", r.Header.Get("Authorization"), "Bearer fc-test")
		}
		body, _ := io.ReadAll(r.Body)
		var req firecrawlRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.URL != "https://example.com/article" {
			t.Errorf("url: got %q", req.URL)
		}
		_, _ = w.Write([]byte(`{
		  "data": {
		    "markdown": "# Test Article\n\nFirst paragraph.",
		    "html": "<h1>Test Article</h1><p>First paragraph.</p>",
		    "links": ["https://example.com/next"],
		    "metadata": {"title": "Test Article", "author": "Jane Doe"}
		  }
		}`))
	}))
	defer srv.Close()

	c := New("fc-test", srv.URL, 0.001)
	res, err := c.Extract(context.Background(), extract.Query{
		URL:     "https://example.com/article",
		Formats: []string{"markdown", "html"},
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Title != "Test Article" {
		t.Errorf("Title: got %q", res.Title)
	}
	if !strings.Contains(res.Markdown, "First paragraph") {
		t.Errorf("Markdown: got %q", res.Markdown)
	}
	if !strings.Contains(res.HTML, "<h1>Test Article") {
		t.Errorf("HTML: got %q", res.HTML)
	}
	if res.Byline != "Jane Doe" {
		t.Errorf("Byline: got %q want Jane Doe", res.Byline)
	}
	if len(res.Links) != 1 || res.Links[0] != "https://example.com/next" {
		t.Errorf("Links: got %v", res.Links)
	}
	if res.CostUSD != 0.001 {
		t.Errorf("CostUSD: got %v want 0.001", res.CostUSD)
	}
}

func TestExtract_DefaultFormatsIsMarkdown(t *testing.T) {
	var captured firecrawlRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_, _ = w.Write([]byte(`{"data":{"markdown":"x","metadata":{}}}`))
	}))
	defer srv.Close()
	c := New("k", srv.URL, 0.001)
	if _, err := c.Extract(context.Background(), extract.Query{URL: "https://x"}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(captured.Formats) != 1 || captured.Formats[0] != "markdown" {
		t.Errorf("Formats default: got %v want [markdown]", captured.Formats)
	}
}

func TestExtract_401IsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()
	c := New("bad", srv.URL, 0.001)
	_, err := c.Extract(context.Background(), extract.Query{URL: "https://x"})
	if err == nil {
		t.Fatalf("expected error on 401")
	}
	if !routing.IsPermanent(err) {
		t.Errorf("401 must classify as permanent; got %v", err)
	}
}

func TestExtract_5xxRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"markdown":"recovered","metadata":{}}}`))
	}))
	defer srv.Close()
	c := New("k", srv.URL, 0.001)
	res, err := c.Extract(context.Background(), extract.Query{URL: "https://x"})
	if err != nil {
		t.Fatalf("expected retry to recover; got %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 attempts; got %d", calls.Load())
	}
	if res.Markdown != "recovered" {
		t.Errorf("unexpected markdown after retry: %q", res.Markdown)
	}
}

func TestExtract_NetworkErrorIsTransient(t *testing.T) {
	c := New("k", "http://127.0.0.1:1", 0.001)
	c.httpClient.Timeout = 200 * time.Millisecond
	_, err := c.Extract(context.Background(), extract.Query{URL: "https://x"})
	if err == nil {
		t.Fatalf("expected network error")
	}
	if !routing.IsTransient(err) {
		t.Errorf("network failure must classify as transient; got %v", err)
	}
}

func TestExtract_EmptyURLIsPermanent(t *testing.T) {
	c := New("k", "http://example.invalid", 0.001)
	_, err := c.Extract(context.Background(), extract.Query{})
	if err == nil {
		t.Fatalf("expected error for empty URL")
	}
	if !routing.IsPermanent(err) {
		t.Errorf("empty URL should be permanent; got %v", err)
	}
	var typed *routing.Error
	if !errors.As(err, &typed) || typed.Provider != "firecrawl" {
		t.Errorf("expected *routing.Error from firecrawl; got %v", err)
	}
}

func TestExtract_ResponseBodyTooLargeIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"markdown":"` + strings.Repeat("a", int(maxResponseBodyBytes)+1) + `","metadata":{}}}`))
	}))
	defer srv.Close()

	c := New("k", srv.URL, 0.001)
	_, err := c.Extract(context.Background(), extract.Query{URL: "https://x"})
	if err == nil {
		t.Fatalf("expected decode error for oversized response body")
	}
	if !routing.IsTransient(err) {
		t.Errorf("oversized response should classify as transient; got %v", err)
	}
}

func TestNameAndCost(t *testing.T) {
	c := New("k", "", 0.001)
	if c.Name() != "firecrawl" {
		t.Errorf("Name: got %q want firecrawl", c.Name())
	}
	if c.CostPerCall() != 0.001 {
		t.Errorf("CostPerCall: got %v want 0.001", c.CostPerCall())
	}
}

func TestNew_DefaultsBaseURL(t *testing.T) {
	c := New("k", "", 0.001)
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL default: got %q want %q", c.baseURL, DefaultBaseURL)
	}
}
