package tools

import (
	"strings"
	"testing"
)

func TestClassifyIntent(t *testing.T) {
	cases := []struct {
		name       string
		intent     string
		capability string
		url        string
		query      string
		freshness  string
		noteHas    string
	}{
		{
			name:       "schemed URL is an extract",
			intent:     "read https://example.com/post and summarize it",
			capability: "extract",
			url:        "https://example.com/post",
			noteHas:    "URL detected",
		},
		{
			name:       "trailing sentence punctuation stripped from URL",
			intent:     "summarize https://example.com/a.",
			capability: "extract",
			url:        "https://example.com/a",
		},
		{
			name:       "www URL gets a scheme",
			intent:     "fetch www.example.org/page",
			capability: "extract",
			url:        "https://www.example.org/page",
		},
		{
			name:       "bare domain with allowlisted TLD is a URL",
			intent:     "what does frugal.sh say",
			capability: "extract",
			url:        "https://frugal.sh",
		},
		{
			name:       "bare domain with path counts even off-allowlist",
			intent:     "grab example.zz/docs/intro",
			capability: "extract",
			url:        "https://example.zz/docs/intro",
		},
		{
			name:       "node.js is not a URL",
			intent:     "compare node.js frameworks",
			capability: "search",
			query:      "compare node.js frameworks",
			noteHas:    "no URL detected",
		},
		{
			name:       "version numbers are not URLs",
			intent:     "what changed in v1.2 of the spec",
			capability: "search",
		},
		{
			name:       "render cue with URL is a browse",
			intent:     "render https://app.example.com after js finishes",
			capability: "browse",
			url:        "https://app.example.com",
			noteHas:    "render cue",
		},
		{
			name:       "search cue outweighs a URL",
			intent:     "find alternatives to https://serper.dev",
			capability: "search",
			noteHas:    "search cue",
		},
		{
			name:       "plain question is a search",
			intent:     "best go yaml parser",
			capability: "search",
			query:      "best go yaml parser",
			noteHas:    "no URL detected",
		},
		{
			name:       "leading verb phrase stripped",
			intent:     "search for current NVIDIA earnings",
			capability: "search",
			query:      "current NVIDIA earnings",
		},
		{
			name:       "web search prefix stripped longest-first",
			intent:     "web search for MCP server registries",
			capability: "search",
			query:      "MCP server registries",
		},
		{
			name:       "freshness day",
			intent:     "latest AI chip announcements",
			capability: "search",
			freshness:  "day",
		},
		{
			name:       "freshness week",
			intent:     "recent Go releases this week",
			capability: "search",
			freshness:  "week",
		},
		{
			name:       "freshness month",
			intent:     "funding rounds this month",
			capability: "search",
			freshness:  "month",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyIntent(tc.intent)
			if got.Capability != tc.capability {
				t.Fatalf("capability = %q, want %q (note: %s)", got.Capability, tc.capability, got.Note)
			}
			if tc.url != "" && got.URL != tc.url {
				t.Errorf("url = %q, want %q", got.URL, tc.url)
			}
			if tc.query != "" && got.Query != tc.query {
				t.Errorf("query = %q, want %q", got.Query, tc.query)
			}
			if tc.freshness != "" && got.Freshness != tc.freshness {
				t.Errorf("freshness = %q, want %q", got.Freshness, tc.freshness)
			}
			if got.Note == "" {
				t.Error("note must always explain the decision")
			}
			if tc.noteHas != "" && !strings.Contains(got.Note, tc.noteHas) {
				t.Errorf("note = %q, want it to mention %q", got.Note, tc.noteHas)
			}
		})
	}
}

func TestClassifyIntent_MultipleURLsNoted(t *testing.T) {
	got := ClassifyIntent("diff https://a.example.com/x against https://b.example.com/y")
	if got.Capability != "extract" || got.URL != "https://a.example.com/x" {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(got.Note, "multiple URLs") {
		t.Errorf("note = %q, want a multiple-URLs caveat", got.Note)
	}
}
