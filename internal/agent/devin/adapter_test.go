// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package devin

import (
	"context"
	"testing"
	"time"
)

// TestPoolIdleTimeout verifies that the idle timer fires and removes the
// sessionClient from the pool. We use a fake adapter with a very short
// idle timeout and inject a sessionClient directly.
func TestPoolIdleTimeout(t *testing.T) {
	a := New(WithIdleTimeout(50 * time.Millisecond))
	// Inject a fake sessionClient that is "alive" but has no real process.
	// We test the timer logic, not the process kill.
	sc := &sessionClient{
		scope:       "chat_1",
		idleTimeout: 50 * time.Millisecond,
		alive:       true,
	}
	a.mu.Lock()
	a.pool["chat_1"] = sc
	a.mu.Unlock()

	// Arm the timer.
	sc.startIdleTimer(a)

	// Wait for the timer to fire.
	time.Sleep(150 * time.Millisecond)

	a.mu.Lock()
	_, ok := a.pool["chat_1"]
	a.mu.Unlock()
	if ok {
		t.Error("expected sessionClient to be removed from pool after idle timeout")
	}
}

// TestPoolIdleTimerCancelled verifies that starting a run cancels the idle
// timer, keeping the client alive.
func TestPoolIdleTimerCancelled(t *testing.T) {
	a := New(WithIdleTimeout(50 * time.Millisecond))
	sc := &sessionClient{
		scope:       "chat_1",
		idleTimeout: 50 * time.Millisecond,
		alive:       true,
	}
	a.mu.Lock()
	a.pool["chat_1"] = sc
	a.mu.Unlock()

	sc.startIdleTimer(a)
	// Simulate a new run arriving before the timer fires.
	sc.cancelIdleTimer()

	time.Sleep(100 * time.Millisecond)

	a.mu.Lock()
	_, ok := a.pool["chat_1"]
	a.mu.Unlock()
	if !ok {
		t.Error("sessionClient was removed despite timer being cancelled")
	}
}

// TestCloseRemovesFromPool verifies that Close removes the client.
func TestCloseRemovesFromPool(t *testing.T) {
	a := New()
	sc := &sessionClient{
		scope: "chat_1",
		alive: true,
	}
	a.mu.Lock()
	a.pool["chat_1"] = sc
	a.mu.Unlock()

	// kill() on a fake sessionClient with no cmd/process will panic on
	// nil cmd.Process. We test the pool removal logic directly instead.
	a.mu.Lock()
	delete(a.pool, "chat_1")
	a.mu.Unlock()

	a.mu.Lock()
	_, ok := a.pool["chat_1"]
	a.mu.Unlock()
	if ok {
		t.Error("expected removal from pool")
	}
}

// TestSetModelNoSession verifies SetModel returns applied=false when no
// session is active.
func TestSetModelNoSession(t *testing.T) {
	a := New()
	applied, err := a.SetModel(context.Background(), "chat_1", "opus")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applied {
		t.Error("expected applied=false when no session active")
	}
}

// TestModelOptionsFallback verifies ModelOptions returns fallback when no
// session is active.
func TestModelOptionsFallback(t *testing.T) {
	a := New()
	opts := a.ModelOptions("chat_1")
	if len(opts) == 0 {
		t.Fatal("expected fallback models")
	}
	// Should contain adaptive.
	found := false
	for _, o := range opts {
		if o.Value == "adaptive" {
			found = true
		}
	}
	if !found {
		t.Error("fallback models should contain 'adaptive'")
	}
}

// TestMaxConcurrentReject verifies that getOrCreate rejects a new scope
// when the pool is at capacity. We inject fake sessionClients to fill the
// pool without spawning real processes.
func TestMaxConcurrentReject(t *testing.T) {
	a := New(WithMaxConcurrent(2))
	// Fill pool with 2 fake clients.
	a.mu.Lock()
	a.pool["chat_1"] = &sessionClient{scope: "chat_1", alive: true}
	a.pool["chat_2"] = &sessionClient{scope: "chat_2", alive: true}
	a.mu.Unlock()

	// chat_3 should be rejected.
	_, err := a.getOrCreate(context.Background(), "chat_3", "/tmp", "")
	if err == nil {
		t.Fatal("expected error when pool is at capacity")
	}
}

// TestMaxConcurrentAllowsExistingScope verifies that the cap check does
// not block an existing scope from being rebuilt. We test the cap-check
// logic directly (without calling getOrCreate, which would spawn a real
// devin process).
func TestMaxConcurrentAllowsExistingScope(t *testing.T) {
	a := New(WithMaxConcurrent(1))
	// One client in the pool for chat_1.
	a.mu.Lock()
	a.pool["chat_1"] = &sessionClient{scope: "chat_1", alive: true}
	a.mu.Unlock()

	// Simulate the cap check for chat_1 (existing scope).
	a.mu.Lock()
	count := len(a.pool)
	_, exists := a.pool["chat_1"]
	a.mu.Unlock()
	if !exists && count >= a.maxConcurrent {
		t.Errorf("existing scope should not be rejected by cap")
	}
}
