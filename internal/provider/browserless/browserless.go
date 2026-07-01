// Package browserless implements a browse.Browser backed by
// Browserless (https://chrome.browserless.io), a hosted headless-Chrome
// service.
//
// Browserless is Frugal's premium browse tier — handles JS-rendered
// content, anti-bot waits, and custom wait-for selectors. Operators pay
// per render (~$0.002 list).
package browserless

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/frugalsh/frugal/internal/browse"
	"github.com/frugalsh/frugal/internal/routing"
)

// DefaultBaseURL is the Browserless production endpoint.
const DefaultBaseURL = "https://chrome.browserless.io"

// Client implements browse.Browser against Browserless.
type Client struct {
	token       string
	baseURL     string
	httpClient  *http.Client
	costPerCall float64
}

// New constructs a Browserless client. token is the operator's
// BROWSERLESS_TOKEN. baseURL defaults to DefaultBaseURL when empty.
// costPerCall is the per-render USD price the operator agreed to.
func New(token, baseURL string, costPerCall float64) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		token:       token,
		baseURL:     strings.TrimRight(baseURL, "/"),
		costPerCall: costPerCall,
		// Renders can be slow on heavy pages; give the upstream room.
		httpClient: &http.Client{Timeout: 90 * time.Second},
	}
}

// Name reports the provider identifier — stable across releases.
func (c *Client) Name() string { return "browserless" }

// CostPerCall returns the configured per-render USD price.
func (c *Client) CostPerCall() float64 { return c.costPerCall }

// Render fetches q.URL via Browserless's /content endpoint, returning
// the rendered HTML. When q.ReturnFormat == "text", the driver runs
// the HTML through the inline strip in internal/browse.
func (c *Client) Render(ctx context.Context, q browse.Query) (browse.Result, error) {
	if q.URL == "" {
		return browse.Result{}, routing.Permanent(c.Name(), 0, fmt.Errorf("empty url"))
	}
	body, err := json.Marshal(browserlessRequest{URL: q.URL, WaitFor: q.WaitForMS})
	if err != nil {
		return browse.Result{}, routing.Permanent(c.Name(), 0, fmt.Errorf("marshal request: %w", err))
	}

	var out browse.Result
	err = routing.DoWithRetry(ctx, 1+len(routing.DefaultBackoff), routing.DefaultBackoff, func() error {
		var attemptErr error
		out, attemptErr = c.doOnce(ctx, body, q.ReturnFormat)
		return attemptErr
	})
	return out, err
}

// redact replaces the operator token in an error's message. Browserless
// only accepts the token as a ?token= query param, so transport errors
// (url.Error) embed the secret in the request URL they quote. Anything
// that escapes doOnce must pass through here before it can reach retry
// logs or MCP tool output. The result is a flattened copy — classify the
// original error first; the wrap chain does not survive redaction.
func (c *Client) redact(err error) error {
	if err == nil || c.token == "" || !strings.Contains(err.Error(), c.token) {
		return err
	}
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), c.token, "REDACTED"))
}

// doOnce runs one HTTP attempt against Browserless. The retry loop in
// Render wraps this; the returned error is already a *routing.Error.
func (c *Client) doOnce(ctx context.Context, body []byte, returnFormat string) (browse.Result, error) {
	endpoint := c.baseURL + "/content?token=" + c.token
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return browse.Result{}, routing.Permanent(c.Name(), 0, fmt.Errorf("build request: %w", c.redact(err)))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/html")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return browse.Result{}, &routing.Error{
			Provider: c.Name(), Kind: routing.ClassifyNetwork(err), Err: c.redact(err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return browse.Result{}, &routing.Error{
			Provider: c.Name(),
			Kind:     routing.ClassifyHTTPStatus(resp.StatusCode),
			Status:   resp.StatusCode,
			Err:      c.redact(fmt.Errorf("%s", bytes.TrimSpace(snippet))),
		}
	}

	// Browserless /content returns the rendered HTML as the response body,
	// not JSON. Read everything.
	html, err := io.ReadAll(resp.Body)
	if err != nil {
		return browse.Result{}, routing.Transient(c.Name(), resp.StatusCode, fmt.Errorf("read body: %w", err))
	}
	res := browse.Result{HTML: string(html), CostUSD: c.costPerCall}
	if returnFormat == "text" {
		res.Text = browse.StripHTML(res.HTML)
	}
	return res, nil
}

type browserlessRequest struct {
	URL     string `json:"url"`
	WaitFor int    `json:"waitFor,omitempty"`
}
