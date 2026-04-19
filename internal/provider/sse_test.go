package provider

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewSSEScanner_AllowsLargeLineBeyondDefaultScannerLimit(t *testing.T) {
	payload := "data: " + strings.Repeat("a", 100*1024) + "\n\n"
	s := NewSSEScanner(bytes.NewBufferString(payload))

	if !s.Scan() {
		t.Fatalf("expected scanner to read large SSE line, err=%v", s.Err())
	}

	if got := s.Text(); got != strings.TrimSuffix(payload, "\n\n") {
		t.Fatalf("unexpected scanned text length: got=%d", len(got))
	}
}

func TestNewSSEScanner_ErrorsWhenLineExceedsConfiguredMax(t *testing.T) {
	payload := "data: " + strings.Repeat("b", maxSSELineBytes+1) + "\n\n"
	s := NewSSEScanner(bytes.NewBufferString(payload))

	if s.Scan() {
		t.Fatal("expected scan to fail for oversized SSE line")
	}

	if err := s.Err(); err == nil {
		t.Fatal("expected scanner error for oversized SSE line")
	}
}
