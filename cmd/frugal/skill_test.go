package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	defaults "github.com/frugalsh/frugal/config"
	"github.com/frugalsh/frugal/internal/config"
)

// The agent skill served at frugal.sh/.well-known/agent-skills/frugal/SKILL.md
// carries a generated provider-chain section. These tests fail when that
// section drifts from the embedded default config + canonicalProviderOrder
// (the code's routing table), or when index.json's sha256 is stale.
//
// Regenerate with: FRUGAL_UPDATE_SKILL=1 go test ./cmd/frugal -run TestSkill

var (
	skillPath      = filepath.Join("..", "..", "docs", ".well-known", "agent-skills", "frugal", "SKILL.md")
	skillIndexPath = filepath.Join("..", "..", "docs", ".well-known", "agent-skills", "index.json")
)

const (
	skillBegin = "<!-- BEGIN GENERATED: provider chains (cmd/frugal/skill_test.go) -->"
	skillEnd   = "<!-- END GENERATED -->"
)

func skillUSD(c float64) string {
	return "$" + strconv.FormatFloat(c, 'f', -1, 64)
}

func needs(sp config.SearchProviderConfig) string {
	switch {
	case sp.APIKeyEnv != "" && sp.BaseURLEnv != "":
		return "`" + sp.APIKeyEnv + "` + `" + sp.BaseURLEnv + "`"
	case sp.APIKeyEnv != "":
		return "`" + sp.APIKeyEnv + "`"
	case sp.BaseURLEnv != "":
		return "`" + sp.BaseURLEnv + "` (self-hosted)"
	}
	return "nothing (keyless)"
}

// cheapChain mirrors what mcp serve does: register in
// canonicalProviderOrder (sortedProviderNames), then stable-sort by list
// price — the default `cheap` policy.
func cheapChain(capability string, m map[string]config.SearchProviderConfig) []string {
	var names []string
	for _, n := range sortedProviderNames(capability, m) {
		if !m[n].Disabled() {
			names = append(names, n)
		}
	}
	sort.SliceStable(names, func(i, j int) bool { return m[names[i]].CostPerCall < m[names[j]].CostPerCall })
	return names
}

func renderSkillChains(t *testing.T) string {
	t.Helper()
	cfg, err := config.Parse(defaults.DefaultModelsYAML)
	if err != nil {
		t.Fatalf("parse embedded default config: %v", err)
	}
	var b strings.Builder
	b.WriteString(skillBegin + "\n")
	b.WriteString("Default `cheap` order (list price ascending; equal prices keep a fixed\n")
	b.WriteString("order defined in code). A provider is only in the chain when what it\n")
	b.WriteString("needs is set.\n\n")
	for _, c := range []struct {
		tool, capability string
		m                map[string]config.SearchProviderConfig
	}{
		{"frugal__search", "search", cfg.SearchProviders},
		{"frugal__extract", "extract", cfg.ExtractProviders},
		{"frugal__browse", "browse", cfg.BrowseProviders},
	} {
		fmt.Fprintf(&b, "**`%s`** (%s)\n\n", c.tool, c.capability)
		b.WriteString("| # | Provider | List price / call | Needs |\n|---|---|---|---|\n")
		for i, n := range cheapChain(c.capability, c.m) {
			sp := c.m[n]
			fmt.Fprintf(&b, "| %d | `%s` | %s | %s |\n", i+1, n, skillUSD(sp.CostPerCall), needs(sp))
		}
		b.WriteString("\n")
	}
	b.WriteString(skillEnd)
	return b.String()
}

func TestSkillChainsMatchRoutingTable(t *testing.T) {
	raw, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	doc := string(raw)
	i, j := strings.Index(doc, skillBegin), strings.Index(doc, skillEnd)
	if i < 0 || j < i {
		t.Fatalf("SKILL.md is missing the generated-section markers")
	}
	want := renderSkillChains(t)
	got := doc[i : j+len(skillEnd)]
	if got == want {
		return
	}
	if os.Getenv("FRUGAL_UPDATE_SKILL") == "1" {
		doc = doc[:i] + want + doc[j+len(skillEnd):]
		if err := os.WriteFile(skillPath, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("SKILL.md provider chains drifted from the routing table; run FRUGAL_UPDATE_SKILL=1 go test ./cmd/frugal -run TestSkill\n--- want ---\n%s\n--- got ---\n%s", want, got)
}

func TestSkillIndexHashMatches(t *testing.T) {
	raw, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	want := hex.EncodeToString(sum[:])
	idxRaw, err := os.ReadFile(skillIndexPath)
	if err != nil {
		t.Fatal(err)
	}
	var idx map[string]any
	if err := json.Unmarshal(idxRaw, &idx); err != nil {
		t.Fatalf("index.json: %v", err)
	}
	skills, _ := idx["skills"].([]any)
	for _, s := range skills {
		m, _ := s.(map[string]any)
		if m["name"] != "frugal" {
			continue
		}
		if m["sha256"] == want {
			return
		}
		if os.Getenv("FRUGAL_UPDATE_SKILL") == "1" {
			old, _ := m["sha256"].(string)
			out := strings.Replace(string(idxRaw), old, want, 1)
			if err := os.WriteFile(skillIndexPath, []byte(out), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Fatalf("index.json sha256 = %v, SKILL.md hashes to %s; run FRUGAL_UPDATE_SKILL=1 go test ./cmd/frugal -run TestSkill", m["sha256"], want)
	}
	t.Fatal("index.json has no frugal skill entry")
}
