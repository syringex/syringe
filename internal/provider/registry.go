package provider

import (
	"fmt"
	"slices"
	"sync"
)

// Registry maps a reference tag to the Provider that handles it. The zero
// value is an empty Registry ready to use.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// Register adds p under its own Tag(). It errors if that tag is already
// registered.
func (r *Registry) Register(p Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	tag := p.Tag()
	if r.providers == nil {
		r.providers = make(map[string]Provider)
	}
	if _, exists := r.providers[tag]; exists {
		return fmt.Errorf("provider tag %q already registered", tag)
	}
	r.providers[tag] = p
	return nil
}

// Get returns the provider registered for tag, if any.
func (r *Registry) Get(tag string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[tag]
	return p, ok
}

// Tags returns all registered tags, sorted for deterministic output.
func (r *Registry) Tags() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tags := make([]string, 0, len(r.providers))
	for t := range r.providers {
		tags = append(tags, t)
	}
	slices.Sort(tags)
	return tags
}
