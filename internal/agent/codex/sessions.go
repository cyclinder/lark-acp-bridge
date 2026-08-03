// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
)

// sessionsCacheTTL bounds how often /sessions re-scans the rollout tree.
// The scan touches every rollout file under ~/.codex/sessions, so repeated
// calls within the TTL reuse the cached list. It is deliberately short:
// /sessions <N> resolves its index against the cached numbering the user
// just saw.
const sessionsCacheTTL = 30 * time.Second

// titleScanLineCap bounds how many lines of a rollout file are read when
// looking for the first user message (used as the session title). The meta
// line plus the first user message are always near the top.
const titleScanLineCap = 200

// ListSessions implements the commands.SessionLister seam by scanning the
// Codex CLI's on-disk session store (~/.codex/sessions/**/rollout-*.jsonl).
// Codex has no machine-readable session-list command; the rollout files are
// the only surface. Each file starts with a session_meta record carrying
// the thread id and cwd; the first user_message event serves as the title.
// Results are sorted most-recently-updated first and cached for
// sessionsCacheTTL.
func (a *Adapter) ListSessions(_ context.Context) ([]agent.SessionInfo, error) {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	if time.Since(a.sessionsFetchedAt) < sessionsCacheTTL && a.sessionsCache != nil {
		return a.sessionsCache, nil
	}
	sessions, err := scanRolloutSessions(codexHome())
	if err != nil {
		return nil, err
	}
	a.sessionsCache = sessions
	a.sessionsFetchedAt = time.Now()
	return sessions, nil
}

// codexHome returns the Codex CLI's state directory, honoring CODEX_HOME.
func codexHome() string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

// rolloutMeta mirrors the first record of a rollout file (session_meta).
type rolloutMeta struct {
	Payload struct {
		ID        string `json:"id"`
		Cwd       string `json:"cwd"`
		Timestamp string `json:"timestamp"`
	} `json:"payload"`
}

// rolloutEvent mirrors event_msg records; only user_message is used.
type rolloutEvent struct {
	Payload struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"payload"`
}

// scanRolloutSessions walks <home>/sessions for rollout-*.jsonl files and
// extracts one SessionInfo per file.
func scanRolloutSessions(home string) ([]agent.SessionInfo, error) {
	root := filepath.Join(home, "sessions")
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no codex sessions found (%s does not exist)", root)
		}
		return nil, err
	}
	sessions := []agent.SessionInfo{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasPrefix(d.Name(), "rollout-") || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		if info, ok := parseRolloutFile(path); ok {
			sessions = append(sessions, info)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

// parseRolloutFile extracts the session id, cwd, title, and mtime from one
// rollout file. ok=false means the file is not a parseable session rollout.
func parseRolloutFile(path string) (info agent.SessionInfo, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return agent.SessionInfo{}, false
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return agent.SessionInfo{}, false
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for lines := 0; scanner.Scan() && lines < titleScanLineCap; lines++ {
		line := scanner.Bytes()
		if lines == 0 {
			var meta rolloutMeta
			if err := json.Unmarshal(line, &meta); err != nil || meta.Payload.ID == "" {
				return agent.SessionInfo{}, false
			}
			info.ID = meta.Payload.ID
			info.Cwd = meta.Payload.Cwd
			continue
		}
		var ev rolloutEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Payload.Type == "user_message" {
			info.Title = firstLine(ev.Payload.Message, 60)
			break
		}
	}
	if info.ID == "" {
		return agent.SessionInfo{}, false
	}
	info.UpdatedAt = st.ModTime()
	return info, true
}

// firstLine returns the first line of s, squashed of surrounding space and
// truncated to n runes with an ellipsis.
func firstLine(s string, n int) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}
