// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"testing"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
)

// stubAdapter is a minimal AgentAdapter for registry tests.
type stubAdapter struct {
	id     string
	name   string
	avail  bool
}

func (s *stubAdapter) ID() string                                { return s.id }
func (s *stubAdapter) DisplayName() string                       { return s.name }
func (s *stubAdapter) Available(context.Context) error           { return nil }
func (s *stubAdapter) Run(context.Context, agent.RunOptions) (agent.Run, error) {
	return nil, nil
}

func TestRegistryResolveDefault(t *testing.T) {
	dir := t.TempDir()
	sel := NewSelection(dir)
	r := NewRegistry("devin", sel)
	devin := &stubAdapter{id: "devin", name: "Devin", avail: true}
	codex := &stubAdapter{id: "codex", name: "Codex", avail: true}
	r.Register(devin)
	r.Register(codex)

	if r.Resolve("chat_1").ID() != "devin" {
		t.Fatal("default resolve should return devin")
	}
	if r.Current("chat_1") != "devin" {
		t.Fatal("Current should return default")
	}
}

func TestRegistryResolveSelectionOverride(t *testing.T) {
	dir := t.TempDir()
	sel := NewSelection(dir)
	r := NewRegistry("devin", sel)
	r.Register(&stubAdapter{id: "devin", name: "Devin"})
	r.Register(&stubAdapter{id: "codex", name: "Codex"})

	if err := r.Set("chat_1", "codex"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if r.Resolve("chat_1").ID() != "codex" {
		t.Fatal("Resolve should honor selection override")
	}
	if r.Current("chat_1") != "codex" {
		t.Fatal("Current should honor selection override")
	}
}

func TestRegistryResolveFallsBackWhenSelectionMissing(t *testing.T) {
	dir := t.TempDir()
	sel := NewSelection(dir)
	r := NewRegistry("devin", sel)
	r.Register(&stubAdapter{id: "devin", name: "Devin"})
	// Selection points at a provider that is not registered.
	if err := r.Set("chat_1", "claude"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if r.Resolve("chat_1").ID() != "devin" {
		t.Fatal("Resolve should fall back to default when selection is unregistered")
	}
	if r.Current("chat_1") != "devin" {
		t.Fatal("Current should fall back to default when selection is unregistered")
	}
}

func TestRegistryClearRevertsToDefault(t *testing.T) {
	dir := t.TempDir()
	sel := NewSelection(dir)
	r := NewRegistry("devin", sel)
	r.Register(&stubAdapter{id: "devin", name: "Devin"})
	r.Register(&stubAdapter{id: "codex", name: "Codex"})

	if err := r.Set("chat_1", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := r.Clear("chat_1"); err != nil {
		t.Fatal(err)
	}
	if r.Current("chat_1") != "devin" {
		t.Fatal("Clear should revert to default")
	}
}

func TestRegistryListIsSortedByID(t *testing.T) {
	dir := t.TempDir()
	sel := NewSelection(dir)
	r := NewRegistry("devin", sel)
	r.Register(&stubAdapter{id: "devin", name: "Devin"})
	r.Register(&stubAdapter{id: "codex", name: "Codex"})
	list := r.List()
	if len(list) != 2 || list[0].ID != "codex" || list[1].ID != "devin" {
		t.Fatalf("list = %+v, want sorted [codex, devin]", list)
	}
}

func TestSelectionPersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	sel := NewSelection(dir)
	if err := sel.Set("chat_1", "codex"); err != nil {
		t.Fatal(err)
	}
	sel2 := NewSelection(dir)
	if err := sel2.Load(); err != nil {
		t.Fatal(err)
	}
	if got := sel2.Get("chat_1"); got != "codex" {
		t.Fatalf("after reload, Get = %q, want codex", got)
	}
}

func TestSelectionClearRemovesEntry(t *testing.T) {
	dir := t.TempDir()
	sel := NewSelection(dir)
	if err := sel.Set("chat_1", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := sel.Clear("chat_1"); err != nil {
		t.Fatal(err)
	}
	if got := sel.Get("chat_1"); got != "" {
		t.Fatalf("Get after Clear = %q, want empty", got)
	}
}

// Ensure the Available probe path does not hang on a slow adapter.
func TestRegistryListProbesAvailability(t *testing.T) {
	dir := t.TempDir()
	sel := NewSelection(dir)
	r := NewRegistry("devin", sel)
	r.Register(&stubAdapter{id: "devin", name: "Devin"})
	done := make(chan struct{})
	go func() {
		_ = r.List()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("List hung on availability probe")
	}
}
