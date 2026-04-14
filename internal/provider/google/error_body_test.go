package google

import (
	"strings"
	"testing"
)

func TestReadErrorBody_TruncatesLargeBody(t *testing.T) {
	in := strings.Repeat("a", errorBodyLimit+32)
	out := readErrorBody(strings.NewReader(in))

	if !strings.HasSuffix(out, "... (truncated)") {
		t.Fatalf("expected truncation suffix")
	}
	if len(out) <= errorBodyLimit {
		t.Fatalf("expected output to include suffix beyond limit, got len=%d", len(out))
	}
}

func TestReadErrorBody_SmallBodyUnchanged(t *testing.T) {
	in := "provider unavailable"
	out := readErrorBody(strings.NewReader(in))
	if out != in {
		t.Fatalf("expected %q, got %q", in, out)
	}
}
