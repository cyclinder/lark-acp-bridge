// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package devin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
	"github.com/cognition/lark-acp-bridge/internal/agent/devin/acp"
)

// sessionsCacheTTL bounds how often /sessions spawns a fresh `devin acp`
// probe. It is deliberately short: the list changes as sessions are
// created, and /sessions <N> resolves its index against the cached
// numbering the user just saw.
const sessionsCacheTTL = 30 * time.Second

// ListSessions implements the commands.SessionLister seam: it enumerates
// the account's sessions via ACP session/list on a short-lived probe
// process. Results are cached for sessionsCacheTTL.
func (a *Adapter) ListSessions(ctx context.Context) ([]agent.SessionInfo, error) {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	if time.Since(a.sessionsFetchedAt) < sessionsCacheTTL && a.sessionsCache != nil {
		return a.sessionsCache, nil
	}
	sessions, err := a.probeSessions(ctx)
	if err != nil {
		return nil, err
	}
	a.sessionsCache = sessions
	a.sessionsFetchedAt = time.Now()
	return sessions, nil
}

// probeSessions performs one ACP handshake against a short-lived
// `devin acp` process and reads the session list.
func (a *Adapter) probeSessions(ctx context.Context) ([]agent.SessionInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.binary, "acp")
	cmd.Dir = os.TempDir()
	cmd.Env = os.Environ()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("devin acp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("devin acp stdout pipe: %w", err)
	}
	// devin acp writes noisy tracing logs to stderr; discard them.
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start devin acp: %w", err)
	}
	// The probe is short-lived; make sure the process never outlives it.
	// devin acp does not exit on stdin EOF, so kill it explicitly.
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	client := acp.NewClient(stdin, stdout)
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(ctx, acp.InitializeParams{
		ProtocolVersion: acp.ProtocolVersion,
		ClientInfo:      acp.ImplementationInfo{Name: "lark-acp-bridge", Version: "0.1.0"},
	}); err != nil {
		return nil, fmt.Errorf("devin acp initialize: %w", err)
	}
	res, err := client.SessionList(ctx)
	if err != nil {
		return nil, fmt.Errorf("devin acp session/list: %w", err)
	}

	sessions := make([]agent.SessionInfo, 0, len(res.Sessions))
	for _, s := range res.Sessions {
		if s.SessionID == "" {
			continue
		}
		info := agent.SessionInfo{
			ID:     s.SessionID,
			Title:  s.Title,
			Cwd:    s.Cwd,
			Locked: s.IsLocked(),
		}
		if t, err := time.Parse(time.RFC3339, s.UpdatedAt); err == nil {
			info.UpdatedAt = t
		}
		sessions = append(sessions, info)
	}
	return sessions, nil
}
