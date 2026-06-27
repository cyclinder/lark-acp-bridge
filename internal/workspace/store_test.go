// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveValidDir(t *testing.T) {
	dir := t.TempDir()
	got, err := Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want, _ := filepath.Abs(dir)
	if got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestResolveTildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	// ~ alone expands to home, which isTooBroad rejects. Verify the
	// expansion happens (error mentions the home path, not "~").
	_, err = Resolve("~")
	if err == nil {
		t.Fatal("expected error: home dir is too broad")
	}
	if !filepath.IsAbs(home) {
		t.Skip("home is not absolute")
	}
	// ~/subdir should resolve to an absolute path under home and succeed
	// when that subdir exists.
	sub := filepath.Join(home, "lark-acp-bridge-test-tilde")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sub)
	got, err := Resolve("~/lark-acp-bridge-test-tilde")
	if err != nil {
		t.Fatalf("Resolve ~/sub: %v", err)
	}
	if got != sub {
		t.Errorf("Resolve ~/sub = %q, want %q", got, sub)
	}
}

func TestResolveNonexistent(t *testing.T) {
	_, err := Resolve("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestResolveFileNotDir(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve(f)
	if err == nil {
		t.Fatal("expected error for file (not dir)")
	}
}

func TestResolveRejectsRoot(t *testing.T) {
	_, err := Resolve("/")
	if err == nil {
		t.Fatal("expected error for root /")
	}
}

func TestResolveFromRelativeAgainstBase(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveFrom("sub", base)
	if err != nil {
		t.Fatalf("ResolveFrom(sub, base): %v", err)
	}
	if got != sub {
		t.Errorf("ResolveFrom = %q, want %q", got, sub)
	}
}

func TestResolveFromRelativeNoBaseFallsBackToProcessCwd(t *testing.T) {
	// With no base, a relative path resolves against the process cwd.
	// We just verify it produces an error (the dir doesn't exist) and
	// the error message contains an absolute path (proving it was abs'd).
	_, err := ResolveFrom("nonexistent-relative-dir-xyz", "")
	if err == nil {
		t.Fatal("expected error for nonexistent relative path")
	}
	if !strings.HasPrefix(err.Error(), "path /") {
		t.Errorf("expected error to mention an absolute path, got %q", err.Error())
	}
}

func TestResolveFromAbsoluteIgnoresBase(t *testing.T) {
	dir := t.TempDir()
	// An absolute path should ignore the base entirely.
	got, err := ResolveFrom(dir, "/some/other/base")
	if err != nil {
		t.Fatalf("ResolveFrom(abs, base): %v", err)
	}
	if got != dir {
		t.Errorf("ResolveFrom = %q, want %q", got, dir)
	}
}

func TestResolveFromTildeIgnoresBase(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	sub := filepath.Join(home, "lark-acp-bridge-test-resolvefrom-tilde")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sub)
	// ~/subdir should resolve against home, not against base.
	got, err := ResolveFrom("~/lark-acp-bridge-test-resolvefrom-tilde", "/some/base")
	if err != nil {
		t.Fatalf("ResolveFrom(~/sub, base): %v", err)
	}
	if got != sub {
		t.Errorf("ResolveFrom = %q, want %q", got, sub)
	}
}

func TestStoreCwdFor(t *testing.T) {
	s := New(t.TempDir())
	if err := s.SetCwd("scope1", "/tmp"); err != nil {
		t.Fatal(err)
	}
	if got := s.CwdFor("scope1", "/fallback"); got != "/tmp" {
		t.Errorf("CwdFor = %q, want /tmp", got)
	}
	if got := s.CwdFor("unknown", "/fallback"); got != "/fallback" {
		t.Errorf("CwdFor unknown = %q, want /fallback", got)
	}
}

func TestStoreNamedAliases(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.SaveNamed("main", "/tmp"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveNamed("docs", "/var"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetNamed("main"); got != "/tmp" {
		t.Errorf("GetNamed(main) = %q, want /tmp", got)
	}
	if got := s.GetNamed("missing"); got != "" {
		t.Errorf("GetNamed(missing) = %q, want empty", got)
	}
	listed := s.ListNamed()
	if len(listed) != 2 {
		t.Fatalf("ListNamed = %d entries, want 2", len(listed))
	}
	// Sorted by name: docs, main.
	if listed[0].Name != "docs" || listed[1].Name != "main" {
		t.Errorf("ListNamed order = %s, %s; want docs, main", listed[0].Name, listed[1].Name)
	}
	if !s.RemoveNamed("main") {
		t.Error("RemoveNamed(main) = false, want true")
	}
	if s.RemoveNamed("main") {
		t.Error("RemoveNamed(main) second time = true, want false")
	}
	if got := s.GetNamed("main"); got != "" {
		t.Errorf("GetNamed(main) after remove = %q, want empty", got)
	}
}

func TestStoreNamedPersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s1 := New(dir)
	if err := s1.SaveNamed("alpha", "/tmp"); err != nil {
		t.Fatal(err)
	}
	if err := s1.SetCwd("chat_1", "/home/me/proj"); err != nil {
		t.Fatal(err)
	}
	// Reload from the same file.
	s2 := New(dir)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	if got := s2.GetNamed("alpha"); got != "/tmp" {
		t.Errorf("GetNamed after reload = %q, want /tmp", got)
	}
	if got := s2.CwdFor("chat_1", ""); got != "/home/me/proj" {
		t.Errorf("CwdFor after reload = %q, want /home/me/proj", got)
	}
}

func TestStoreLoadLegacyFlatMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspaces.json")
	// Write a legacy flat map (no "chats"/"named" keys).
	legacy := []byte(`{"oc_123": "/tmp", "oc_456": "/var"}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if got := s.CwdFor("oc_123", ""); got != "/tmp" {
		t.Errorf("legacy CwdFor(oc_123) = %q, want /tmp", got)
	}
	if got := s.CwdFor("oc_456", ""); got != "/var" {
		t.Errorf("legacy CwdFor(oc_456) = %q, want /var", got)
	}
	// Named table should be empty.
	if entries := s.ListNamed(); len(entries) != 0 {
		t.Errorf("legacy ListNamed = %d entries, want 0", len(entries))
	}
}
