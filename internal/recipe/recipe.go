// Package recipe loads named task recipes (fresh-facts, code-dev, …) and the
// step lists the executor runs per quality tier.
//
// A Recipe describes WHAT to do for a class of tasks — an ordered list of
// steps (MCP tool calls and/or routed chat-completion calls) that produces an
// answer at a predictable cost. The CLI (frugal run / route / compare) and
// the MCP server (frugal mcp serve) both consume Recipes through the same
// Registry, so the routing decision is identical no matter which surface the
// task entered through.
//
// Step execution lives in cmd/frugal/run.go (Phase 1 PR 5) — this package
// only defines the data shape, loads YAML, and lets the classifier pick a
// recipe ID for a free-form task string.
//
// See frugal-strategy-v6.md §6 for the recipe-model rationale and §8 for
// the component-status matrix that says which step types have executors
// wired today.
package recipe

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// ValidTiers are the quality tiers a recipe must define. Missing a tier is a
// load-time error — every recipe ships high / balanced / cost so callers can
// trust the --quality flag without per-recipe special-casing.
var ValidTiers = []string{"high", "balanced", "cost"}

// Recipe is the full record loaded from one YAML file.
type Recipe struct {
	ID          string                `yaml:"id"`
	Description string                `yaml:"description"`
	Classifier  Classifier            `yaml:"classifier"`
	Recipes     map[string]TierRecipe `yaml:"recipes"`
	Source      string                `yaml:"source,omitempty"`
	AsOf        string                `yaml:"as_of,omitempty"`
	Confidence  string                `yaml:"confidence,omitempty"`
	Workload    string                `yaml:"workload,omitempty"`
}

// Classifier captures the hints the task→recipe classifier uses to pick this
// recipe from a free-form task string. `hints` are keywords/phrases that
// suggest the task fits this recipe; `signals` are structured flags (e.g.
// requires_recency: true) that future classifier passes can match against
// extracted task features. Today only `hints` is consumed (see classify.go).
type Classifier struct {
	Hints   []string       `yaml:"hints,omitempty"`
	Signals map[string]any `yaml:"signals,omitempty"`
}

// TierRecipe is the step list and rationale for one (recipe, quality tier)
// pair.
type TierRecipe struct {
	Steps  []Step `yaml:"steps"`
	Reason string `yaml:"reason,omitempty"`
}

// Step is exactly one MCP tool call OR one routed chat call. Loader enforces
// exactly-one-of via UnmarshalYAML below — never both, never neither.
type Step struct {
	// Tool is the MCP tool name (e.g. "frugal__search"). When set, this is a
	// tool step and Chat must be nil.
	Tool string `yaml:"tool,omitempty"`
	// With is the tool's input arguments. Values may contain `{task}`,
	// `{step1.results}`, `{step2.output}` placeholders that the executor
	// resolves before the call.
	With map[string]any `yaml:"with,omitempty"`
	// Chat is a routed chat-completion step. When set, Tool must be empty.
	Chat *ChatStep `yaml:"chat,omitempty"`
}

// ChatStep is a routed chat-completion call. Model `auto` lets the router
// pick the cheapest model meeting the recipe's quality threshold; any other
// value pins the model.
type ChatStep struct {
	Model  string `yaml:"model"`
	System string `yaml:"system,omitempty"`
	Input  string `yaml:"input,omitempty"`
}

// IsTool reports whether this step calls an MCP tool.
func (s Step) IsTool() bool { return s.Tool != "" }

// IsChat reports whether this step calls a chat model.
func (s Step) IsChat() bool { return s.Chat != nil }

// UnmarshalYAML enforces that exactly one of {tool, chat} is set per step.
// Without this the strict-fields decoder would accept malformed steps that
// the executor would have to validate at runtime — better to catch at load.
func (s *Step) UnmarshalYAML(value *yaml.Node) error {
	// Decode into a sibling type to avoid recursion.
	type rawStep struct {
		Tool string         `yaml:"tool,omitempty"`
		With map[string]any `yaml:"with,omitempty"`
		Chat *ChatStep      `yaml:"chat,omitempty"`
	}
	var raw rawStep
	if err := value.Decode(&raw); err != nil {
		return err
	}
	hasTool := raw.Tool != ""
	hasChat := raw.Chat != nil
	if hasTool && hasChat {
		return fmt.Errorf("step at line %d: must declare `tool` OR `chat`, not both", value.Line)
	}
	if !hasTool && !hasChat {
		return fmt.Errorf("step at line %d: must declare either `tool` or `chat`", value.Line)
	}
	if hasTool && raw.Chat != nil {
		// guarded above; defensive.
		return fmt.Errorf("step at line %d: tool step cannot carry chat", value.Line)
	}
	if hasChat && raw.With != nil {
		return fmt.Errorf("step at line %d: chat step cannot carry `with` (use chat.input)", value.Line)
	}
	if hasChat && raw.Chat.Model == "" {
		return fmt.Errorf("step at line %d: chat step requires a model (use `auto` to let the router pick)", value.Line)
	}
	*s = Step{Tool: raw.Tool, With: raw.With, Chat: raw.Chat}
	return nil
}

// ChatModel returns the model named on the first chat step in the tier's
// step list, or "" if no chat step exists. Bench uses this to resolve a
// per-tier baseline model without having to know the recipe structure.
func (t TierRecipe) ChatModel() string {
	for _, s := range t.Steps {
		if s.IsChat() {
			return s.Chat.Model
		}
	}
	return ""
}

// HasToolStep reports whether the tier calls the named MCP tool at any
// position. Eval uses HasToolStep("frugal__search") to decide whether to
// attach a synthetic search tool to chat requests (preserving the
// tool-use-accuracy measurement from the Bundle.Search-era harness).
func (t TierRecipe) HasToolStep(name string) bool {
	for _, s := range t.Steps {
		if s.IsTool() && s.Tool == name {
			return true
		}
	}
	return false
}

// Registry is a read-only lookup table of known recipes. Construct via Load;
// the zero value is invalid.
type Registry struct {
	mu      sync.RWMutex
	recipes map[string]Recipe
	// order preserves the load order so Classify can use it as a tiebreaker
	// when multiple recipes match a task string equally well.
	order []string
}

// Load reads every *.yaml file in dir, parses it as a Recipe, and returns a
// populated Registry. An empty dir (no files) returns an empty registry and
// no error — recipe-based routing is opt-in, and running without any
// recipes is a valid configuration (e.g. for `frugal sync`).
func Load(dir string) (*Registry, error) {
	r := &Registry{recipes: map[string]Recipe{}}
	if dir == "" {
		return r, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, fmt.Errorf("recipe: read dir %q: %w", dir, err)
	}
	// Sort for deterministic load order — Classify treats earlier IDs as
	// higher priority on ties, which matters for reproducibility.
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, ".yaml") && !strings.HasSuffix(n, ".yml") {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		path := filepath.Join(dir, n)
		rec, err := loadFile(path)
		if err != nil {
			return nil, err
		}
		if _, dup := r.recipes[rec.ID]; dup {
			return nil, fmt.Errorf("recipe: duplicate id %q (in %s)", rec.ID, path)
		}
		r.recipes[rec.ID] = rec
		r.order = append(r.order, rec.ID)
	}
	return r, nil
}

func loadFile(path string) (Recipe, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Recipe{}, fmt.Errorf("recipe: read %q: %w", path, err)
	}
	var rec Recipe
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&rec); err != nil {
		return Recipe{}, fmt.Errorf("recipe: parse %q: %w", path, err)
	}
	if rec.ID == "" {
		return Recipe{}, fmt.Errorf("recipe: %q: missing id", path)
	}
	if len(rec.Recipes) == 0 {
		return Recipe{}, fmt.Errorf("recipe: %q: no tier recipes declared", path)
	}
	for _, tier := range ValidTiers {
		t, ok := rec.Recipes[tier]
		if !ok {
			return Recipe{}, fmt.Errorf("recipe: %q: missing %q tier", path, tier)
		}
		if len(t.Steps) == 0 {
			return Recipe{}, fmt.Errorf("recipe: %q: tier %q has no steps", path, tier)
		}
	}
	return rec, nil
}

// Get returns the full Recipe record for id. Second return is false when id
// is unknown.
func (r *Registry) Get(id string) (Recipe, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.recipes[id]
	return rec, ok
}

// Tier returns the TierRecipe for (id, tier). Second return is false when
// the recipe is unknown OR the tier isn't defined for that recipe (the
// latter can't happen for recipes loaded via Load, which enforces all three
// tiers, but is honest about the lookup semantics for synthetic registries
// built in tests).
func (r *Registry) Tier(id, tier string) (TierRecipe, bool) {
	rec, ok := r.Get(id)
	if !ok {
		return TierRecipe{}, false
	}
	t, ok := rec.Recipes[tier]
	return t, ok
}

// IDs returns a sorted list of registered recipe ids. Used by the
// "unknown recipe" error path and any UI that wants to render a menu.
func (r *Registry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.recipes))
	for id := range r.recipes {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Order returns the registry's load order — the sequence Classify uses to
// break ties when multiple recipes match a task equally well.
func (r *Registry) Order() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Len reports how many recipes are loaded. Zero is a valid state.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.recipes)
}
