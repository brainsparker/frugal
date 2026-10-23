package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frugalsh/frugal/internal/config"
)

func TestPrintRoutePlan_StdioDefault(t *testing.T) {
	cfg := &config.Config{
		SearchProviders: map[string]config.SearchProviderConfig{
			"wikipedia": {BaseURL: "https://en.wikipedia.org"},
		},
	}
	var buf bytes.Buffer
	printRoutePlan(&buf, cfg, "v0.1.0", "", false, false)
	out := buf.String()

	if !strings.Contains(out, "frugal mcp serve --dry-run") {
		t.Errorf("expected dry-run banner, got:\n%s", out)
	}
	if !strings.Contains(out, "wikipedia") {
		t.Errorf("expected wikipedia in output, got:\n%s", out)
	}
	if !strings.Contains(out, "ACTIVE") {
		t.Errorf("expected ACTIVE status, got:\n%s", out)
	}
	if !strings.Contains(out, "stdio (default") {
		t.Errorf("expected stdio transport note, got:\n%s", out)
	}
	if strings.Contains(out, "no network calls were made") {
		// good - should mention no network was called
	} else {
		t.Errorf("expected safety note about no network calls, got:\n%s", out)
	}
}

func TestPrintRoutePlan_HTTPAuthMatrix(t *testing.T) {
	tests := []struct {
		name          string
		addr          string
		authTokenSet  bool
		allowAnon     bool
		wantTransport string
		wantAuth      string
	}{
		{
			name:          "http_with_auth",
			addr:          "0.0.0.0:8765",
			authTokenSet:  true,
			allowAnon:     false,
			wantTransport: "streamable HTTP on 0.0.0.0:8765",
			wantAuth:      "FRUGAL_AUTH_TOKEN",
		},
		{
			name:          "http_anon",
			addr:          "127.0.0.1:8765",
			authTokenSet:  false,
			allowAnon:     true,
			wantTransport: "streamable HTTP on 127.0.0.1:8765",
			wantAuth:      "NONE (--allow-anon",
		},
		{
			name:          "http_no_auth_no_anon",
			addr:          "0.0.0.0:8765",
			authTokenSet:  false,
			allowAnon:     false,
			wantTransport: "streamable HTTP on 0.0.0.0:8765",
			wantAuth:      "would refuse to start",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{}
			var buf bytes.Buffer
			printRoutePlan(&buf, cfg, "v0.1.0", tt.addr, tt.authTokenSet, tt.allowAnon)
			out := buf.String()

			if !strings.Contains(out, tt.wantTransport) {
				t.Errorf("expected transport %q in output, got:\n%s", tt.wantTransport, out)
			}
			if !strings.Contains(out, tt.wantAuth) {
				t.Errorf("expected auth text %q in output, got:\n%s", tt.wantAuth, out)
			}
		})
	}
}