package main

import (
	"strings"
	"testing"
)

func TestInjectEnv_OverridesExistingBaseURLsWithoutDuplicates(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"OPENAI_BASE_URL=http://old.local/v1",
		"OPENAI_API_BASE=http://old.local/v1",
		"OPENAI_API_KEY=sk-user-real-key",
	}

	got := injectEnv(env, "http://127.0.0.1:8080/v1", "frugal-session-token")

	if countEnvKey(got, "OPENAI_BASE_URL") != 1 {
		t.Fatalf("expected exactly one OPENAI_BASE_URL entry, got %d", countEnvKey(got, "OPENAI_BASE_URL"))
	}
	if countEnvKey(got, "OPENAI_API_BASE") != 1 {
		t.Fatalf("expected exactly one OPENAI_API_BASE entry, got %d", countEnvKey(got, "OPENAI_API_BASE"))
	}
	if countEnvKey(got, "OPENAI_API_KEY") != 1 {
		t.Fatalf("expected exactly one OPENAI_API_KEY entry, got %d", countEnvKey(got, "OPENAI_API_KEY"))
	}

	if valueForEnvKey(got, "OPENAI_BASE_URL") != "http://127.0.0.1:8080/v1" {
		t.Fatalf("OPENAI_BASE_URL not updated, got %q", valueForEnvKey(got, "OPENAI_BASE_URL"))
	}
	if valueForEnvKey(got, "OPENAI_API_BASE") != "http://127.0.0.1:8080/v1" {
		t.Fatalf("OPENAI_API_BASE not updated, got %q", valueForEnvKey(got, "OPENAI_API_BASE"))
	}
	if valueForEnvKey(got, "OPENAI_API_KEY") != "frugal-session-token" {
		t.Fatalf("OPENAI_API_KEY not replaced with session token, got %q", valueForEnvKey(got, "OPENAI_API_KEY"))
	}
}

func TestInjectEnv_AddsBaseURLsWhenMissing(t *testing.T) {
	env := []string{"PATH=/usr/bin"}
	got := injectEnv(env, "http://127.0.0.1:9090/v1", "")

	if valueForEnvKey(got, "OPENAI_BASE_URL") != "http://127.0.0.1:9090/v1" {
		t.Fatalf("OPENAI_BASE_URL missing or incorrect")
	}
	if valueForEnvKey(got, "OPENAI_API_BASE") != "http://127.0.0.1:9090/v1" {
		t.Fatalf("OPENAI_API_BASE missing or incorrect")
	}
	// Empty auth token means we must not inject a phantom OPENAI_API_KEY
	// when the caller supplied one; callers in production always supply one.
	if valueForEnvKey(got, "OPENAI_API_KEY") != "" {
		t.Fatalf("OPENAI_API_KEY should not be set when authToken is empty")
	}
}

func countEnvKey(env []string, key string) int {
	prefix := key + "="
	count := 0
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			count++
		}
	}
	return count
}

func valueForEnvKey(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix)
		}
	}
	return ""
}
