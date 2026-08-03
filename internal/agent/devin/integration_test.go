// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

//go:build integration

// This file contains integration tests that spawn a real `devin acp`
// subprocess. They are gated behind the "integration" build tag so they
// do not run in normal `go test`. Run with:
//
//	go test -tags integration ./internal/agent/devin/ -v -timeout 120s -run TestSmokeListModels
package devin

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
)

// TestSmokeListModels probes the real devin acp model list end to end.
func TestSmokeListModels(t *testing.T) {
	a := New()
	if err := a.Available(context.Background()); err != nil {
		t.Skipf("devin not available: %v", err)
	}
	models, currentID, err := a.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("empty model list")
	}
	fmt.Printf("current: %s (%d models)\n", currentID, len(models))
	for _, m := range models {
		fmt.Printf("    %s (%s)\n", m.Value, m.Name)
	}
	// Second call must be served from the cache.
	models2, _, err := a.ListModels(context.Background())
	if err != nil || len(models2) != len(models) {
		t.Fatalf("cached ListModels: err=%v len=%d want %d", err, len(models2), len(models))
	}
}

// TestSmokeListSessions probes the real devin acp session list end to end.
func TestSmokeListSessions(t *testing.T) {
	a := New()
	if err := a.Available(context.Background()); err != nil {
		t.Skipf("devin not available: %v", err)
	}
	sessions, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	fmt.Printf("%d sessions\n", len(sessions))
	for _, s := range sessions {
		fmt.Printf("    %s | %s | %s | locked=%v\n", s.ID, s.Title, s.Cwd, s.Locked)
	}
	// Second call must be served from the cache.
	sessions2, err := a.ListSessions(context.Background())
	if err != nil || len(sessions2) != len(sessions) {
		t.Fatalf("cached ListSessions: err=%v len=%d want %d", err, len(sessions2), len(sessions))
	}
}

// TestSmokeLoadAndPromptSession loads an existing session via
// RunOptions.SessionID and runs one tiny prompt turn on it, exercising the
// session/load path (including the history-replay drainer) end to end.
func TestSmokeLoadAndPromptSession(t *testing.T) {
	a := New()
	defer a.CloseAll()
	if err := a.Available(context.Background()); err != nil {
		t.Skipf("devin not available: %v", err)
	}
	sessions, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	picked := ""
	cwd := ""
	for _, s := range sessions {
		if !s.Locked && s.Cwd != "" {
			picked, cwd = s.ID, s.Cwd
			break
		}
	}
	if picked == "" {
		t.Skip("no unlocked session with a cwd available")
	}
	t.Logf("loading session %s (cwd %s)", picked, cwd)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	r, err := a.Run(ctx, agent.RunOptions{
		Prompt:    "Reply with exactly: ok",
		Cwd:       cwd,
		Scope:     "integration-load",
		SessionID: picked,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var done agent.Event
	for ev := range r.Events() {
		if ev.Type == agent.EventError {
			t.Fatalf("run error: %v", ev.Err)
		}
		if ev.Type == agent.EventDone {
			done = ev
		}
	}
	if done.SessionID != picked {
		t.Errorf("done session = %q, want loaded session %q", done.SessionID, picked)
	}
}
