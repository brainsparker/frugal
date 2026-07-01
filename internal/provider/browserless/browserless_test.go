package browserless

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

	"github.com/frugalsh/frugal/internal/browse"
	"github.com/frugalsh/frugal/internal/routing"
)

func TestRender_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/content" {
			t.Errorf("path: got %s want /content", r.URL.Path)
		}
		if r.URL.Query().Get("token") != "bl-test" {
			t.Errorf("token query: got %q", r.URL.Query().Get("token"))
		}
		body, _ := io.ReadAll(r.Body)
		var req browserlessRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.URL != "https://example.com/spa" {
			t.Errorf("url: got %q", req.URL)
		}
		if req.WaitFor != 500 {
			t.Errorf("waitFor: got %d want 500", req.WaitFor)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body><h1>Rendered</h1><p>SPA content</p></body></html>"))
	}))
	defer srv.Close()

	c := New("bl-test", srv.URL, 0.002)
	res, err := c.Render(context.Background(), browse.Query{
		URL:       "https://example.com/spa",
		WaitForMS: 500,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(res.HTML, "<h1>Rendered</h1>") {
		t.Errorf("HTML missing rendered body: %q", res.HTML)
	}
	if res.Text != "" {
		t.Errorf("Text should be empty when ReturnFormat != 'text'; got %q", res.Text)
	}
	if res.CostUSD != 0.002 {
		t.Errorf("CostUSD: got %v want 0.002", res.CostUSD)
	}
}

func TestRender_TextFormatStripsHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body><h1>Hi</h1><p>Body.</p><script>tracker()</script></body></html>"))
	}))
	defer srv.Close()
	c := New("k", srv.URL, 0.002)
	res, err := c.Render(context.Background(), browse.Query{
		URL:          "https://example.com",
		ReturnFormat: "text",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(res.Text, "Hi") || !strings.Contains(res.Text, "Body.") {
		t.Errorf("Text missing headline / body; got %q", res.Text)
	}
	if strings.Contains(res.Text, "tracker") {
		t.Errorf("script leaked into Text; got %q", res.Text)
	}
}

func TestRender_401IsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("bad token"))
	}))
	defer srv.Close()
	c := New("bad", srv.URL, 0.002)
	_, err := c.Render(context.Background(), browse.Query{URL: "https://x"})
	if err == nil {
		t.Fatalf("expected error on 401")
	}
	if !routing.IsPermanent(err) {
		t.Errorf("401 must classify as permanent; got %v", err)
	}
}

func TestRender_5xxRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("<html><body>recovered</body></html>"))
	}))
	defer srv.Close()
	c := New("k", srv.URL, 0.002)
	res, err := c.Render(context.Background(), browse.Query{URL: "https://x"})
	if err != nil {
		t.Fatalf("expected retry to recover; got %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 attempts; got %d", calls.Load())
	}
	if !strings.Contains(res.HTML, "recovered") {
		t.Errorf("unexpected HTML after retry: %q", res.HTML)
	}
}

func TestRender_NetworkErrorIsTransient(t *testing.T) {
	c := New("k", "http://127.0.0.1:1", 0.002)
	c.httpClient.Timeout = 200 * time.Millisecond
	_, err := c.Render(context.Background(), browse.Query{URL: "https://x"})
	if err == nil {
		t.Fatalf("expected network error")
	}
	if !routing.IsTransient(err) {
		t.Errorf("network failure must classify as transient; got %v", err)
	}
}

func TestRender_ConnectionRefusedDoesNotLeakToken(t *testing.T) {
	const token = "bl-super-secret-token"
	c := New(token, "http://127.0.0.1:1", 0.002)
	c.httpClient.Timeout = 200 * time.Millisecond
	_, err := c.Render(context.Background(), browse.Query{URL: "https://x"})
	if err == nil {
		t.Fatalf("expected connection-refused error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("token leaked into error: %v", err)
	}
	if !strings.Contains(err.Error(), "REDACTED") {
		t.Errorf("expected redaction marker in transport error; got %v", err)
	}
	if !routing.IsTransient(err) {
		t.Errorf("redaction must not change classification; got %v", err)
	}
}

func TestRender_UpstreamBodyEchoingTokenIsRedacted(t *testing.T) {
	const token = "bl-super-secret-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		// Some upstream error pages echo the full request URL back.
		_, _ = w.Write([]byte("bad request for " + r.URL.String()))
	}))
	defer srv.Close()
	c := New(token, srv.URL, 0.002)
	_, err := c.Render(context.Background(), browse.Query{URL: "https://x"})
	if err == nil {
		t.Fatalf("expected error on 400")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("token leaked into error: %v", err)
	}
}

func TestRender_EmptyURLIsPermanent(t *testing.T) {
	c := New("k", "http://example.invalid", 0.002)
	_, err := c.Render(context.Background(), browse.Query{})
	if err == nil {
		t.Fatalf("expected error for empty URL")
	}
	if !routing.IsPermanent(err) {
		t.Errorf("empty URL should be permanent; got %v", err)
	}
	var typed *routing.Error
	if !errors.As(err, &typed) || typed.Provider != "browserless" {
		t.Errorf("expected *routing.Error from browserless; got %v", err)
	}
}

func TestNameAndCost(t *testing.T) {
	c := New("k", "", 0.002)
	if c.Name() != "browserless" {
		t.Errorf("Name: got %q want browserless", c.Name())
	}
	if c.CostPerCall() != 0.002 {
		t.Errorf("CostPerCall: got %v want 0.002", c.CostPerCall())
	}
}

func TestNew_DefaultsBaseURL(t *testing.T) {
	c := New("k", "", 0.002)
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL default: got %q want %q", c.baseURL, DefaultBaseURL)
	}
}
