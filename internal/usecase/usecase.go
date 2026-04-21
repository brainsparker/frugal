// Package usecase loads named use cases (research-synthesis, code-dev, …)
// and the (capability → model) bundles the router picks per quality tier.
//
// Use cases are Frugal's primary product abstraction: callers declare
// what kind of work they're doing, Frugal delivers the bundle that's
// proven best for that work. See the project vision in
// .claude/projects/.../memory/project_vision.md for the full rationale.
//
// This package is capability-agnostic — it stores `search` and `rerank`
// fields on Bundle but doesn't care whether those capabilities have
// providers wired yet. Ring 1a ships chat-only; Rings 1b/1c populate
// the other fields without touching this package.
package usecase

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Bundle is the recommended mapping of capability → model for one
// (use case, quality tier) pair. Fields not yet populated by curation
// for a given tier are left zero-valued; consumers should check for
// empty strings rather than treat them as "no opinion."
type Bundle struct {
	Chat   string `yaml:"chat"`
	Search string `yaml:"search,omitempty"`
	Rerank string `yaml:"rerank,omitempty"`
	Reason string `yaml:"reason,omitempty"`
}

// UseCase is the full record loaded from one YAML file.
type UseCase struct {
	ID          string            `yaml:"id"`
	Description string            `yaml:"description"`
	Source      string            `yaml:"source"`
	AsOf        string            `yaml:"as_of"`
	Confidence  string            `yaml:"confidence"`
	Bundles     map[string]Bundle `yaml:"bundles"`
	Workload    string            `yaml:"workload,omitempty"`
}

// ValidTiers are the quality tiers a bundle must define. Missing a tier
// isn't fatal at load time — callers see an empty Bundle and fall through
// to the non-use-case routing path — but we warn during Load.
var ValidTiers = []string{"high", "balanced", "cost"}

// Registry is a read-only lookup table of known use cases. Construct via
// Load; zero values are invalid.
type Registry struct {
	mu    sync.RWMutex
	cases map[string]UseCase
}

// Load reads every *.yaml file in dir, parses it as a UseCase, and
// returns a populated Registry. An empty dir (no files) returns an empty
// registry and no error — use-case routing is opt-in, and running
// without any use cases is a valid configuration.
func Load(dir string) (*Registry, error) {
	r := &Registry{cases: map[string]UseCase{}}
	if dir == "" {
		return r, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, fmt.Errorf("usecase: read dir %q: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		path := filepath.Join(dir, name)
		uc, err := loadFile(path)
		if err != nil {
			return nil, err
		}
		if _, dup := r.cases[uc.ID]; dup {
			return nil, fmt.Errorf("usecase: duplicate id %q (in %s)", uc.ID, path)
		}
		r.cases[uc.ID] = uc
	}
	return r, nil
}

func loadFile(path string) (UseCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return UseCase{}, fmt.Errorf("usecase: read %q: %w", path, err)
	}
	var uc UseCase
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&uc); err != nil {
		return UseCase{}, fmt.Errorf("usecase: parse %q: %w", path, err)
	}
	if uc.ID == "" {
		return UseCase{}, fmt.Errorf("usecase: %q: missing id", path)
	}
	if len(uc.Bundles) == 0 {
		return UseCase{}, fmt.Errorf("usecase: %q: no bundles declared", path)
	}
	for _, tier := range ValidTiers {
		b, ok := uc.Bundles[tier]
		if !ok {
			return UseCase{}, fmt.Errorf("usecase: %q: missing %q tier", path, tier)
		}
		if b.Chat == "" {
			return UseCase{}, fmt.Errorf("usecase: %q: tier %q has empty chat model", path, tier)
		}
	}
	return uc, nil
}

// Get returns the full UseCase record for id. Second return is false
// when id is unknown.
func (r *Registry) Get(id string) (UseCase, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	uc, ok := r.cases[id]
	return uc, ok
}

// Bundle returns the bundle for (id, tier). Second return is false when
// the use case is unknown OR the tier isn't defined for that use case.
func (r *Registry) Bundle(id, tier string) (Bundle, bool) {
	uc, ok := r.Get(id)
	if !ok {
		return Bundle{}, false
	}
	b, ok := uc.Bundles[tier]
	return b, ok
}

// IDs returns a sorted list of registered use-case ids. Useful for the
// "unknown use case" 400 response and /v1/bundles index rendering.
func (r *Registry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.cases))
	for id := range r.cases {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Len reports how many use cases are loaded. Zero is a valid state.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.cases)
}
