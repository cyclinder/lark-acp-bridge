// Package provider holds the registry of available agent adapters and the
// per-scope provider selection. The registry is built once at startup; the
// selection is a persisted scope -> provider-id map that lets each chat
// override the default provider via `/provider <id>`.
package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
)

// Entry describes one registered provider for display and lookup.
type Entry struct {
	ID          string
	DisplayName string
	Available   bool
}

// Registry maps provider ids to adapters and resolves the effective adapter
// for a scope (selection override, else the default).
type Registry struct {
	defaultID string
	mu        sync.Mutex
	adapters  map[string]agent.AgentAdapter
	selection *Selection
}

// NewRegistry creates an empty registry with the given default provider id
// and selection store.
func NewRegistry(defaultID string, selection *Selection) *Registry {
	return &Registry{
		defaultID: defaultID,
		adapters:  map[string]agent.AgentAdapter{},
		selection: selection,
	}
}

// Register adds an adapter. Its ID() must be unique.
func (r *Registry) Register(a agent.AgentAdapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[a.ID()] = a
}

// Get returns the adapter for an id, or nil if not registered.
func (r *Registry) Get(id string) agent.AgentAdapter {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.adapters[id]
}

// Default returns the default provider id.
func (r *Registry) Default() string { return r.defaultID }

// Selection returns the per-scope selection store.
func (r *Registry) Selection() *Selection { return r.selection }

// List returns all registered providers sorted by id. Availability is probed
// with a short timeout; providers whose binary is missing are still listed
// (marked unavailable) so `/provider` can explain why a switch failed.
func (r *Registry) List() []Entry {
	r.mu.Lock()
	ids := make([]string, 0, len(r.adapters))
	for id := range r.adapters {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	// Deterministic order by id.
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
	out := make([]Entry, 0, len(ids))
	for _, id := range ids {
		a := r.Get(id)
		probeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		avail := a.Available(probeCtx) == nil
		cancel()
		out = append(out, Entry{
			ID:          a.ID(),
			DisplayName: a.DisplayName(),
			Available:   avail,
		})
	}
	return out
}

// Resolve returns the effective adapter for a scope: the scope's selection if
// set and registered, otherwise the default. If the default is also missing
// (misconfiguration), nil is returned.
func (r *Registry) Resolve(scope string) agent.AgentAdapter {
	if r.selection != nil {
		if id := r.selection.Get(scope); id != "" {
			if a := r.Get(id); a != nil {
				return a
			}
		}
	}
	return r.Get(r.defaultID)
}

// Current returns the effective provider id for a scope (selection or
// default). Satisfies commands.ProviderResolver.
func (r *Registry) Current(scope string) string {
	if r.selection != nil {
		if id := r.selection.Get(scope); id != "" {
			if r.Get(id) != nil {
				return id
			}
		}
	}
	return r.defaultID
}

// Set records a provider selection for a scope. Satisfies
// commands.ProviderResolver.
func (r *Registry) Set(scope, id string) error {
	if r.selection == nil {
		return nil
	}
	return r.selection.Set(scope, id)
}

// Clear removes a scope's selection. Satisfies commands.ProviderResolver.
func (r *Registry) Clear(scope string) error {
	if r.selection == nil {
		return nil
	}
	return r.selection.Clear(scope)
}

// Selection is a thread-safe, file-backed scope -> provider-id map.
type Selection struct {
	path string
	mu   sync.Mutex
	data map[string]string
}

// NewSelection creates a Selection backed by providers.json under home.
func NewSelection(home string) *Selection {
	return &Selection{
		path: filepath.Join(home, "providers.json"),
		data: map[string]string{},
	}
}

// Load reads the backing file if present. Missing file is not an error.
func (s *Selection) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, &s.data)
}

func (s *Selection) save() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

// Get returns the selected provider id for a scope, or "" if unset.
func (s *Selection) Get(scope string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[scope]
}

// Set records a provider selection for a scope and persists.
func (s *Selection) Set(scope, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[scope] = id
	return s.save()
}

// Clear removes a scope's selection and persists.
func (s *Selection) Clear(scope string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, scope)
	return s.save()
}
