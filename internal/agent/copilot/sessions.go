// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package copilot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
)

// sessionsCacheTTL bounds how often /sessions re-scans the session-state
// directory. It is deliberately short: /sessions <N> resolves its index
// against the cached numbering the user just saw.
const sessionsCacheTTL = 30 * time.Second

// ListSessions implements the commands.SessionLister seam by scanning the
// Copilot CLI's on-disk session store (~/.copilot/session-state/<uuid>/
// workspace.yaml). Copilot has no machine-readable session-list command
// (--resume without an id is an interactive picker); the per-session
// workspace.yaml is the only surface. Results are sorted
// most-recently-updated first and cached for sessionsCacheTTL.
func (a *Adapter) ListSessions(_ context.Context) ([]agent.SessionInfo, error) {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	if time.Since(a.sessionsFetchedAt) < sessionsCacheTTL && a.sessionsCache != nil {
		return a.sessionsCache, nil
	}
	sessions, err := scanSessionState(copilotHome())
	if err != nil {
		return nil, err
	}
	a.sessionsCache = sessions
	a.sessionsFetchedAt = time.Now()
	return sessions, nil
}

// copilotHome returns the Copilot CLI's state directory, honoring
// COPILOT_HOME (mirrors readSettingsModel in models.go).
func copilotHome() string {
	if dir := os.Getenv("COPILOT_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".copilot")
}

// scanSessionState reads <home>/session-state/<uuid>/workspace.yaml for
// every session directory and extracts one SessionInfo per entry.
func scanSessionState(home string) ([]agent.SessionInfo, error) {
	root := filepath.Join(home, "session-state")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no copilot sessions found (%s does not exist)", root)
		}
		return nil, err
	}
	sessions := []agent.SessionInfo{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if info, ok := parseWorkspaceYAML(filepath.Join(root, e.Name(), "workspace.yaml")); ok {
			sessions = append(sessions, info)
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

// parseWorkspaceYAML extracts the session id, cwd, name, and updated_at
// from one workspace.yaml. The file is a flat key: value mapping (no
// nesting), so a simple line split suffices — no YAML dependency needed.
// ok=false means the file is missing or carries no session id.
func parseWorkspaceYAML(path string) (info agent.SessionInfo, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return agent.SessionInfo{}, false
	}
	var updated string
	for line := range strings.Lines(string(data)) {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "id":
			info.ID = value
		case "cwd":
			info.Cwd = value
		case "name":
			info.Title = value
		case "updated_at":
			updated = value
		}
	}
	if info.ID == "" {
		return agent.SessionInfo{}, false
	}
	if t, err := time.Parse(time.RFC3339, updated); err == nil {
		info.UpdatedAt = t
	}
	return info, true
}
