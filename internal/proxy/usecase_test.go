package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/frugalsh/frugal/internal/classifier"
	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/router"
	"github.com/frugalsh/frugal/internal/types"
	"github.com/frugalsh/frugal/internal/usecase"
)

// setupUseCaseHandler builds a handler wired with two mock models plus a
// small use-case registry that routes specific tiers to each. Chi is used
// here (unlike setupHandler) so the /v1/bundles/{useCase} path param is
// parsed by the actual production router.
func setupUseCaseHandler(t *testing.T) (*Handler, *httptest.Server) {
	t.Helper()

	reg := provider.NewRegistry()
	reg.Register(&mockProvider{
		name:   "mock",
		models: []string{"mock-cheap", "mock-premium"},
	})

	models := []router.ModelEntry{
		{Name: "mock-cheap", Provider: "mock", CostPer1KInput: 0.0001, CostPer1KOutput: 0.0004,
			Reasoning: 0.7, Coding: 0.68, Creative: 0.65, InstructFollowing: 0.72, MaxContext: 128000},
		{Name: "mock-premium", Provider: "mock", CostPer1KInput: 0.003, CostPer1KOutput: 0.015,
			Reasoning: 0.95, Coding: 0.93, Creative: 0.9, InstructFollowing: 0.95, MaxContext: 200000},
	}
	thresholds := map[string]router.Threshold{
		"high":     {MinReasoning: 0.88, MinCoding: 0.85, MinCreative: 0.82, MinInstructFollowing: 0.88},
		"balanced": {MinReasoning: 0.70, MinCoding: 0.68, MinCreative: 0.65, MinInstructFollowing: 0.72},
		"cost":     {},
	}

	// Build a tiny in-tmpdir use-case registry: two use cases, known tiers.
	dir := t.TempDir()
	writeYAML(t, filepath.Join(dir, "heavy-lift.yaml"), `
id: heavy-lift
description: uses the premium model
source: curated
as_of: "2026-04-21"
confidence: high
bundles:
  high:     { chat: mock-premium }
  balanced: { chat: mock-premium }
  cost:     { chat: mock-cheap }
`)
	writeYAML(t, filepath.Join(dir, "tight-budget.yaml"), `
id: tight-budget
description: always cheap
source: curated
as_of: "2026-04-21"
confidence: high
bundles:
  high:     { chat: mock-cheap }
  balanced: { chat: mock-cheap }
  cost:     { chat: mock-cheap }
`)
	useCases, err := usecase.Load(dir)
	if err != nil {
		t.Fatalf("Load use cases: %v", err)
	}

	cls := classifier.NewRuleBased()
	rtr := router.New(models, thresholds)
	h := NewHandlerWithUseCases(cls, rtr, reg, useCases)

	r := chi.NewRouter()
	r.Use(HeaderExtractionMiddleware)
	r.Post("/v1/chat/completions", h.ChatCompletions)
	r.Get("/v1/bundles", h.ListBundles)
	r.Get("/v1/bundles/{useCase}", h.GetBundle)

	ts := httptest.NewServer(r)
	return h, ts
}

func writeYAML(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestChatCompletions_UseCaseRoutesToBundleModel(t *testing.T) {
	_, ts := setupUseCaseHandler(t)
	defer ts.Close()

	body, _ := json.Marshal(types.ChatCompletionRequest{
		Model:    "auto",
		Messages: []types.Message{{Role: "user", Content: mustMarshalJSON("hi")}},
	})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Frugal-Use-Case", "heavy-lift")
	req.Header.Set("X-Frugal-Quality", "balanced")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(b))
	}
	if got := resp.Header.Get("X-Frugal-Model"); got != "mock-premium" {
		t.Errorf("expected X-Frugal-Model=mock-premium, got %q", got)
	}
	if got := resp.Header.Get("X-Frugal-Use-Case"); got != "heavy-lift" {
		t.Errorf("expected X-Frugal-Use-Case echo, got %q", got)
	}
}

func TestChatCompletions_UseCaseQualityTiers(t *testing.T) {
	_, ts := setupUseCaseHandler(t)
	defer ts.Close()

	cases := []struct {
		tier, wantModel string
	}{
		{"high", "mock-premium"},
		{"balanced", "mock-premium"},
		{"cost", "mock-cheap"},
	}
	for _, tc := range cases {
		t.Run(tc.tier, func(t *testing.T) {
			body, _ := json.Marshal(types.ChatCompletionRequest{
				Model:    "auto",
				Messages: []types.Message{{Role: "user", Content: mustMarshalJSON("hi")}},
			})
			req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Frugal-Use-Case", "heavy-lift")
			req.Header.Set("X-Frugal-Quality", tc.tier)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			resp.Body.Close()

			if got := resp.Header.Get("X-Frugal-Model"); got != tc.wantModel {
				t.Errorf("tier=%s: expected %s, got %s", tc.tier, tc.wantModel, got)
			}
		})
	}
}

func TestChatCompletions_UnknownUseCaseReturns400(t *testing.T) {
	_, ts := setupUseCaseHandler(t)
	defer ts.Close()

	body, _ := json.Marshal(types.ChatCompletionRequest{
		Model:    "auto",
		Messages: []types.Message{{Role: "user", Content: mustMarshalJSON("hi")}},
	})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Frugal-Use-Case", "legal-research") // not registered

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "unknown use case") {
		t.Errorf("expected 'unknown use case' in body, got %s", string(b))
	}
	if !strings.Contains(string(b), "heavy-lift") {
		t.Errorf("expected body to list known use cases including heavy-lift, got %s", string(b))
	}
}

func TestChatCompletions_AbsentUseCaseHeaderFallsThrough(t *testing.T) {
	// Absent header → existing classifier/router path runs. Default quality
	// is balanced; mock-cheap and mock-premium both clear it, so the router
	// picks cheapest (mock-cheap).
	_, ts := setupUseCaseHandler(t)
	defer ts.Close()

	body, _ := json.Marshal(types.ChatCompletionRequest{
		Model:    "auto",
		Messages: []types.Message{{Role: "user", Content: mustMarshalJSON("hi")}},
	})
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Frugal-Model"); got == "" {
		t.Errorf("expected routed model on fallthrough, got empty")
	}
	if got := resp.Header.Get("X-Frugal-Use-Case"); got != "" {
		t.Errorf("expected no use-case echo when header absent, got %q", got)
	}
}

func TestGetBundle_ReturnsJSONForKnownUseCase(t *testing.T) {
	_, ts := setupUseCaseHandler(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/bundles/heavy-lift?quality=balanced")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["use_case"] != "heavy-lift" {
		t.Errorf("wrong use_case: %v", got["use_case"])
	}
	if got["chat"] != "mock-premium" {
		t.Errorf("wrong chat: %v", got["chat"])
	}
	if got["quality"] != "balanced" {
		t.Errorf("wrong quality: %v", got["quality"])
	}
	if got["source"] != "curated" {
		t.Errorf("wrong source: %v", got["source"])
	}
}

func TestGetBundle_DefaultsToBalanced(t *testing.T) {
	_, ts := setupUseCaseHandler(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/bundles/heavy-lift")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got["quality"] != "balanced" {
		t.Errorf("default tier should be balanced, got %v", got["quality"])
	}
}

func TestGetBundle_UnknownUseCaseReturns404(t *testing.T) {
	_, ts := setupUseCaseHandler(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/bundles/legal-research")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestListBundles_ReturnsEveryRegisteredUseCase(t *testing.T) {
	_, ts := setupUseCaseHandler(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/bundles")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var got map[string][]map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got["data"]) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got["data"]))
	}
	// Sorted by ID, so heavy-lift < tight-budget.
	if got["data"][0]["id"] != "heavy-lift" {
		t.Errorf("unexpected order: %v", got["data"][0]["id"])
	}
	if got["data"][1]["id"] != "tight-budget" {
		t.Errorf("unexpected order: %v", got["data"][1]["id"])
	}
}

func TestUseCase_NoRegistryConfiguredReturns400ForChatHeader(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&mockProvider{name: "mock", models: []string{"mock-cheap"}})
	models := []router.ModelEntry{{Name: "mock-cheap", Provider: "mock",
		CostPer1KInput: 0.0001, CostPer1KOutput: 0.0004,
		Reasoning: 0.9, Coding: 0.9, Creative: 0.9, InstructFollowing: 0.9, MaxContext: 128000}}
	thresholds := map[string]router.Threshold{"balanced": {}}
	h := NewHandler(classifier.NewRuleBased(), router.New(models, thresholds), reg)

	r := chi.NewRouter()
	r.Use(HeaderExtractionMiddleware)
	r.Post("/v1/chat/completions", h.ChatCompletions)
	ts := httptest.NewServer(r)
	defer ts.Close()

	body, _ := json.Marshal(types.ChatCompletionRequest{
		Model:    "auto",
		Messages: []types.Message{{Role: "user", Content: mustMarshalJSON("hi")}},
	})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Frugal-Use-Case", "heavy-lift")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400 when use-case header is set but no registry, got %d: %s", resp.StatusCode, string(b))
	}
}
