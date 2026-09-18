// Package jina implements an extract.Extractor backed by the Jina Reader
// API (https://r.jina.ai): prepend the endpoint to any URL and Reader
// fetches the page in a headless browser, strips the chrome, and returns
// the main content as Markdown.
//
// Jina Reader is Frugal's free hosted rung for frugal__extract, sitting
// between the local go-readability pass and the paid Firecrawl tier.
// go-readability is a plain HTTP fetch, so a page that paints its body
// with JavaScript comes back empty and the chain falls through; before
// this driver the next rung was a paid scrape. Reader renders the page
// server-side at no charge: the keyless tier allows 20 requests per
// minute per IP, and a free Jina API key (JINA_API_KEY) lifts that to
// 500 requests per minute, tracked per key instead of per IP. Rate
// limits: https://jina.ai/api-dashboard/rate-limit
//
// Privacy note for operators: like Marginalia and Wikipedia on the search
// side, this rung sends the request (here, the target URL) to a third
// party. The page content itself never touches the operator's machine
// until Reader returns it. Operators who want extraction to stay fully
// local tombstone the entry (`jina: {enabled: false}`) or add it to the
// extract deny list.
package jina

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/routing"
)

// DefaultBaseURL is the public Jina Reader endpoint.
const DefaultBaseURL = "https://r.jina.ai"

// DefaultUserAgent identifies the driver honestly to Jina, matching the
// (+URL) convention the other keyless drivers use.
const DefaultUserAgent = "frugal (+https://frugal.sh)"

// maxResponseBodyBytes caps successful API response reads. Reader
// returns one rendered page as JSON, so a few MiB is plenty; the cap is
// generous because truncating a good render wastes the call.
const maxResponseBodyBytes int64 = 20 << 20 // 20 MiB

// Client implements extract.Extractor against Jina Reader.
type Client struct {
	baseURL     string
	apiKey      string
	userAgent   string
	httpClient  *http.Client
	costPerCall float64
}

// New constructs a Jina Reader client. baseURL defaults to
// DefaultBaseURL when empty (overridable for tests and self-hosted
// Reader deployments). apiKey is optional: empty means the keyless
// 20 RPM tier; a key is sent as a bearer token for the 500 RPM tier.
// costPerCall is the per-call USD price the operator declared; the
// shipped default is 0.
func New(baseURL, apiKey string, costPerCall float64) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL:     baseURL,
		apiKey:      strings.TrimSpace(apiKey),
		userAgent:   DefaultUserAgent,
		costPerCall: costPerCall,
		// Reader renders in a headless browser; its published average
		// latency is around 8s and slow pages run longer. Same generous
		// budget the Firecrawl driver uses, well above the search tier's 15s.
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// Name reports the provider identifier. Stable across releases: routing
// policy order / deny lists and the `provider:` tool argument pin by it.
func (c *Client) Name() string { return "jina" }

// CostPerCall returns the configured per-call USD price (0 by default).
func (c *Client) CostPerCall() float64 { return c.costPerCall }

// Keyed reports whether the client sends an API key. Exposed so the
// server can log which rate-limit tier a registration landed on.
func (c *Client) Keyed() bool { return c.apiKey != "" }

// Extract runs one Reader call for q.URL. Transient HTTP and network
// failures are retried inside the driver; permanent failures fall
// through to the next extractor at the router. A dead target (Reader
// reports the site itself returned 404/410) is Fatal so the chain stops
// instead of paying a scraper to rediscover a dead link.
func (c *Client) Extract(ctx context.Context, q extract.Query) (extract.Result, error) {
	target := strings.TrimSpace(q.URL)
	if target == "" {
		return extract.Result{}, routing.Fatal(c.Name(), 0, fmt.Errorf("empty url"))
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme == "" || u.Host == "" {
		if err == nil {
			err = fmt.Errorf("missing scheme or host")
		}
		return extract.Result{}, routing.Fatal(c.Name(), 0, fmt.Errorf("parse url: %w", err))
	}

	format := returnFormat(q.Formats)
	var out extract.Result
	err = routing.DoWithRetry(ctx, 1+len(routing.DefaultBackoff), routing.DefaultBackoff, func() error {
		var attemptErr error
		out, attemptErr = c.doOnce(ctx, target, format)
		return attemptErr
	})
	return out, err
}

// doOnce performs one Reader request. The retry loop in Extract wraps
// this; the returned error is already a *routing.Error.
func (c *Client) doOnce(ctx context.Context, target, format string) (extract.Result, error) {
	// Reader's URL form is the endpoint followed by the full target URL,
	// scheme included: https://r.jina.ai/https://example.com/page.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/"+target, nil)
	if err != nil {
		return extract.Result{}, routing.Permanent(c.Name(), 0, fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Return-Format", format)
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return extract.Result{}, &routing.Error{
			Provider: c.Name(), Kind: routing.ClassifyNetwork(err), Err: err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Non-200 here is Reader's own verdict (429 rate limit, 5xx,
		// 422 bad target), not the target site's. 429 classifies as
		// transient and carries its status so the routing guard opens
		// the cooldown window.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return extract.Result{}, &routing.Error{
			Provider: c.Name(),
			Kind:     routing.ClassifyHTTPStatus(resp.StatusCode),
			Status:   resp.StatusCode,
			Err:      fmt.Errorf("%s", bytes.TrimSpace(snippet)),
		}
	}

	// Read the full body before decoding so failures classify correctly:
	// a read error is the network dying mid-body (transient, retry);
	// an oversized or unparseable body is deterministic (permanent).
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return extract.Result{}, routing.Transient(c.Name(), resp.StatusCode, fmt.Errorf("read response: %w", err))
	}
	if int64(len(raw)) > maxResponseBodyBytes {
		return extract.Result{}, routing.Permanent(c.Name(), resp.StatusCode, fmt.Errorf("response exceeds %d MiB cap", maxResponseBodyBytes>>20))
	}
	var parsed readerResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return extract.Result{}, routing.Permanent(c.Name(), resp.StatusCode, fmt.Errorf("decode response: %w", err))
	}

	// Reader answers 200 even when the TARGET site failed, and reports
	// the site's status in data.httpStatus. Mirror go-readability's
	// rule: 404/410 from the site is Fatal (the page is gone for every
	// extractor); any other 4xx/5xx is Permanent (a 403 is often an
	// anti-bot wall a different renderer can get past), so the chain
	// still falls through to the next rung.
	if st := parsed.Data.HTTPStatus; st >= 400 {
		kind := routing.KindPermanent
		if st == http.StatusNotFound || st == http.StatusGone {
			kind = routing.KindFatal
		}
		msg := strings.TrimSpace(parsed.Data.Warning)
		if msg == "" {
			msg = fmt.Sprintf("target returned http %d", st)
		}
		return extract.Result{}, &routing.Error{
			Provider: c.Name(),
			Kind:     kind,
			Status:   st,
			Err:      fmt.Errorf("%s", msg),
		}
	}

	content := strings.TrimSpace(parsed.Data.Content)
	if content == "" {
		// A rendered page with nothing left after boilerplate removal:
		// Permanent so a paid scraper with a different pipeline gets a
		// turn, same as go-readability's empty-content rule.
		return extract.Result{}, routing.Permanent(c.Name(), resp.StatusCode, fmt.Errorf("empty content after render"))
	}

	res := extract.Result{
		Title:   parsed.Data.Title,
		CostUSD: c.costPerCall,
	}
	switch format {
	case "html":
		res.HTML = content
	case "text":
		res.Text = content
	default:
		res.Markdown = content
		// Reader returns one format per request; Markdown doubles as
		// the plain-text read, the same shortcut the Firecrawl driver
		// takes.
		res.Text = content
	}
	return res, nil
}

// returnFormat maps Frugal's requested formats onto Reader's single
// X-Return-Format value. Reader renders one shape per request, so
// markdown wins whenever it is requested or nothing is; html and text
// are honored only when they are the sole ask.
func returnFormat(formats []string) string {
	if len(formats) == 0 {
		return "markdown"
	}
	var html, text bool
	for _, f := range formats {
		switch strings.ToLower(strings.TrimSpace(f)) {
		case "markdown":
			return "markdown"
		case "html":
			html = true
		case "text":
			text = true
		}
	}
	switch {
	case html && !text:
		return "html"
	case text && !html:
		return "text"
	}
	return "markdown"
}

// readerResponse is the subset of the Reader JSON envelope the driver
// consumes. Reader also returns description, publishedTime, usage
// (tokens), and metadata; those drop on the floor until an eval shows
// they change downstream answer quality.
type readerResponse struct {
	Code int `json:"code"`
	Data struct {
		Title      string `json:"title"`
		URL        string `json:"url"`
		Content    string `json:"content"`
		Warning    string `json:"warning"`
		HTTPStatus int    `json:"httpStatus"`
	} `json:"data"`
}
