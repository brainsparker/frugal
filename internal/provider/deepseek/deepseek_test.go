package deepseek

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
	"github.com/frugalsh/frugal/internal/search"
)

var fixedNow = time.Date(2026, time.October, 27, 13, 0, 0, 0, time.UTC)

func newTestSearch(url string, cost float64) *Search {
	c := NewSearch("ds-test", url, cost)
	c.now = func() time.Time { return fixedNow }
	return c
}

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode body %q: %v", raw, err)
	}
	return got
}

func TestSearch_HappyPath(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: got %s want POST", r.Method)
		}
		if r.URL.Path != "/v1/tools/search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer ds-test" {
			t.Errorf("Authorization: got %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type: got %q", r.Header.Get("Content-Type"))
		}
		got = decodeBody(t, r)
		_, _ = w.Write([]byte(`{
		  "results": [
		    {"rerank_score": 2.4, "title": " DeepSeek V4 model released ", "url": "https://example.com/deepseek-v4",
		     "snippet": "DeepSeek V4 is now available.", "summary": "Long summary for a model.",
		     "content": "", "site_name": "Example News", "site_icon": null,
		     "date": "2026-06-01T00:00:00+00:00", "authority": 3},
		    {"rerank_score": 1.1, "title": "No snippet here", "url": "https://example.org/b",
		     "snippet": "", "summary": "Summary stands in when the snippet is empty.",
		     "content": "", "site_name": null, "site_icon": null, "date": null, "authority": -1},
		    {"rerank_score": 0.2, "title": "dropped: no url", "url": "", "snippet": "x", "summary": "y",
		     "content": "", "site_name": null, "site_icon": null, "date": null, "authority": 0}
		  ]
		}`))
	}))
	defer srv.Close()

	c := newTestSearch(srv.URL, DefaultCostPerCall)
	res, err := c.Search(context.Background(), search.Query{Text: "deepseek v4 release", MaxResults: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got["query"] != "deepseek v4 release" {
		t.Errorf("query: got %q", got["query"])
	}
	if got["count"] != float64(3) {
		t.Errorf("count: got %v want 3", got["count"])
	}
	if got["need_content"] != false {
		t.Errorf("need_content must be false (page content is the fetch rung's job); got %v", got["need_content"])
	}
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 items (empty-url record dropped), got %d: %+v", len(res.Items), res.Items)
	}
	if res.Items[0].Title != "DeepSeek V4 model released" || res.Items[0].URL != "https://example.com/deepseek-v4" ||
		res.Items[0].Snippet != "DeepSeek V4 is now available." || res.Items[0].PublishedAt != "2026-06-01T00:00:00+00:00" {
		t.Errorf("item[0] = %+v", res.Items[0])
	}
	if res.Items[1].Snippet != "Summary stands in when the snippet is empty." || res.Items[1].PublishedAt != "" {
		t.Errorf("item[1] = %+v", res.Items[1])
	}
	if res.CostUSD != DefaultCostPerCall {
		t.Errorf("cost: got %v want %v", res.CostUSD, DefaultCostPerCall)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("no warnings expected for a short query without freshness; got %v", res.Warnings)
	}
}

func TestSearch_FreshnessBecomesAfterOperator(t *testing.T) {
	cases := map[string]string{
		"day":   "frugal after:2026-10-26",
		"week":  "frugal after:2026-10-20",
		"month": "frugal after:2026-09-27",
		"":      "frugal",
		"year":  "frugal", // unknown window: no filter, same as the other drivers
	}
	for fresh, want := range cases {
		var got map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = decodeBody(t, r)
			_, _ = w.Write([]byte(`{"results":[]}`))
		}))
		c := newTestSearch(srv.URL, 0)
		res, err := c.Search(context.Background(), search.Query{Text: "frugal", Freshness: fresh})
		srv.Close()
		if err != nil {
			t.Fatalf("freshness %q: %v", fresh, err)
		}
		if got["query"] != want {
			t.Errorf("freshness %q: query got %q want %q", fresh, got["query"], want)
		}
		if len(res.Warnings) != 0 {
			t.Errorf("freshness %q is honored exactly, no warning expected; got %v", fresh, res.Warnings)
		}
	}
}

func TestSearch_LongQueryCutOnWordBoundaryWithWarning(t *testing.T) {
	long := strings.Repeat("alpha beta ", 20) // 220 chars
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = decodeBody(t, r)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	c := newTestSearch(srv.URL, 0)
	res, err := c.Search(context.Background(), search.Query{Text: long, Freshness: "week"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	q, _ := got["query"].(string)
	if n := len([]rune(q)); n > maxQueryRunes {
		t.Fatalf("query sent is %d runes, over the %d cap: %q", n, maxQueryRunes, q)
	}
	if !strings.HasSuffix(q, " after:2026-10-20") {
		t.Errorf("the freshness operator must survive the cut; got %q", q)
	}
	body := strings.TrimSuffix(q, " after:2026-10-20")
	if strings.HasSuffix(body, "alph") || strings.HasSuffix(body, "bet") || strings.HasSuffix(body, " ") {
		t.Errorf("cut must land on a word boundary; got %q", body)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "query cut to") || !strings.Contains(res.Warnings[0], "128") {
		t.Errorf("expected one truncation warning naming the limit; got %v", res.Warnings)
	}
}

func TestSearch_CountClampedAndDefaulted(t *testing.T) {
	var counts []float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := decodeBody(t, r)
		counts = append(counts, got["count"].(float64))
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	c := newTestSearch(srv.URL, 0)
	for _, n := range []int{0, 7, 500} {
		if _, err := c.Search(context.Background(), search.Query{Text: "x", MaxResults: n}); err != nil {
			t.Fatalf("MaxResults=%d: %v", n, err)
		}
	}
	if len(counts) != 3 || counts[0] != 5 || counts[1] != 7 || counts[2] != float64(maxCount) {
		t.Errorf("counts: got %v want [5 7 %d]", counts, maxCount)
	}
}

func TestSearch_EmptyQueryIsPermanent(t *testing.T) {
	c := newTestSearch("http://127.0.0.1:1", 0)
	_, err := c.Search(context.Background(), search.Query{Text: "   "})
	var re *routing.Error
	if !errors.As(err, &re) || re.Kind != routing.KindPermanent {
		t.Fatalf("expected permanent routing error, got %v", err)
	}
}

func TestSearch_ErrorEnvelopeMessageSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":{"message":"Insufficient Balance","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	c := newTestSearch(srv.URL, DefaultCostPerCall)
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	var re *routing.Error
	if !errors.As(err, &re) {
		t.Fatalf("expected routing error, got %v", err)
	}
	if re.Kind != routing.KindPermanent || re.Status != http.StatusPaymentRequired {
		t.Errorf("402 must be permanent with status; got kind=%v status=%d", re.Kind, re.Status)
	}
	if !strings.Contains(err.Error(), "Insufficient Balance") || strings.Contains(err.Error(), `"type"`) {
		t.Errorf("error should carry the envelope message, not the raw JSON: %v", err)
	}
}

func TestSearch_RateLimitClassifiedForCooldown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"QPS limit exceeded"}}`))
	}))
	defer srv.Close()

	// Paid price: no in-driver retry, so the 429 surfaces on the first attempt.
	c := newTestSearch(srv.URL, DefaultCostPerCall)
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	var re *routing.Error
	if !errors.As(err, &re) {
		t.Fatalf("expected routing error, got %v", err)
	}
	if re.Status != http.StatusTooManyRequests || re.Kind != routing.KindTransient {
		t.Errorf("429 must be transient and carry its status; got kind=%v status=%d", re.Kind, re.Status)
	}
	if re.RetryAfter != 2*time.Second {
		t.Errorf("Retry-After: got %v want 2s", re.RetryAfter)
	}
}

func TestSearch_5xxRetriedWhenFree(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"title":"ok","url":"https://ok.example","snippet":"s","summary":"","date":null}]}`))
	}))
	defer srv.Close()

	c := newTestSearch(srv.URL, 0)
	res, err := c.Search(context.Background(), search.Query{Text: "x"})
	if err != nil {
		t.Fatalf("expected retry to recover, got %v", err)
	}
	if len(res.Items) != 1 || atomic.LoadInt32(&hits) != 2 {
		t.Errorf("items=%d hits=%d", len(res.Items), hits)
	}
}

func TestSearch_PaidProviderNotRetried(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := newTestSearch(srv.URL, DefaultCostPerCall)
	if _, err := c.Search(context.Background(), search.Query{Text: "x"}); err == nil {
		t.Fatal("expected error")
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("a paid provider must not be retried in-driver; got %d attempts", hits)
	}
}

func TestSearch_BadJSONIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html>not json`))
	}))
	defer srv.Close()

	c := newTestSearch(srv.URL, DefaultCostPerCall)
	_, err := c.Search(context.Background(), search.Query{Text: "x"})
	var re *routing.Error
	if !errors.As(err, &re) || re.Kind != routing.KindTransient {
		t.Fatalf("expected transient decode error, got %v", err)
	}
}

func TestSearch_NameCostAndDefaults(t *testing.T) {
	c := NewSearch("  k  ", "", 0.00075)
	if c.Name() != "deepseek" {
		t.Errorf("Name: %q", c.Name())
	}
	if c.CostPerCall() != 0.00075 {
		t.Errorf("CostPerCall: %v", c.CostPerCall())
	}
	if c.baseURL != DefaultBaseURL || c.apiKey != "k" {
		t.Errorf("defaults: base=%q key=%q", c.baseURL, c.apiKey)
	}
	if NewSearch("k", "https://proxy.example/", 0).baseURL != "https://proxy.example" {
		t.Error("trailing slash must be trimmed")
	}
}

func TestBuildQuery_OperatorNeverCut(t *testing.T) {
	q, warns := buildQuery(strings.Repeat("w", 200), "day", fixedNow)
	if !strings.HasSuffix(q, " after:2026-10-26") {
		t.Fatalf("operator lost: %q", q)
	}
	if n := len([]rune(q)); n > maxQueryRunes {
		t.Errorf("over cap: %d", n)
	}
	if len(warns) != 1 {
		t.Errorf("warnings: %v", warns)
	}
	// Exactly at the cap with no operator: untouched, no warning.
	exact := strings.Repeat("é", maxQueryRunes)
	q, warns = buildQuery(exact, "", fixedNow)
	if q != exact || len(warns) != 0 {
		t.Errorf("a query at the cap must pass through: %q %v", q, warns)
	}
}

// ---- Fetch ----

func TestFetch_HappyPath(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/tools/fetch" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer ds-test" {
			t.Errorf("Authorization: got %q", r.Header.Get("Authorization"))
		}
		got = decodeBody(t, r)
		_, _ = w.Write([]byte(`{"title":"Example article","content":"# Example article\n\nArticle content."}`))
	}))
	defer srv.Close()

	c := NewFetch("ds-test", srv.URL, DefaultCostPerCall)
	res, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/article", Formats: []string{"html"}})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got["url"] != "https://example.com/article" {
		t.Errorf("url: got %q", got["url"])
	}
	if res.Title != "Example article" || !strings.HasPrefix(res.Markdown, "# Example article") || res.Text != res.Markdown {
		t.Errorf("result = %+v", res)
	}
	if res.HTML != "" {
		t.Errorf("fetch has no html rendering; HTML must stay empty, got %q", res.HTML)
	}
	if res.CostUSD != DefaultCostPerCall {
		t.Errorf("cost: %v", res.CostUSD)
	}
}

func TestFetch_404IsPermanentNotFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"Failed to fetch usable content"}}`))
	}))
	defer srv.Close()

	c := NewFetch("k", srv.URL, DefaultCostPerCall)
	_, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/maybe-gone"})
	var re *routing.Error
	if !errors.As(err, &re) {
		t.Fatalf("expected routing error, got %v", err)
	}
	if re.Kind != routing.KindPermanent || re.Status != http.StatusNotFound {
		t.Errorf("fetch 404 is ambiguous and must fall through, not stop the chain; got kind=%v status=%d", re.Kind, re.Status)
	}
	if !strings.Contains(err.Error(), "Failed to fetch usable content") {
		t.Errorf("message lost: %v", err)
	}
}

func TestFetch_403IsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"Rejected by security policy"}}`))
	}))
	defer srv.Close()

	c := NewFetch("k", srv.URL, DefaultCostPerCall)
	_, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/x"})
	var re *routing.Error
	if !errors.As(err, &re) || re.Kind != routing.KindPermanent || re.Status != http.StatusForbidden {
		t.Fatalf("expected permanent 403, got %v", err)
	}
}

func TestFetch_EmptyContentIsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"title":"Blank","content":"   "}`))
	}))
	defer srv.Close()

	c := NewFetch("k", srv.URL, DefaultCostPerCall)
	_, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/blank"})
	var re *routing.Error
	if !errors.As(err, &re) || re.Kind != routing.KindPermanent {
		t.Fatalf("expected permanent empty-content error, got %v", err)
	}
}

func TestFetch_429IsTransientWithStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewFetch("k", srv.URL, DefaultCostPerCall)
	_, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/x"})
	var re *routing.Error
	if !errors.As(err, &re) || re.Kind != routing.KindTransient || re.Status != http.StatusTooManyRequests {
		t.Fatalf("expected transient 429 for the cooldown guard, got %v", err)
	}
}

func TestFetch_5xxRetriedWhenFree(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		_, _ = w.Write([]byte(`{"title":"t","content":"body"}`))
	}))
	defer srv.Close()

	c := NewFetch("k", srv.URL, 0)
	res, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/x"})
	if err != nil || res.Markdown != "body" || atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("expected recovery on retry; err=%v res=%+v hits=%d", err, res, hits)
	}
}

func TestFetch_URLsRejectedLocally(t *testing.T) {
	c := NewFetch("k", "http://127.0.0.1:1", DefaultCostPerCall)
	cases := []struct {
		url  string
		kind routing.Kind
	}{
		{"", routing.KindFatal},
		{"not a url", routing.KindFatal},
		{"ftp://example.com/file", routing.KindPermanent},
		{"https://user:pw@example.com/", routing.KindPermanent},
		{"https://example.com/" + strings.Repeat("a", maxURLLen), routing.KindPermanent},
	}
	for _, tc := range cases {
		_, err := c.Extract(context.Background(), extract.Query{URL: tc.url})
		var re *routing.Error
		if !errors.As(err, &re) || re.Kind != tc.kind {
			t.Errorf("url %.40q: got %v want kind %v", tc.url, err, tc.kind)
		}
	}
}

func TestFetch_BadJSONIsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html>`))
	}))
	defer srv.Close()

	c := NewFetch("k", srv.URL, DefaultCostPerCall)
	_, err := c.Extract(context.Background(), extract.Query{URL: "https://example.com/x"})
	var re *routing.Error
	if !errors.As(err, &re) || re.Kind != routing.KindPermanent {
		t.Fatalf("expected permanent decode error, got %v", err)
	}
}

func TestFetch_NameCostAndDefaults(t *testing.T) {
	c := NewFetch("k", "", DefaultCostPerCall)
	if c.Name() != Name || c.CostPerCall() != DefaultCostPerCall || c.baseURL != DefaultBaseURL {
		t.Errorf("defaults: %+v", c)
	}
}

func TestErrorMessage(t *testing.T) {
	if got := errorMessage([]byte(`{"error":{"message":" 'count' must be between 1 and 50 "}}`)); got != "'count' must be between 1 and 50" {
		t.Errorf("envelope: %q", got)
	}
	if got := errorMessage([]byte("  plain text  ")); got != "plain text" {
		t.Errorf("fallback: %q", got)
	}
	if got := errorMessage([]byte(`{"error":{"message":""}}`)); got != `{"error":{"message":""}}` {
		t.Errorf("empty message falls back to the raw body: %q", got)
	}
}

// Compile-time interface checks.
var (
	_ search.Searcher   = (*Search)(nil)
	_ extract.Extractor = (*Fetch)(nil)
)
