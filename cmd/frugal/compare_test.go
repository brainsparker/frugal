package main

import "testing"

func TestValidateBaselineModel(t *testing.T) {
	available := []string{"gpt-4o", "claude-sonnet-4", "gemini-2.5-pro"}

	if err := validateBaselineModel("gpt-4o", available); err != nil {
		t.Fatalf("expected known model to pass, got error: %v", err)
	}

	if err := validateBaselineModel("", available); err == nil {
		t.Fatal("expected empty baseline model to fail")
	}

	if err := validateBaselineModel("unknown-model", available); err == nil {
		t.Fatal("expected unknown baseline model to fail")
	}
}
