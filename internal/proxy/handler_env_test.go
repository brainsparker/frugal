package proxy

import "testing"

func TestParseMaxCostPerRequestUSD(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want float64
	}{
		{name: "empty uses default", raw: "", want: defaultMaxCostPerRequestUSD},
		{name: "whitespace uses default", raw: "   ", want: defaultMaxCostPerRequestUSD},
		{name: "invalid uses default", raw: "oops", want: defaultMaxCostPerRequestUSD},
		{name: "negative uses default", raw: "-1", want: defaultMaxCostPerRequestUSD},
		{name: "zero disables cap", raw: "0", want: 0},
		{name: "trimmed parses", raw: " 0.25 ", want: 0.25},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseMaxCostPerRequestUSD(tc.raw); got != tc.want {
				t.Fatalf("parseMaxCostPerRequestUSD(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestDecisionBufferSizeFromEnv_Default(t *testing.T) {
	original := lookupEnv
	lookupEnv = func(string) (string, bool) { return "", false }
	t.Cleanup(func() { lookupEnv = original })

	if got := decisionBufferSizeFromEnv(); got != defaultDecisionBufferSize {
		t.Fatalf("expected default buffer size %d, got %d", defaultDecisionBufferSize, got)
	}
}

func TestDecisionBufferSizeFromEnv_InvalidFallsBack(t *testing.T) {
	original := lookupEnv
	lookupEnv = func(string) (string, bool) { return "not-a-number", true }
	t.Cleanup(func() { lookupEnv = original })

	if got := decisionBufferSizeFromEnv(); got != defaultDecisionBufferSize {
		t.Fatalf("expected default buffer size %d, got %d", defaultDecisionBufferSize, got)
	}
}

func TestDecisionBufferSizeFromEnv_ClampsToMax(t *testing.T) {
	original := lookupEnv
	lookupEnv = func(string) (string, bool) { return "500000", true }
	t.Cleanup(func() { lookupEnv = original })

	if got := decisionBufferSizeFromEnv(); got != maxDecisionBufferSize {
		t.Fatalf("expected clamped buffer size %d, got %d", maxDecisionBufferSize, got)
	}
}

func TestDecisionBufferSizeFromEnv_UsesConfiguredValue(t *testing.T) {
	original := lookupEnv
	lookupEnv = func(string) (string, bool) { return "2048", true }
	t.Cleanup(func() { lookupEnv = original })

	if got := decisionBufferSizeFromEnv(); got != 2048 {
		t.Fatalf("expected configured buffer size 2048, got %d", got)
	}
}
