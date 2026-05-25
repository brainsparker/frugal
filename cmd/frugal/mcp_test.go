package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frugalsh/frugal/internal/config"
)

// TestPrintRoutePlan_StdioDefault exercises the dry-run audit output with
// the shipped models.yaml under a no-env-vars baseline. The output is the
// trust artifact: it has to be deterministic (so reviewers can diff across
// runs) and complete (every YAML entry shown, with the gating reason).
func TestPrintRoutePlan_StdioDefault(t *testing.T) {
	t.Setenv("SERPER_API_KEY", "")
	t.Setenv("YDC_API_KEY", "")
	t.Setenv("SEARXNG_URL", "")
	t.Setenv("FIRECRAWL_API_KEY", "")
	t.Setenv("BROWSERLESS_TOKEN", "")

	cfg, err := config.Load("../../config/models.yaml")
	if err != nil {
		t.Fatalf("load shipped config: %v", err)
	}

	var buf bytes.Buffer
	printRoutePlan(&buf, cfg, "v0.test", "", false, false)
	got := buf.String()

	want := []string{
		"frugal mcp serve --dry-run  (frugal v0.test)",
		"no requests will be served; no network calls were made.",
		"frugal__search: registered with 1 provider(s):",
		"ACTIVE  marginalia     $0.0000/call  public endpoint (no key required)",
		"SKIP    searxng        $0.0000/call  skipped: $SEARXNG_URL not set",
		"SKIP    serper         $0.0010/call  skipped: $SERPER_API_KEY not set",
		"SKIP    youcom         $0.0050/call  skipped: $YDC_API_KEY not set",
		"frugal__extract: registered with 1 provider(s):",
		"ACTIVE  goreadability  $0.0000/call  in-process (no key required)",
		"SKIP    firecrawl      $0.0010/call  skipped: $FIRECRAWL_API_KEY not set",
		"frugal__browse: NOT REGISTERED — no providers gated in. Configured but skipped:",
		"SKIP    browserless    $0.0020/call  skipped: $BROWSERLESS_TOKEN not set",
		"transport:",
		"stdio (default — what Claude Desktop, Claude Code, and Cursor consume)",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("dry-run output missing line %q\n--- full output ---\n%s", w, got)
		}
	}

	// Cost ordering: marginalia ($0) must precede serper ($0.001), which
	// must precede youcom ($0.005). The auto-router contract — and the
	// reason this artifact is useful — depends on it.
	margIdx := strings.Index(got, "marginalia")
	serperIdx := strings.Index(got, "serper")
	youcomIdx := strings.Index(got, "youcom")
	if !(margIdx < serperIdx && serperIdx < youcomIdx) {
		t.Errorf("search providers must print in cost-ascending order; got positions: marginalia=%d serper=%d youcom=%d\n%s",
			margIdx, serperIdx, youcomIdx, got)
	}
}

// TestPrintRoutePlan_HTTPAuthMatrix covers the transport footer's three
// HTTP auth states. The "would refuse to start" line is load-bearing: a
// paranoid user reading the audit needs to see that HTTP without a token
// or --allow-anon is rejected by the real serve path.
func TestPrintRoutePlan_HTTPAuthMatrix(t *testing.T) {
	cfg, err := config.Load("../../config/models.yaml")
	if err != nil {
		t.Fatalf("load shipped config: %v", err)
	}
	cases := []struct {
		name      string
		authToken bool
		allowAnon bool
		wantLine  string
	}{
		{"bearer token", true, false, "auth: bearer token ($FRUGAL_AUTH_TOKEN set)"},
		{"allow anon", false, true, "auth: NONE (--allow-anon"},
		{"refuse start", false, false, "auth: would refuse to start"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			printRoutePlan(&buf, cfg, "v0.test", ":8765", tc.authToken, tc.allowAnon)
			if !strings.Contains(buf.String(), tc.wantLine) {
				t.Errorf("HTTP footer missing %q\n%s", tc.wantLine, buf.String())
			}
			if !strings.Contains(buf.String(), "streamable HTTP on :8765") {
				t.Errorf("HTTP footer missing addr line\n%s", buf.String())
			}
		})
	}
}
