package provider

import "fmt"

// Registry maps model names to their providers.
type Registry struct {
	providers map[string]Provider // provider name → Provider
	modelMap  map[string]string   // model name → provider name
}

func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
		modelMap:  make(map[string]string),
	}
}

// Register adds a provider and indexes all its models.
func (r *Registry) Register(p Provider) {
	r.providers[p.Name()] = p
	for _, model := range p.Models() {
		r.modelMap[model] = p.Name()
	}
}

// Resolve returns the provider for a given model name.
func (r *Registry) Resolve(model string) (Provider, error) {
	providerName, ok := r.modelMap[model]
	if !ok {
		return nil, fmt.Errorf("unknown model: %s", model)
	}
	return r.providers[providerName], nil
}

// GetProvider returns a provider by its name.
func (r *Registry) GetProvider(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", name)
	}
	return p, nil
}

// AllModels returns all registered model names.
func (r *Registry) AllModels() []string {
	models := make([]string, 0, len(r.modelMap))
	for m := range r.modelMap {
		models = append(models, m)
	}
	return models
}
