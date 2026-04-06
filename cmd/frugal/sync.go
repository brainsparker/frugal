package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/frugalsh/frugal/internal/config"
	msync "github.com/frugalsh/frugal/internal/sync"
	"gopkg.in/yaml.v3"
)

// modelAliases maps our config model names to models.dev lookup keys.
// The sync flattens models.dev into both "provider/model" and bare "model" keys,
// so most models resolve directly. Aliases handle naming mismatches.
var modelAliases = map[string][]string{
	// Anthropic date-stamped names → models.dev names
	"claude-opus-4-20250918":   {"claude-opus-4-6", "openai/claude-opus-4-6"},
	"claude-sonnet-4-20250514": {"claude-sonnet-4", "claude-sonnet-4-6"},
	"claude-haiku-3.5":         {"claude-3-5-haiku", "claude-3.5-haiku"},
}

func runSync(configPath string) error {
	log.Println("fetching model pricing from models.dev...")

	catalog, err := msync.FetchModels()
	if err != nil {
		return fmt.Errorf("fetch failed: %w", err)
	}
	log.Printf("fetched %d model entries from models.dev", len(catalog))

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	updated := 0
	notFound := 0

	for providerName, pc := range cfg.Providers {
		for modelName, mc := range pc.Models {
			entry, found := lookupModel(catalog, providerName, modelName)
			if !found {
				log.Printf("  [skip] %s/%s — not found in models.dev", providerName, modelName)
				notFound++
				continue
			}

			changed := false

			if entry.Cost != nil {
				newInput := msync.CostPer1K(entry.Cost.Input)
				newOutput := msync.CostPer1K(entry.Cost.Output)
				if newInput != mc.CostPer1KInput || newOutput != mc.CostPer1KOutput {
					log.Printf("  [update] %s/%s: input $%.6f→$%.6f, output $%.6f→$%.6f per 1K tokens",
						providerName, modelName,
						mc.CostPer1KInput, newInput,
						mc.CostPer1KOutput, newOutput)
					mc.CostPer1KInput = newInput
					mc.CostPer1KOutput = newOutput
					changed = true
				}
			}

			if entry.Limit != nil && entry.Limit.Context > 0 && entry.Limit.Context != mc.Capabilities.MaxContext {
				log.Printf("  [update] %s/%s: context %d→%d",
					providerName, modelName, mc.Capabilities.MaxContext, entry.Limit.Context)
				mc.Capabilities.MaxContext = entry.Limit.Context
				changed = true
			}

			if entry.ToolCall != mc.Capabilities.ToolUse {
				log.Printf("  [update] %s/%s: tool_use %v→%v", providerName, modelName, mc.Capabilities.ToolUse, entry.ToolCall)
				mc.Capabilities.ToolUse = entry.ToolCall
				changed = true
			}
			if entry.StructuredOutput != mc.Capabilities.JSONMode {
				log.Printf("  [update] %s/%s: json_mode %v→%v", providerName, modelName, mc.Capabilities.JSONMode, entry.StructuredOutput)
				mc.Capabilities.JSONMode = entry.StructuredOutput
				changed = true
			}

			if changed {
				pc.Models[modelName] = mc
				updated++
			} else {
				log.Printf("  [ok] %s/%s — up to date", providerName, modelName)
			}
		}
		cfg.Providers[providerName] = pc
	}

	log.Printf("updated %d models, %d not found in catalog", updated, notFound)

	if updated > 0 {
		return writeConfig(configPath, cfg)
	}

	log.Println("no changes needed")
	return nil
}

func lookupModel(catalog map[string]msync.ModelsDevEntry, providerName, modelName string) (msync.ModelsDevEntry, bool) {
	// 1. Try "provider/model" (e.g., "openai/gpt-4o")
	if entry, ok := catalog[providerName+"/"+modelName]; ok {
		return entry, true
	}

	// 2. Try bare model name (e.g., "gpt-4o")
	if entry, ok := catalog[modelName]; ok {
		return entry, true
	}

	// 3. Try aliases
	if aliases, ok := modelAliases[modelName]; ok {
		for _, alias := range aliases {
			if entry, ok := catalog[alias]; ok {
				return entry, true
			}
			// Also try with provider prefix
			if entry, ok := catalog[providerName+"/"+alias]; ok {
				return entry, true
			}
		}
	}

	// 4. Fuzzy: find catalog entry containing the model name or vice versa
	for id, entry := range catalog {
		bare := id
		if idx := strings.LastIndex(id, "/"); idx >= 0 {
			bare = id[idx+1:]
		}
		if strings.Contains(bare, modelName) || strings.Contains(modelName, bare) {
			return entry, true
		}
	}

	return msync.ModelsDevEntry{}, false
}

func writeConfig(path string, cfg *config.Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	log.Printf("wrote updated config to %s", path)
	return nil
}
