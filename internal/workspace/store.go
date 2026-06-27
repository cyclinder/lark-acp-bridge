// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package workspace maps a chat scope to a working directory, and also
// maintains a separate table of named workspace aliases used by /ws.
package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Store is a thread-safe, file-backed store with two tables:
//   - chats: scope -> cwd (per-chat working directory)
//   - named: alias name -> cwd (named workspace aliases for /ws)
type Store struct {
	path  string
	mu    sync.Mutex
	chats map[string]string
	named map[string]string
}

// New creates a Store backed by workspaces.json under home.
func New(home string) *Store {
	return &Store{
		path:  filepath.Join(home, "workspaces.json"),
		chats: map[string]string{},
		named: map[string]string{},
	}
}

// workspaceFile is the structured on-disk format. Older versions of the
// bridge wrote a flat map[string]string; Load tolerates that legacy shape
// by treating every key as a chat scope.
type workspaceFile struct {
	Chats map[string]string `json:"chats,omitempty"`
	Named map[string]string `json:"named,omitempty"`
}

// Load reads the backing file if it exists. It accepts both the current
// structured {chats, named} format and the legacy flat map[string]string
// format (in which case all entries are loaded as chat scopes).
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
	// Try the structured format first.
	var wf workspaceFile
	if err := json.Unmarshal(data, &wf); err == nil && (wf.Chats != nil || wf.Named != nil) {
		if wf.Chats != nil {
			s.chats = wf.Chats
		}
		if wf.Named != nil {
			s.named = wf.Named
		}
		return nil
	}
	// Fall back to the legacy flat map.
	var flat map[string]string
	if err := json.Unmarshal(data, &flat); err != nil {
		return err
	}
	s.chats = flat
	return nil
}

func (s *Store) save() error {
	wf := workspaceFile{Chats: s.chats, Named: s.named}
	data, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

// CwdFor returns the cwd for a scope, or the fallback if unset.
func (s *Store) CwdFor(scope, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cwd, ok := s.chats[scope]; ok {
		return cwd
	}
	return fallback
}

// SetCwd stores the cwd for a scope and persists.
func (s *Store) SetCwd(scope, cwd string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chats[scope] = cwd
	return s.save()
}

// Clear removes a scope's cwd.
func (s *Store) Clear(scope string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.chats, scope)
	return s.save()
}

// SaveNamed records a named workspace alias (name -> cwd) and persists.
// Used by /ws save.
func (s *Store) SaveNamed(name, cwd string) error {
	if name == "" {
		return errors.New("workspace alias name is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.named[name] = cwd
	return s.save()
}

// GetNamed returns the cwd for a named alias, or "" if not found.
// Used by /ws use.
func (s *Store) GetNamed(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.named[name]
}

// NamedEntry is one alias returned by ListNamed.
type NamedEntry struct {
	Name string
	Cwd  string
}

// ListNamed returns all named aliases sorted by name. Used by /ws list.
func (s *Store) ListNamed() []NamedEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]NamedEntry, 0, len(s.named))
	for name, cwd := range s.named {
		out = append(out, NamedEntry{Name: name, Cwd: cwd})
	}
	// Sort by name for stable display.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Name > out[j].Name; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// RemoveNamed deletes a named alias. Returns false if the alias did not
// exist. Used by /ws remove.
func (s *Store) RemoveNamed(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.named[name]; !ok {
		return false
	}
	delete(s.named, name)
	_ = s.save()
	return true
}

// Resolve validates a user-supplied path and returns the absolute cwd.
// Relative paths are resolved against the process working directory.
// Rejects overly broad or non-existent directories.
func Resolve(input string) (string, error) {
	return ResolveFrom(input, "")
}

// ResolveFrom validates a user-supplied path and returns the absolute cwd.
// Relative paths are resolved against base when base is non-empty and is
// itself an absolute directory; otherwise they fall back to the process
// working directory (matching filepath.Abs semantics). Rejects overly
// broad or non-existent directories.
func ResolveFrom(input, base string) (string, error) {
	if input == "" {
		return "", errors.New("path is empty")
	}
	// Expand ~ to the home directory.
	if input == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		input = home
	} else if strings.HasPrefix(input, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		input = filepath.Join(home, input[2:])
	} else if !filepath.IsAbs(input) && base != "" {
		// Relative path: resolve against the chat's current cwd when
		// available, so `/cd subdir` and `/open subdir` behave like a
		// shell's `cd subdir` instead of being anchored to the bridge
		// process's working directory.
		input = filepath.Join(base, input)
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("path %s: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %s is not a directory", abs)
	}
	if isTooBroad(abs) {
		return "", fmt.Errorf("path %s is too broad; pick a project subdirectory", abs)
	}
	return abs, nil
}

// isTooBroad rejects root, home, and common system/temp roots.
func isTooBroad(abs string) bool {
	// Resolve symlinks for / and home comparison.
	clean := filepath.Clean(abs)
	if clean == "/" {
		return true
	}
	home, _ := os.UserHomeDir()
	if clean == home {
		return true
	}
	// Reject well-known system/temp roots.
	for _, bad := range []string{"/tmp", "/var", "/etc", "/usr", "/bin", "/sbin", "/sys", "/proc"} {
		if clean == bad {
			return true
		}
	}
	return false
}
