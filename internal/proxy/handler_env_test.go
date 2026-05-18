package proxy

import "testing"

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

func TestMaxCostPerRequestFromEnv_Default(t *testing.T) {
	original := lookupEnv
	lookupEnv = func(string) (string, bool) { return "", false }
	t.Cleanup(func() { lookupEnv = original })

	if got := maxCostPerRequestFromEnv(); got != defaultMaxCostPerRequestUSD {
		t.Fatalf("expected default max cost %.2f, got %.2f", defaultMaxCostPerRequestUSD, got)
	}
}

func TestMaxCostPerRequestFromEnv_InvalidFallsBack(t *testing.T) {
	tests := []string{"not-a-number", "-1", "NaN", "Inf", "-Inf"}
	for _, tc := range tests {
		t.Run(tc, func(t *testing.T) {
			original := lookupEnv
			lookupEnv = func(string) (string, bool) { return tc, true }
			t.Cleanup(func() { lookupEnv = original })

			if got := maxCostPerRequestFromEnv(); got != defaultMaxCostPerRequestUSD {
				t.Fatalf("expected default max cost %.2f, got %.2f", defaultMaxCostPerRequestUSD, got)
			}
		})
	}
}

func TestMaxCostPerRequestFromEnv_UsesConfiguredValue(t *testing.T) {
	original := lookupEnv
	lookupEnv = func(string) (string, bool) { return "2.5", true }
	t.Cleanup(func() { lookupEnv = original })

	if got := maxCostPerRequestFromEnv(); got != 2.5 {
		t.Fatalf("expected configured max cost 2.5, got %.2f", got)
	}
}
