// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package intake

import (
	"sync"
	"testing"
	"time"
)

func TestBatcherDebounces(t *testing.T) {
	var mu sync.Mutex
	var got []string
	h := func(scope, prompt string) {
		mu.Lock()
		got = append(got, prompt)
		mu.Unlock()
	}
	b := New(50*time.Millisecond, h)

	b.Push("s1", "hello")
	b.Push("s1", "world")
	b.Push("s1", "again")

	// Wait for flush.
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected 1 batch, got %d (%v)", len(got), got)
	}
	if got[0] != "hello\n\nworld\n\nagain" {
		t.Errorf("batch = %q, want %q", got[0], "hello\n\nworld\n\nagain")
	}
}

func TestBatcherSeparateScopes(t *testing.T) {
	var got sync.Map
	h := func(scope, prompt string) {
		got.Store(scope, prompt)
	}
	b := New(30*time.Millisecond, h)

	b.Push("s1", "a")
	b.Push("s2", "b")

	time.Sleep(100 * time.Millisecond)

	if v, ok := got.Load("s1"); !ok || v != "a" {
		t.Errorf("s1 = %v, want a", v)
	}
	if v, ok := got.Load("s2"); !ok || v != "b" {
		t.Errorf("s2 = %v, want b", v)
	}
}

func TestBatcherCancel(t *testing.T) {
	flushed := make(chan string, 1)
	h := func(scope, prompt string) {
		flushed <- prompt
	}
	b := New(30*time.Millisecond, h)

	b.Push("s1", "should be cancelled")
	b.Cancel("s1")

	select {
	case msg := <-flushed:
		t.Errorf("unexpected flush: %q", msg)
	case <-time.After(80 * time.Millisecond):
		// Good: no flush happened.
	}
}

func TestBatcherPending(t *testing.T) {
	b := New(1*time.Second, func(string, string) {})
	b.Push("s1", "a")
	b.Push("s1", "b")
	if n := b.Pending("s1"); n != 2 {
		t.Errorf("Pending = %d, want 2", n)
	}
	if n := b.Pending("s2"); n != 0 {
		t.Errorf("Pending unknown = %d, want 0", n)
	}
	if !b.HasActive("s1") {
		t.Error("HasActive should be true for s1")
	}
	if b.HasActive("s2") {
		t.Error("HasActive should be false for s2")
	}
}

func TestBatcherResetsTimerOnPush(t *testing.T) {
	flushed := make(chan string, 1)
	h := func(scope, prompt string) {
		flushed <- prompt
	}
	b := New(50*time.Millisecond, h)

	b.Push("s1", "first")
	// Push again at 30ms, before the 50ms timer fires. This should reset
	// the timer so the flush happens at ~80ms, not ~50ms.
	time.Sleep(30 * time.Millisecond)
	b.Push("s1", "second")

	// At 60ms (from start), no flush should have happened yet because the
	// timer was reset at 30ms.
	select {
	case msg := <-flushed:
		t.Errorf("flushed too early: %q", msg)
	case <-time.After(40 * time.Millisecond):
	}

	// Now wait for the reset timer to fire.
	select {
	case msg := <-flushed:
		if msg != "first\n\nsecond" {
			t.Errorf("batch = %q, want %q", msg, "first\n\nsecond")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("no flush after reset timer")
	}
}
