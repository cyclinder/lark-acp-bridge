// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSetGetClear(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}

	e, ok := s.Get("scope1")
	if ok {
		t.Fatalf("expected ok=false for unset scope, got %+v", e)
	}

	if err := s.Set("scope1", Entry{SessionID: "s1", Cwd: "/tmp", Model: "opus"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	e, ok = s.Get("scope1")
	if !ok {
		t.Fatal("expected ok=true after Set")
	}
	if e.SessionID != "s1" || e.Cwd != "/tmp" || e.Model != "opus" {
		t.Errorf("entry = %+v, want {s1 /tmp opus}", e)
	}

	if err := s.Clear("scope1"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	_, ok = s.Get("scope1")
	if ok {
		t.Fatal("expected ok=false after Clear")
	}
}

func TestStoreSetModel(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.Set("scope1", Entry{SessionID: "s1", Cwd: "/tmp"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModel("scope1", "sonnet"); err != nil {
		t.Fatal(err)
	}
	e, _ := s.Get("scope1")
	if e.Model != "sonnet" {
		t.Errorf("model = %q, want sonnet", e.Model)
	}
	if e.SessionID != "s1" {
		t.Errorf("sessionID changed = %q, want s1", e.SessionID)
	}
}

func TestStorePersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s1 := New(dir)
	if err := s1.Set("scope_a", Entry{SessionID: "a1", Cwd: "/a"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Set("scope_b", Entry{SessionID: "b1", Cwd: "/b"}); err != nil {
		t.Fatal(err)
	}

	// File should exist on disk.
	if _, err := os.Stat(filepath.Join(dir, "sessions.json")); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	// New store instance loads from disk.
	s2 := New(dir)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	e, ok := s2.Get("scope_a")
	if !ok || e.SessionID != "a1" {
		t.Errorf("scope_a after reload = %+v ok=%v", e, ok)
	}
	e, ok = s2.Get("scope_b")
	if !ok || e.SessionID != "b1" {
		t.Errorf("scope_b after reload = %+v ok=%v", e, ok)
	}
}

func TestStoreList(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	_ = s.Set("scope_c", Entry{SessionID: "c1", Cwd: "/c", Model: "opus"})
	_ = s.Set("scope_a", Entry{SessionID: "a1", Cwd: "/a"})
	_ = s.Set("scope_b", Entry{SessionID: "b1", Cwd: "/b"})

	items := s.List()
	if len(items) != 3 {
		t.Fatalf("List = %d items, want 3", len(items))
	}
	// Should be sorted by scope.
	if items[0].Scope != "scope_a" || items[1].Scope != "scope_b" || items[2].Scope != "scope_c" {
		t.Errorf("List order = %s %s %s, want scope_a scope_b scope_c",
			items[0].Scope, items[1].Scope, items[2].Scope)
	}
	if items[2].Model != "opus" {
		t.Errorf("scope_c model = %q, want opus", items[2].Model)
	}
}

func TestStoreListEmpty(t *testing.T) {
	s := New(t.TempDir())
	items := s.List()
	if len(items) != 0 {
		t.Errorf("List on empty store = %d, want 0", len(items))
	}
}
