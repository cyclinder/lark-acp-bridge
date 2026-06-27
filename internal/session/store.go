// Package session maps a chat scope (chatId or chatId:threadId) to an agent
// session id and associated metadata.
package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Entry is the persisted state for one scope.
type Entry struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd,omitempty"`
	Model     string `json:"model,omitempty"`
}

// Store is a thread-safe, file-backed scope -> Entry map.
type Store struct {
	path string
	mu   sync.Mutex
	data map[string]Entry
}

// New creates a Store backed by sessions.json under home.
func New(home string) *Store {
	return &Store{
		path: filepath.Join(home, "sessions.json"),
		data: map[string]Entry{},
	}
}

// Load reads the backing file if it exists. Missing file is not an error.
func (s *Store) Load() error {
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

// Save writes the current state to disk.
func (s *Store) save() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

// Get returns the entry for a scope, or ok=false if unset.
func (s *Store) Get(scope string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[scope]
	return e, ok
}

// Set stores an entry for a scope and persists.
func (s *Store) Set(scope string, e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[scope] = e
	return s.save()
}

// Clear removes a scope's entry and persists.
func (s *Store) Clear(scope string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, scope)
	return s.save()
}

// SetModel updates only the model field for a scope.
func (s *Store) SetModel(scope, model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.data[scope]
	e.Model = model
	s.data[scope] = e
	return s.save()
}

// ListItem is one row in the session list returned by List.
type ListItem struct {
	Scope     string
	SessionID string
	Cwd       string
	Model     string
}

// List returns all stored sessions sorted by scope. Used by /resume.
func (s *Store) List() []ListItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ListItem, 0, len(s.data))
	for scope, e := range s.data {
		out = append(out, ListItem{
			Scope:     scope,
			SessionID: e.SessionID,
			Cwd:       e.Cwd,
			Model:     e.Model,
		})
	}
	// Simple insertion sort by scope for deterministic output.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Scope > out[j].Scope; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
