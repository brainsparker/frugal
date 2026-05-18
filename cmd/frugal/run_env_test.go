package main

import "testing"

func TestRecipeDirTrimsWhitespaceEnv(t *testing.T) {
	t.Setenv("FRUGAL_USE_CASES_DIR", "   custom/use_cases  \n")
	if got := recipeDir(); got != "custom/use_cases" {
		t.Fatalf("recipeDir() = %q, want %q", got, "custom/use_cases")
	}
}

func TestRecipeDirFallsBackWhenEnvWhitespaceOnly(t *testing.T) {
	t.Setenv("FRUGAL_USE_CASES_DIR", "   \n\t")
	if got := recipeDir(); got != "config/use_cases" {
		t.Fatalf("recipeDir() = %q, want %q", got, "config/use_cases")
	}
}

func TestConfigPathOrDefaultTrimsWhitespaceEnv(t *testing.T) {
	t.Setenv("FRUGAL_CONFIG", "  ./config/custom.yaml  \n")
	if got := configPathOrDefault(); got != "./config/custom.yaml" {
		t.Fatalf("configPathOrDefault() = %q, want %q", got, "./config/custom.yaml")
	}
}

func TestConfigPathOrDefaultFallsBackWhenEnvWhitespaceOnly(t *testing.T) {
	t.Setenv("FRUGAL_CONFIG", "   \t\n")
	if got := configPathOrDefault(); got != "config/models.yaml" {
		t.Fatalf("configPathOrDefault() = %q, want %q", got, "config/models.yaml")
	}
}
