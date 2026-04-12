package types

import "testing"

func TestParseQualityThreshold(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want QualityThreshold
	}{
		{name: "high", in: "high", want: QualityHigh},
		{name: "cost", in: "cost", want: QualityCost},
		{name: "balanced explicit", in: "balanced", want: QualityBalanced},
		{name: "uppercase", in: "COST", want: QualityCost},
		{name: "surrounding spaces", in: "  High  ", want: QualityHigh},
		{name: "invalid defaults to balanced", in: "fast", want: QualityBalanced},
		{name: "empty defaults to balanced", in: "", want: QualityBalanced},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseQualityThreshold(tt.in); got != tt.want {
				t.Fatalf("ParseQualityThreshold(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
