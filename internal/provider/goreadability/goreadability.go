// Package goreadability implements an extract.Extractor backed by
// go-shiori/go-readability — a pure-Go port of Mozilla's Readability.js.
// No shell-out, no Python, no JS runtime: a single HTTP fetch followed
// by an in-process content-extraction pass.
//
// This is Frugal's free / local tier for the frugal__extract MCP tool.
// CostPerCall is zero so the router prefers it over paid providers when
// configured. Falls back to Firecrawl (or whatever paid extractor is
// configured) when a page is JS-rendered and Readability finds no
// meaningful content.
package goreadability

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	readability "github.com/go-shiori/go-readability"

	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/routing"
)

// Client implements extract.Extractor against an in-process
// Readability pipeline.
type Client struct {
	httpClient *http.Client
	userAgent  string
}

// DefaultUserAgent is the UA the driver sends when fetching pages.
// Identifies the bot honestly so site operators can recognize it.
const DefaultUserAgent = "frugal-readability (+https://frugal.sh)"

// New constructs a goreadability client. The default HTTP timeout is
// 15s (matches the searxng / marginalia drivers).
func New() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		userAgent:  DefaultUserAgent,
	}
}

// Name reports the provider identifier — stable across releases.
func (c *Client) Name() string { return "goreadability" }

// CostPerCall is always zero. The operator pays for egress + CPU,
// not per call.
func (c *Client) CostPerCall() float64 { return 0 }

// Extract fetches q.URL and pipes the response body through Readability.
// Network and HTTP failures are retried by the driver; an empty
// TextContent after a successful fetch is classified Permanent so the
// router falls through to a paid extractor that can render JS. A dead
// target (404/410 from the site itself) or unusable URL is Fatal — no
// extractor can fix the request, so the router must not spend a paid
// call rediscovering that.
func (c *Client) Extract(ctx context.Context, q extract.Query) (extract.Result, error) {
	if q.URL == "" {
		return extract.Result{}, routing.Fatal(c.Name(), 0, fmt.Errorf("empty url"))
	}
	parsed, err := url.Parse(q.URL)
	if err != nil {
		return extract.Result{}, routing.Fatal(c.Name(), 0, fmt.Errorf("parse url: %w", err))
	}

	var out extract.Result
	err = routing.DoWithRetry(ctx, 1+len(routing.DefaultBackoff), routing.DefaultBackoff, func() error {
		var attemptErr error
		out, attemptErr = c.doOnce(ctx, parsed)
		return attemptErr
	})
	return out, err
}

// doOnce performs one fetch + parse. The retry loop in Extract wraps
// this; the returned error is already classified.
func (c *Client) doOnce(ctx context.Context, u *url.URL) (extract.Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return extract.Result{}, routing.Permanent(c.Name(), 0, fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "text/html, application/xhtml+xml")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return extract.Result{}, &routing.Error{
			Provider: c.Name(), Kind: routing.ClassifyNetwork(err), Err: err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		kind := routing.ClassifyHTTPStatus(resp.StatusCode)
		// 404/410 here comes from the TARGET site, not from a provider
		// API — the page is gone for every extractor. Fatal stops the
		// chain instead of forwarding the dead link to a paid scraper.
		// (A 403, by contrast, stays Permanent and falls through: it's
		// often an anti-bot block a rendering provider can get past.)
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
			kind = routing.KindFatal
		}
		return extract.Result{}, &routing.Error{
			Provider: c.Name(),
			Kind:     kind,
			Status:   resp.StatusCode,
			Err:      fmt.Errorf("http %d", resp.StatusCode),
		}
	}

	// go-readability needs a seekable / re-readable buffer.
	buf := &bytes.Buffer{}
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return extract.Result{}, routing.Transient(c.Name(), resp.StatusCode, fmt.Errorf("read body: %w", err))
	}

	article, err := readability.FromReader(buf, u)
	if err != nil {
		// Parse failure on a 200 page is uncommon — could be the page is
		// malformed enough that the parser bails. Treat as Permanent so
		// the router falls through to a paid driver that may have a
		// different parser stack.
		return extract.Result{}, routing.Permanent(c.Name(), 0, fmt.Errorf("readability: %w", err))
	}

	// Empty TextContent typically means "this page renders content via
	// JS" — Readability can't see it. Classify Permanent so the router
	// falls through to a paid driver (Firecrawl, eventually a browse
	// driver) that can run JS. See plan open question #2.
	if article.TextContent == "" {
		return extract.Result{}, routing.Permanent(c.Name(), 0, fmt.Errorf("empty content (page likely requires JS)"))
	}

	return extract.Result{
		Markdown: article.Content, // go-readability returns HTML; treat as markdown-equiv
		HTML:     article.Content,
		Text:     article.TextContent,
		Title:    article.Title,
		Byline:   article.Byline,
		// go-readability doesn't surface a separate links slice; agents
		// can parse them from HTML if needed.
		CostUSD: 0,
	}, nil
}
