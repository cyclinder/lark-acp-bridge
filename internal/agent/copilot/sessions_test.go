// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package copilot

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const workspaceYAMLFixture = `id: 1313d8d4-7365-486f-a6a3-c6fb3846f4d8
cwd: /root/cyclinder/dynamo
git_root: /root/cyclinder/dynamo
repository: ai-dynamo/dynamo
host_type: github
branch: main
client_name: windsurf
user_named: false
summary_count: 0
created_at: 2026-06-09T10:19:38.135Z
updated_at: 2026-06-09T10:19:38.185Z
`

func TestParseWorkspaceYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.yaml")
	if err := os.WriteFile(path, []byte(workspaceYAMLFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	info, ok := parseWorkspaceYAML(path)
	if !ok {
		t.Fatal("parseWorkspaceYAML: not ok")
	}
	if info.ID != "1313d8d4-7365-486f-a6a3-c6fb3846f4d8" {
		t.Errorf("id = %q", info.ID)
	}
	if info.Cwd != "/root/cyclinder/dynamo" {
		t.Errorf("cwd = %q", info.Cwd)
	}
	if info.UpdatedAt.IsZero() {
		t.Error("updatedAt not parsed")
	}
}

func TestParseWorkspaceYAMLMissing(t *testing.T) {
	if _, ok := parseWorkspaceYAML(filepath.Join(t.TempDir(), "nope.yaml")); ok {
		t.Error("expected ok=false for missing file")
	}
}

func TestParseWorkspaceYAMLNoID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.yaml")
	if err := os.WriteFile(path, []byte("cwd: /x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := parseWorkspaceYAML(path); ok {
		t.Error("expected ok=false when no id field")
	}
}

func TestScanSessionState(t *testing.T) {
	home := t.TempDir()
	mk := func(id string) {
		dir := filepath.Join(home, "session-state", id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "workspace.yaml"), []byte(workspaceYAMLFixture), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mk("aaaaaaaa-0000-0000-0000-000000000001")
	mk("bbbbbbbb-0000-0000-0000-000000000002")
	// A directory without workspace.yaml must be skipped.
	if err := os.MkdirAll(filepath.Join(home, "session-state", "empty"), 0o700); err != nil {
		t.Fatal(err)
	}

	sessions, err := scanSessionState(home)
	if err != nil {
		t.Fatalf("scanSessionState: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("len(sessions) = %d, want 2", len(sessions))
	}
}

func TestScanSessionStateMissingRoot(t *testing.T) {
	if _, err := scanSessionState(t.TempDir()); err == nil {
		t.Fatal("expected error for missing session-state directory")
	}
}

func TestCopilotListSessionsCache(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "session-state", "aaaaaaaa-0000-0000-0000-000000000001")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workspace.yaml"), []byte(workspaceYAMLFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COPILOT_HOME", home)
	a := New()
	sessions, err := a.ListSessions(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ListSessions = %d, %v", len(sessions), err)
	}
	if err := os.RemoveAll(filepath.Join(home, "session-state")); err != nil {
		t.Fatal(err)
	}
	sessions2, err := a.ListSessions(context.Background())
	if err != nil || len(sessions2) != 1 {
		t.Fatalf("cached ListSessions = %d, %v", len(sessions2), err)
	}
}
