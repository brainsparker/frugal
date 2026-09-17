package brave

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

func TestSearch_HappyPath(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method: got %s want GET", r.Method)
		}
		if r.URL.Path != "/res/v1/web/search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-Subscription-Token") != "brave-test" {
			t.Errorf("X-Subscription-Token: got %q", r.Header.Get("X-Subscription-Token"))
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept: got %q", r.Header.Get("Accept"))
		}
		got = r.URL.Query()
		_, _ = w.Write([]byte(`{
		  "type": "search",
		  "query": {"original": "best ramen in tokyo", "more_results_available": true},
		  "web": {
		    "type": "search",
		    "results": [
		      {"title": "Tokyo Ramen Guide", "url": "https://example.com/ramen", "description": "Top 10 ramen shops...", "age": "3 weeks ago"},
		      {"title": "Eater", "url": "https://eater.com", "description": "Best ramen in Tokyo for 2026", "page_age": "2026-04-01T09:00:00"}
		    ]
		  },
		  "news": {"results": [{"title": "ignored", "url": "https://news.example", "description": "news vertical"}]}
		}`))
	}))
	defer srv.Close()

	c := New("brave-test", srv.URL, 0.005)
	res, err := c.Search(context.Background(), search.Query{Text: "best ramen in tokyo", MaxResults: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got.Get("q") != "best ramen in tokyo" {
		t.Errorf("q: got %q", got.Get("q"))
	}
	if got.Get("count") != "3" {
		t.Errorf("count: got %q want 3", got.Get("count"))
	}
	if got.Get("text_decorations") != "false" {
		t.Errorf("text_decorations: got %q want false", got.Get("text_decorations"))
	}
	if got.Get("result_filter") != "web" {
		t.Errorf("result_filter: got %q want web", got.Get("result_filter"))
	}
	if got.Has("freshness") {
		t.Errorf("freshness must be omitted when the query has none; got %q", got.Get("freshness"))
	}
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 items (web only, news ignored), got %d: %+v", len(res.Items), res.Items)
	}
	if res.Items[0].Title != "Tokyo Ramen Guide" || res.Items[0].URL != "https://example.com/ramen" || res.Items[0].PublishedAt != "" {
		t.Errorf("item[0] = %+v", res.Items[0])
	}
	if res.Items[1].URL != "https://eater.com" || res.Items[1].PublishedAt != "2026-04-01T09:00:00" {
		t.Errorf("item[1] = %+v", res.Items[1])
	}
	if res.CostUSD != 0.005 {
		t.Errorf("cost: got %v want 0.005", res.CostUSD)
	}
}

func TestSearch_FreshnessMapsToBraveCodes(t *testing.T) {
	cases := map[string]string{"day": "pd", "week": "pw", "month": "pm", "decade": ""}
	for in, want := range cases {
		var captured url.Values
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			captured = r.URL.Query()
			_, _ = w.Write([]byte(`{"web":{"results":[]}}`))
		}))
		c := New("k", srv.URL, 0.005)
		if _, err := c.Search(context.Background(), search.Query{Text: "x", Freshness: in}); err != nil {
			t.Fatalf("Search(%s): %v", in, err)
		}
		srv.Close()
		if captured.Get("freshness") != want {
			t.Errorf("freshness for %q: got %q want %q", in, captured.Get("freshness"), want)
		}
	}
}

func TestSearch_CountClampedAndDefaulted(t *testing.T) {
	var captured url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.URL.Query()
		_, _ = w.Write([]byte(`{"web":{"results":[]}}`))
	}))
	defer srv.Close()
	c := New("k", srv.URL, 0.005)

	if _, err := c.Search(context.Background(), search.Query{Text: "x", MaxResults: 50}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if captured.Get("count") != "20" {
		t.Errorf("count for 50: got %q want 20 (Brave's maximum)", captured.Get("count"))
	}
	if _, err := c.Search(context.Background(), search.Query{Text: "x"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if captured.Get("count") != "5" {
		t.Errorf("count for zero: got %q want driver default 5", captured.Get("count"))
	}
}

func TestSearch_StripsMarkupAndEntities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"web":{"results":[
		  {"title":"<strong>Python</strong> web frameworks &amp; tools","url":"https://x","description":"Django isn&#x27;t the only <strong>Python</strong> framework"},
		  {"title":"no url is dropped","description":"skip me"}
		]}}`))
	}))
	defer srv.Close()
	c := New("k", srv.URL, 0.005)
	res, err := c.Search(context.Background(), search.Query{Text: "python"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("expected the url-less result to be dropped; got %d items", len(res.Items))
	}
	if res.Items[0].Title != "Python web frameworks & tools" {
		t.Errorf("title not cleaned: %q", res.Items[0].Title)
	}
	if res.Items[0].Snippet != "Django isn't the only Python framework" {
		t.Errorf("snippet not cleaned: %q", res.Items[0].Snippet)
	}
}

func TestSearch_EmptyQueryIsPermanent(t *testing.T) {
	c := New("k", "http://127.0.0.1:1", 0.005)
	_, err := c.Search(context.Background(), search.Query{})
	if err == nil || !routing.IsPermanent(err) {
		t.Fatalf("empty query must fail permanently without a request; got %v", err)
	}
}

func TestSearch_HTTPErrorSurfacedWithSnippet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"ErrorResponse","error":{"code":"SUBSCRIPTION_TOKEN_INVALID","detail":"Unable to validate subscription token"}}`))
	}))
	defer srv.Close()
	c := New("bad", srv.URL, 0.005)
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err == nil {
		t.Fatalf("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "SUBSCRIPTION_TOKEN_INVALID") {
		t.Errorf("error should include status and snippet, got: %v", err)
	}
	if !routing.IsPermanent(err) {
		t.Errorf("401 must classify as permanent; got %v", err)
	}
}

func TestSearch_RateLimitClassifiedForCooldown(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"ErrorResponse","error":{"code":"RATE_LIMITED"}}`))
	}))
	defer srv.Close()
	c := New("k", srv.URL, 0.005)
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err == nil {
		t.Fatalf("expected error on 429")
	}
	var re *routing.Error
	if !errors.As(err, &re) || re.Status != http.StatusTooManyRequests {
		t.Fatalf("expected a *routing.Error carrying status 429; got %v", err)
	}
	if re.Kind != routing.ClassifyHTTPStatus(http.StatusTooManyRequests) {
		t.Errorf("429 kind = %v, want the shared HTTP classification so the guard's cooldown applies", re.Kind)
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
		_, _ = w.Write([]byte(`{"web":{"results":[{"title":"recovered","url":"https://x","description":"hit"}]}}`))
	}))
	defer srv.Close()
	c := New("k", srv.URL, 0.005)
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

func TestSearch_OversizedResponseIsRejected(t *testing.T) {
	largeSnippet := strings.Repeat("a", maxResponseBodyBytes)
	payload := fmt.Sprintf(`{"web":{"results":[{"title":"x","url":"https://x","description":"%s"}]}}`, largeSnippet)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	c := New("k", srv.URL, 0.005)
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

func TestNameCostAndDefaultBaseURL(t *testing.T) {
	c := New("k", "", 0.005)
	if c.Name() != "brave" {
		t.Errorf("Name: got %q", c.Name())
	}
	if c.CostPerCall() != 0.005 {
		t.Errorf("CostPerCall: got %v", c.CostPerCall())
	}
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL: got %q want %q", c.baseURL, DefaultBaseURL)
	}
	if New("k", "https://proxy.example/", 0.005).baseURL != "https://proxy.example" {
		t.Errorf("trailing slash on base URL should be trimmed")
	}
}
