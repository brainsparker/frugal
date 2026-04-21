package proxy

import (
	"errors"
	"strings"
	"testing"
)

func TestSanitizedUpstreamMessage_DoesNotLeakRawError(t *testing.T) {
	// Any provider error we pass through must be collapsed into one of a
	// small set of stable strings — never the raw upstream body.
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"timeout", errors.New("anthropic request: context deadline exceeded"), "upstream timeout"},
		{"rate limited", errors.New("openai error 429: rate limit exceeded, retry in 1s"), "upstream rate limited"},
		{"credentials", errors.New("gemini error 401: invalid api key sk-leaked-key"), "upstream rejected credentials"},
		{"unavailable", errors.New("openai error 503: service unavailable"), "upstream unavailable"},
		{"generic", errors.New("anthropic error 500: {\"echo\":\"sensitive-data\"}"), "upstream error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizedUpstreamMessage(tt.err)
			if got != tt.want {
				t.Fatalf("sanitizedUpstreamMessage = %q, want %q", got, tt.want)
			}
			// Crucial: the raw error text must not appear in the sanitized
			// output even for the generic bucket.
			if strings.Contains(got, "sk-leaked-key") || strings.Contains(got, "echo") || strings.Contains(got, "sensitive-data") {
				t.Fatalf("sanitized output leaked raw error: %q", got)
			}
		})
	}
}
