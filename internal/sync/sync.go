package sync

import (
	"encoding/json"
	"fmt"
	"net/http"
)

const modelsDevURL = "https://models.dev/api.json"

// ModelsDevProvider represents a provider entry from the models.dev API.
type ModelsDevProvider struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	Models map[string]ModelsDevEntry `json:"models"`
}

// ModelsDevEntry represents a single model from the models.dev API.
type ModelsDevEntry struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	Family           string      `json:"family"`
	Reasoning        bool        `json:"reasoning"`
	ToolCall         bool        `json:"tool_call"`
	StructuredOutput bool        `json:"structured_output"`
	Temperature      bool        `json:"temperature"`
	Attachment       bool        `json:"attachment"`
	OpenWeights      bool        `json:"open_weights"`
	Cost             *CostInfo   `json:"cost"`
	Limit            *LimitInfo  `json:"limit"`
	Modalities       *Modalities `json:"modalities"`
}

type CostInfo struct {
	Input     float64 `json:"input"`  // per million tokens
	Output    float64 `json:"output"` // per million tokens
	CacheRead float64 `json:"cache_read"`
}

type LimitInfo struct {
	Context int `json:"context"`
	Input   int `json:"input"`
	Output  int `json:"output"`
}

type Modalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

// FetchModels fetches the full model catalog from models.dev.
// Returns a flat map of "provider/model" → entry, plus bare model names.
func FetchModels() (map[string]ModelsDevEntry, error) {
	resp, err := http.Get(modelsDevURL)
	if err != nil {
		return nil, fmt.Errorf("fetching models.dev: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models.dev returned %d", resp.StatusCode)
	}

	var providers map[string]ModelsDevProvider
	if err := json.NewDecoder(resp.Body).Decode(&providers); err != nil {
		return nil, fmt.Errorf("decoding models.dev response: %w", err)
	}

	// Flatten into a lookup map with multiple key forms per model
	models := make(map[string]ModelsDevEntry)
	for providerID, prov := range providers {
		for modelID, entry := range prov.Models {
			if entry.ID == "" {
				entry.ID = modelID
			}
			// Store as "provider/model" and bare "model"
			models[providerID+"/"+modelID] = entry
			// Bare model ID — first writer wins (prefer primary providers)
			if _, exists := models[modelID]; !exists {
				models[modelID] = entry
			}
		}
	}

	return models, nil
}

// CostPer1K converts models.dev per-million pricing to per-1K pricing.
func CostPer1K(perMillion float64) float64 {
	return perMillion / 1000
}
