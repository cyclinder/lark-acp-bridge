// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeRollout creates a minimal rollout file fixture under dir.
func writeRollout(t *testing.T, dir, name, content string, mtime time.Time) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}

const rolloutFixture = `{"timestamp":"2026-06-03T05:52:07.152Z","type":"session_meta","payload":{"id":"tid-1","timestamp":"2026-06-03T05:51:37.722Z","cwd":"/work/proj"}}
{"timestamp":"2026-06-03T05:52:07.245Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"instr"}]}}
{"timestamp":"2026-06-03T05:52:07.281Z","type":"event_msg","payload":{"type":"user_message","message":"Fix the flaky test\nand more details here"}}
{"timestamp":"2026-06-03T05:52:15.474Z","type":"event_msg","payload":{"type":"agent_message","message":"on it"}}
`

func TestParseRolloutFile(t *testing.T) {
	dir := t.TempDir()
	mtime := time.Date(2026, 6, 3, 6, 0, 0, 0, time.UTC)
	path := writeRollout(t, dir, "rollout-2026-06-03T05-51-37-tid-1.jsonl", rolloutFixture, mtime)

	info, ok := parseRolloutFile(path)
	if !ok {
		t.Fatal("parseRolloutFile: not ok")
	}
	if info.ID != "tid-1" || info.Cwd != "/work/proj" {
		t.Errorf("info = %+v", info)
	}
	if info.Title != "Fix the flaky test" {
		t.Errorf("title = %q, want first line of user message", info.Title)
	}
	if !info.UpdatedAt.Equal(mtime) {
		t.Errorf("updatedAt = %v, want file mtime %v", info.UpdatedAt, mtime)
	}
}

func TestParseRolloutFileBadMeta(t *testing.T) {
	dir := t.TempDir()
	path := writeRollout(t, dir, "rollout-bad.jsonl", `{"type":"event_msg","payload":{}}`+"\n", time.Now())
	if _, ok := parseRolloutFile(path); ok {
		t.Error("expected ok=false for a file without session_meta on line 1")
	}
}

func TestScanRolloutSessions(t *testing.T) {
	home := t.TempDir()
	old := time.Now().Add(-2 * time.Hour)
	recent := time.Now().Add(-1 * time.Hour)
	writeRollout(t, filepath.Join(home, "sessions", "2026", "06", "03"),
		"rollout-1.jsonl", rolloutFixture, old)
	writeRollout(t, filepath.Join(home, "sessions", "2026", "08", "01"),
		"rollout-2.jsonl", rolloutFixture, recent)
	// A non-rollout file and a broken rollout must be ignored.
	writeRollout(t, filepath.Join(home, "sessions"), "notes.txt", "junk", recent)
	writeRollout(t, filepath.Join(home, "sessions"), "rollout-broken.jsonl", "not json\n", recent)

	sessions, err := scanRolloutSessions(home)
	if err != nil {
		t.Fatalf("scanRolloutSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("len(sessions) = %d, want 2", len(sessions))
	}
	// Sorted most-recently-updated first: rollout-2 (recent) before rollout-1.
	if sessions[0].UpdatedAt.Before(sessions[1].UpdatedAt) {
		t.Errorf("sessions not sorted by updatedAt desc: %+v", sessions)
	}
}

func TestScanRolloutSessionsMissingRoot(t *testing.T) {
	home := t.TempDir()
	if _, err := scanRolloutSessions(home); err == nil {
		t.Fatal("expected error for missing sessions directory")
	}
}

func TestCodexListSessionsCache(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, filepath.Join(home, "sessions"), "rollout-1.jsonl", rolloutFixture, time.Now())
	t.Setenv("CODEX_HOME", home)
	a := New()
	sessions, err := a.ListSessions(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ListSessions = %d, %v", len(sessions), err)
	}
	// Delete the fixture; the cached second call must still return 1.
	if err := os.RemoveAll(filepath.Join(home, "sessions")); err != nil {
		t.Fatal(err)
	}
	sessions2, err := a.ListSessions(context.Background())
	if err != nil || len(sessions2) != 1 {
		t.Fatalf("cached ListSessions = %d, %v", len(sessions2), err)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("  hello\nworld  ", 60); got != "hello" {
		t.Errorf("firstLine multiline = %q", got)
	}
	long := string(make([]byte, 100))
	for i := range long {
		long = long[:i] + "a" + long[i+1:]
	}
	if got := firstLine(long, 10); got != "aaaaaaaaaa..." {
		t.Errorf("firstLine truncate = %q", got)
	}
}
