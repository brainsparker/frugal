package types

import "testing"

func TestParseQualityThreshold_NormalizedInputs(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   QualityThreshold
		wantOK bool
	}{
		{name: "exact high", in: "high", want: QualityHigh, wantOK: true},
		{name: "uppercase high", in: "HIGH", want: QualityHigh, wantOK: true},
		{name: "spaced high", in: "  high  ", want: QualityHigh, wantOK: true},
		{name: "mixed-case cost", in: "CoSt", want: QualityCost, wantOK: true},
		{name: "spaced balanced", in: " balanced ", want: QualityBalanced, wantOK: true},
		{name: "unknown returns not-ok", in: "fast", want: QualityBalanced, wantOK: false},
		{name: "empty returns not-ok", in: "", want: QualityBalanced, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseQualityThreshold(tt.in)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("ParseQualityThreshold(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
