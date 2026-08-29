package providers

import (
	"fmt"
	"log/slog"
	"sync"
)

// Registry holds all configured providers.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	logger    *slog.Logger
}

// NewRegistry creates a provider registry.
func NewRegistry(logger *slog.Logger) *Registry {
	return &Registry{
		providers: make(map[string]Provider),
		logger:    logger,
	}
}

// Register adds a provider to the registry.
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.providers[p.Name()] = p
	r.logger.Info("Provider registered", "provider", p.Name())
}

// Get returns a provider by name.
func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", name)
	}

	return p, nil
}

// List returns all registered providers.
func (r *Registry) List() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		list = append(list, p)
	}

	return list
}
