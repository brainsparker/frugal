package obs

import (
	"log/slog"
	"testing"
)

func TestParseLevel_TrimsWhitespace(t *testing.T) {
	if got := parseLevel("  warning  "); got != slog.LevelWarn {
		t.Fatalf("expected warn level, got %v", got)
	}
}

func TestNormalizeLogFormat_TrimsAndLowercases(t *testing.T) {
	if got := normalizeLogFormat("  JSON  "); got != "json" {
		t.Fatalf("expected json, got %q", got)
	}
}
