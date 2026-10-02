// Package youcomfree implements a search.Searcher backed by You.com's
// hosted MCP endpoint on its keyless free profile
// (https://api.you.com/mcp?profile=free). Free, public, no API key, no
// account: the same major-index search the keyed youcom driver reaches
// over REST, capped server-side at roughly 100 queries per day per IP.
//
// This is the zero-key chain's major-index rung. Marginalia covers the
// indie web and Wikipedia covers reference entities; neither is a
// general SERP, and before this driver a keyless install had nothing
// else: on a mainstream query Marginalia returns weak hits or none, and
// Wikipedia returns encyclopedia entries. youcom-free leads the
// public-free rungs in the canonical order (a self-hosted SearXNG still
// comes first), so a keyless install gets a real web index by default
// and falls back to Marginalia and Wikipedia when the daily allowance is
// spent. Operators who prefer the indie index first can say so with
// `order: [marginalia]`, or keep every query off You.com with
// `enabled: false`.
//
// Transport: the endpoint is stateless. It issues no Mcp-Session-Id and
// answers a bare tools/call POST without an initialize handshake, which
// is also the shape the 2026-07-28 MCP revision standardizes on. So
// each search is exactly one HTTP POST of a JSON-RPC tools/call for the
// server's `you-search` tool, and the driver stays a plain HTTP client
// like every other provider here: no long-lived session, no standalone
// SSE stream, no reconnect state. The server may answer as a single JSON
// document or as an SSE stream (a progress notification followed by the
// result); both are handled.
//
// Quota: when the daily allowance is exhausted the server rate-limits.
// The driver reports that as a *routing.Error with Status 429, which the
// routing guard turns into a cooldown so the chain stops probing a
// provider that will keep saying no, and the next free rung serves.
package youcomfree

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// DefaultBaseURL is You.com's hosted MCP endpoint. The free profile is
// selected with the `profile=free` query parameter, appended by the
// driver so an operator override only needs the host and path.
const DefaultBaseURL = "https://api.you.com/mcp"

// DefaultUserAgent identifies frugal to the endpoint, same convention
// the Marginalia driver follows.
const DefaultUserAgent = "frugal (+https://frugal.sh)"

// toolName is the search tool the hosted server advertises.
const toolName = "you-search"

// protocolVersion is the MCP revision the driver declares on each POST.
// It is what the endpoint negotiated when probed; the header is
// advisory for a stateless call and older servers ignore it.
const protocolVersion = "2025-06-18"

// rateLimitSigns matches the wording the hosted server uses in a tool
// error result when the daily allowance is spent. The HTTP status is
// the primary signal; this is the backstop for a 200 that carries a
// quota refusal in the body.
var rateLimitSigns = regexp.MustCompile(`(?i)(\b429\b|rate.?limit|quota|too many requests|limit (reached|exceeded))`)

// Client implements search.Searcher against the hosted free tier.
type Client struct {
	endpoint   string
	userAgent  string
	httpClient *http.Client
}

// New constructs a client. baseURL defaults to DefaultBaseURL when
// empty; overridable for tests against httptest. Trailing slashes are
// stripped and the free profile parameter is appended.
func New(baseURL string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	sep := "?"
	if strings.Contains(baseURL, "?") {
		sep = "&"
	}
	return &Client{
		endpoint:   baseURL + sep + "profile=free",
		userAgent:  DefaultUserAgent,
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

// Name reports the provider identifier. Stable across releases: routing
// policy order / deny lists and the `provider:` tool argument pin by it.
// Deliberately distinct from "youcom" (the keyed REST driver) so a deny
// on one does not silently cover the other; they are different tiers
// with different quotas.
func (c *Client) Name() string { return "youcom-free" }

// CostPerCall is always zero: the free profile bills nothing.
func (c *Client) CostPerCall() float64 { return 0 }

// Search runs one hosted search. Transient failures (network, 5xx, 429)
// are retried inside the driver via routing.DoWithRetry; the router falls
// back to the next provider when all retries fail. Freshness passes
// straight through: the tool accepts the same day | week | month buckets
// frugal__search does, so no warning is needed.
func (c *Client) Search(ctx context.Context, q search.Query) (search.Results, error) {
	if q.Text == "" {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("empty query"))
	}
	var out search.Results
	err := routing.DoWithRetry(ctx, 1+len(routing.DefaultBackoff), routing.DefaultBackoff, func() error {
		var attemptErr error
		out, attemptErr = c.doOnce(ctx, q)
		return attemptErr
	})
	return out, err
}

// rpcRequest is the JSON-RPC 2.0 envelope for one tools/call.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"params"`
}

// rpcMessage is the subset of a JSON-RPC 2.0 message the driver reads.
// Notifications carry Method and no ID; the response carries Result or
// Error.
type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result *callToolResult `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// callToolResult is the MCP tools/call result shape the driver consumes:
// structuredContent when the server sends it, the text content block as
// a fallback (the same JSON document, serialized).
type callToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent *searchPayload `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
}

// searchPayload is the you-search output: results.web[] with the same
// per-hit fields the keyed Search API returns (title, url, description,
// snippets, page_age). News, knowledge, and related_searches sections
// are ignored: frugal__search is a web search.
type searchPayload struct {
	Results struct {
		Web []struct {
			Title       string   `json:"title"`
			URL         string   `json:"url"`
			Description string   `json:"description,omitempty"`
			Snippets    []string `json:"snippets,omitempty"`
			PageAge     string   `json:"page_age,omitempty"`
		} `json:"web"`
	} `json:"results"`
}

// doOnce runs one HTTP attempt. The retry loop in Search wraps this; the
// returned error is already a *routing.Error.
func (c *Client) doOnce(ctx context.Context, q search.Query) (search.Results, error) {
	n := q.MaxResults
	if n <= 0 {
		n = 5
	}
	if n > 20 {
		n = 20
	}
	var rpc rpcRequest
	rpc.JSONRPC = "2.0"
	rpc.ID = 1
	rpc.Method = "tools/call"
	rpc.Params.Name = toolName
	rpc.Params.Arguments = map[string]any{
		"query": q.Text,
		"count": n,
	}
	if q.Freshness != "" {
		rpc.Params.Arguments["freshness"] = q.Freshness
	}
	body, err := json.Marshal(rpc)
	if err != nil {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("encode request: %w", err))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return search.Results{}, routing.Permanent(c.Name(), 0, fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	// Header-based routing hints from the 2026-07-28 revision. Gateways
	// that meter on them get the method and tool name without parsing
	// the body; servers on earlier revisions ignore them.
	req.Header.Set("Mcp-Method", rpc.Method)
	req.Header.Set("Mcp-Name", toolName)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return search.Results{}, &routing.Error{
			Provider: c.Name(), Kind: routing.ClassifyNetwork(err), Err: err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(snippet))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			msg += " (free tier is about 100 queries/day per IP; set YDC_API_KEY for the keyed youcom provider)"
		}
		return search.Results{}, &routing.Error{
			Provider: c.Name(),
			Kind:     routing.ClassifyHTTPStatus(resp.StatusCode),
			Status:   resp.StatusCode,
			Err:      fmt.Errorf("%s", msg),
		}
	}

	msg, err := readResponse(resp)
	if err != nil {
		// 200 with a body we can't read as a JSON-RPC response: give the
		// retry loop another shot, same as a malformed REST body.
		return search.Results{}, routing.Transient(c.Name(), resp.StatusCode, err)
	}
	if msg.Error != nil {
		// A protocol-level error is a fact about this request against
		// this server (unknown tool, invalid params): no retry, but the
		// router still falls through to the next provider.
		return search.Results{}, routing.Permanent(c.Name(), resp.StatusCode,
			fmt.Errorf("rpc error %d: %s", msg.Error.Code, msg.Error.Message))
	}
	if msg.Result == nil {
		return search.Results{}, routing.Transient(c.Name(), resp.StatusCode, fmt.Errorf("response carried neither result nor error"))
	}
	if msg.Result.IsError {
		text := strings.TrimSpace(contentText(msg.Result))
		if rateLimitSigns.MatchString(text) {
			// Quota refusal delivered as a tool error rather than an HTTP
			// 429. Report it as a 429 so the guard's cooldown applies.
			return search.Results{}, &routing.Error{
				Provider: c.Name(),
				Kind:     routing.KindTransient,
				Status:   http.StatusTooManyRequests,
				Err:      fmt.Errorf("rate limited: %s", truncate(text, 300)),
			}
		}
		return search.Results{}, routing.Permanent(c.Name(), resp.StatusCode,
			fmt.Errorf("tool error: %s", truncate(text, 300)))
	}

	payload := msg.Result.StructuredContent
	if payload == nil {
		// No structuredContent: the text block carries the same document.
		var fromText searchPayload
		if err := json.Unmarshal([]byte(contentText(msg.Result)), &fromText); err != nil {
			return search.Results{}, routing.Transient(c.Name(), resp.StatusCode, fmt.Errorf("decode tool result: %w", err))
		}
		payload = &fromText
	}

	items := make([]search.Item, 0, len(payload.Results.Web))
	for _, h := range payload.Results.Web {
		if h.URL == "" {
			continue
		}
		snippet := h.Description
		if snippet == "" && len(h.Snippets) > 0 {
			snippet = h.Snippets[0]
		}
		items = append(items, search.Item{
			Title:       h.Title,
			URL:         h.URL,
			Snippet:     snippet,
			PublishedAt: h.PageAge,
		})
	}
	return search.Results{Items: items, CostUSD: 0}, nil
}

// readResponse decodes the JSON-RPC response from either a plain JSON
// body or an SSE stream. In the SSE case the server may emit
// notifications (progress, log messages) before the result; those are
// skipped and the first message carrying a result or error wins. Bodies
// are capped at 4 MiB so a misbehaving upstream cannot exhaust memory.
func readResponse(resp *http.Response) (*rpcMessage, error) {
	body := io.LimitReader(resp.Body, 4<<20)
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType == "text/event-stream" {
		return readSSE(body)
	}
	var msg rpcMessage
	if err := json.NewDecoder(body).Decode(&msg); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &msg, nil
}

// readSSE walks a server-sent-events body: `data:` lines accumulate into
// one event, a blank line terminates it. Each event's data is one
// JSON-RPC message.
func readSSE(r io.Reader) (*rpcMessage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	var data []string
	flush := func() (*rpcMessage, bool) {
		if len(data) == 0 {
			return nil, false
		}
		joined := strings.Join(data, "\n")
		data = data[:0]
		var msg rpcMessage
		if err := json.Unmarshal([]byte(joined), &msg); err != nil {
			return nil, false // not JSON-RPC (keepalive comment, etc.); keep scanning
		}
		if msg.Result != nil || msg.Error != nil {
			return &msg, true
		}
		return nil, false // notification
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if msg, ok := flush(); ok {
				return msg, nil
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		default:
			// event:, id:, retry:, comments. Not needed to locate the result.
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read event stream: %w", err)
	}
	if msg, ok := flush(); ok {
		return msg, nil
	}
	return nil, fmt.Errorf("event stream ended without a JSON-RPC response")
}

// contentText joins the text content blocks of a tool result.
func contentText(r *callToolResult) string {
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
