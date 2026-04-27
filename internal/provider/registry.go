/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package provider

import (
	"fmt"
	"sort"
)

// Registry maintains registered resource providers
// Providers are registered at operator startup and looked up by type during reconciliation
type Registry struct {
	providers map[string]ResourceProvider
	// resourcesByProvider caches the resources for each provider after initialization
	resourcesByProvider map[string][]Resource
}

// NewRegistry creates a new provider registry
func NewRegistry() *Registry {
	return &Registry{
		providers:           make(map[string]ResourceProvider),
		resourcesByProvider: make(map[string][]Resource),
	}
}

// Register adds a provider to the registry
// Returns an error if a provider with the same name is already registered
func (r *Registry) Register(provider ResourceProvider) error {
	name := provider.Name()
	if _, exists := r.providers[name]; exists {
		return fmt.Errorf("provider %s already registered", name)
	}
	r.providers[name] = provider
	return nil
}

// Get retrieves a provider by name
// Returns an error if the provider type is unknown
func (r *Registry) Get(providerType string) (ResourceProvider, error) {
	provider, exists := r.providers[providerType]
	if !exists {
		return nil, fmt.Errorf("unknown provider type: %s", providerType)
	}
	return provider, nil
}

// List returns all registered provider names sorted alphabetically
func (r *Registry) List() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SetResources caches the resources for a provider after initialization
// This is called by main.go after each provider is initialized
func (r *Registry) SetResources(providerType string, resources []Resource) {
	r.resourcesByProvider[providerType] = resources
}

// GetResources returns the cached resources for a provider
// Returns nil if the provider hasn't been initialized or doesn't exist
func (r *Registry) GetResources(providerType string) []Resource {
	return r.resourcesByProvider[providerType]
}

// GetResource finds a specific resource by name for a given provider
// Returns nil if not found
func (r *Registry) GetResource(providerType, resourceName string) *Resource {
	resources := r.resourcesByProvider[providerType]
	for i := range resources {
		if resources[i].Name == resourceName {
			return &resources[i]
		}
	}
	return nil
}
