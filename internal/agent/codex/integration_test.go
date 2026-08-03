// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

//go:build integration

// This file contains integration tests that read the real Codex CLI session
// store. They are gated behind the "integration" build tag so they do not
// run in normal `go test`. Run with:
//
//	go test -tags integration ./internal/agent/codex/ -v -run TestSmokeListSessions
package codex

import (
	"context"
	"fmt"
	"testing"
)

// TestSmokeListSessions scans the real ~/.codex/sessions tree end to end.
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
