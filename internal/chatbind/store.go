// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package chatbind persists the mapping between a Feishu / Lark group chat
// created by the /open command and the working directory it was created for.
// It is the single source of truth for /open reuse decisions: when a user
// runs /open for a cwd that already has a bound group, that group is reused
// instead of creating a new one.
package chatbind

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Bind is the persisted record for one /open-created group.
type Bind struct {
	Name      string `json:"name"`            // group display name (e.g. "myapp", "myapp-2")
	Cwd       string `json:"cwd"`             // absolute working directory the group is bound to
	CreatedBy string `json:"createdBy,omitempty"` // open_id of the user who first bound this cwd
	CreatedAt int64  `json:"createdAt,omitempty"` // unix seconds
}

// Store is a thread-safe, file-backed chatID -> Bind map.
type Store struct {
	path string
	mu   sync.Mutex
	data map[string]Bind
}

// New creates a Store backed by chatbinds.json under home.
func New(home string) *Store {
	return &Store{
		path: filepath.Join(home, "chatbinds.json"),
		data: map[string]Bind{},
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

func (s *Store) save() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

// Set records a bind for a chatID and persists.
func (s *Store) Set(chatID string, b Bind) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[chatID] = b
	return s.save()
}

// Get returns the bind for a chatID, or ok=false if unset.
func (s *Store) Get(chatID string) (Bind, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.data[chatID]
	return b, ok
}

// FindByCwd returns the chatID and bind for a cwd, if any group is already
// bound to that exact working directory. Used by /open to reuse groups.
func (s *Store) FindByCwd(cwd string) (chatID string, b Bind, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, bind := range s.data {
		if bind.Cwd == cwd {
			return id, bind, true
		}
	}
	return "", Bind{}, false
}

// FindByName returns the chatID and bind for a group name, if any. Used for
// local name de-duplication when creating a new group.
func (s *Store) FindByName(name string) (chatID string, b Bind, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, bind := range s.data {
		if bind.Name == name {
			return id, bind, true
		}
	}
	return "", Bind{}, false
}

// ListItem is one row in the bind list returned by List.
type ListItem struct {
	ChatID string
	Bind   Bind
}

// List returns all binds sorted by chatID for deterministic output.
func (s *Store) List() []ListItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ListItem, 0, len(s.data))
	for id, b := range s.data {
		out = append(out, ListItem{ChatID: id, Bind: b})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChatID < out[j].ChatID })
	return out
}

// Clear removes a chatID's bind and persists.
func (s *Store) Clear(chatID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, chatID)
	return s.save()
}
