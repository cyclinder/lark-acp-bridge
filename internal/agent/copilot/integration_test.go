// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package copilot

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
)

// Manual end-to-end smoke test against the real copilot binary:
//
//	go test -tags integration ./internal/agent/copilot/ -v -timeout 180s -run TestSmoke

func TestSmokeListModels(t *testing.T) {
	a := New()
	if err := a.Available(context.Background()); err != nil {
		t.Skipf("copilot not available: %v", err)
	}
	models, currentID, err := a.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("empty model list")
	}
	fmt.Printf("  current=%s models=%d\n", currentID, len(models))
	for _, m := range models {
		fmt.Printf("    %s (%s)\n", m.Value, m.Name)
	}
	// Second call must be served from the cache.
	models2, _, err := a.ListModels(context.Background())
	if err != nil || len(models2) != len(models) {
		t.Fatalf("cached ListModels: err=%v len=%d want %d", err, len(models2), len(models))
	}
}

func TestSmokeRealCopilotTurn(t *testing.T) {
	a := New()
	if err := a.Available(context.Background()); err != nil {
		t.Skipf("copilot not available: %v", err)
	}
	cwd := t.TempDir()

	runTurn := func(prompt, sessionID string) (string, error) {
		run, err := a.Run(context.Background(), agent.RunOptions{
			Prompt:    prompt,
			Cwd:       cwd,
			SessionID: sessionID,
			StopGrace: 5 * time.Second,
		})
		if err != nil {
			return "", err
		}
		var text, sid string
		for ev := range run.Events() {
			switch ev.Type {
			case agent.EventText:
				text += ev.Delta
			case agent.EventDone:
				sid = ev.SessionID
			case agent.EventError:
				return sid, ev.Err
			default:
				fmt.Printf("  event type=%d tool=%s\n", ev.Type, ev.ToolName)
			}
		}
		fmt.Printf("  turn text=%q sid=%s\n", text, sid)
		if text == "" {
			return sid, fmt.Errorf("empty assistant text")
		}
		return sid, nil
	}

	sid, err := runTurn("Remember the codeword PINEAPPLE and reply with exactly: STORED", "")
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if sid == "" {
		t.Fatal("no session id from first turn")
	}
	if _, err := runTurn("Write the codeword I asked you to remember into a file named codeword.txt, then reply with exactly: WROTE", sid); err != nil {
		t.Fatalf("resume turn: %v", err)
	}
	content, err := os.ReadFile(cwd + "/codeword.txt")
	if err != nil {
		t.Fatalf("read codeword.txt: %v", err)
	}
	fmt.Printf("  codeword.txt=%q\n", string(content))
}

// TestSmokeListSessions scans the real ~/.copilot/session-state tree end to
// end.
func TestSmokeListSessions(t *testing.T) {
	a := New()
	sessions, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("empty session list")
	}
	fmt.Printf("%d sessions\n", len(sessions))
	for i, s := range sessions {
		if i >= 10 {
			fmt.Printf("    ...\n")
			break
		}
		fmt.Printf("    %s | %s | %s | %s\n", s.ID, s.Title, s.Cwd, s.UpdatedAt.Format("2006-01-02 15:04"))
	}
	// Second call must be served from the cache.
	sessions2, err := a.ListSessions(context.Background())
	if err != nil || len(sessions2) != len(sessions) {
		t.Fatalf("cached ListSessions: err=%v len=%d want %d", err, len(sessions2), len(sessions))
	}
}
