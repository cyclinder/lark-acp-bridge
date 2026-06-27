// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package intake collects incoming messages per scope and batches them into
// a single prompt after a debounce window. This handles the common pattern
// of a user sending several short messages in quick succession: instead of
// spawning one agent run per message, the bridge waits for a brief quiet
// period and then sends the concatenated batch.
//
// The batcher is safe for concurrent use. Each scope has its own timer and
// queue. When the debounce window elapses with no new messages, the
// registered handler is called once with the joined prompt.
package intake

import (
	"strings"
	"sync"
	"time"
)

// Handler is called when a scope's debounce window expires. It receives the
// scope and the concatenated prompt (messages joined by double newlines).
type Handler func(scope, prompt string)

// Batcher manages per-scope message queues with debounce.
type Batcher struct {
	debounce time.Duration
	handler  Handler

	mu      sync.Mutex
	scopes  map[string]*scopeState
}

type scopeState struct {
	messages []string
	timer    *time.Timer
}

// New creates a Batcher that waits debounce after the last message before
// flushing the batch to handler.
func New(debounce time.Duration, handler Handler) *Batcher {
	return &Batcher{
		debounce: debounce,
		handler:  handler,
		scopes:   map[string]*scopeState{},
	}
}

// Push adds a message to the scope's queue and resets the debounce timer.
// If a run is already active for this scope, the caller should decide
// whether to queue or reject; the batcher itself does not check.
func (b *Batcher) Push(scope, message string) {
	b.mu.Lock()
	st, ok := b.scopes[scope]
	if !ok {
		st = &scopeState{}
		b.scopes[scope] = st
	}
	st.messages = append(st.messages, message)
	// Reset timer.
	if st.timer != nil {
		st.timer.Stop()
	}
	st.timer = time.AfterFunc(b.debounce, func() {
		b.flush(scope)
	})
	b.mu.Unlock()
}

// flush joins the queued messages and invokes the handler. Called by the
// debounce timer.
func (b *Batcher) flush(scope string) {
	b.mu.Lock()
	st, ok := b.scopes[scope]
	if !ok {
		b.mu.Unlock()
		return
	}
	msgs := st.messages
	st.messages = nil
	st.timer = nil
	// Clean up empty state to avoid unbounded map growth.
	if len(msgs) == 0 {
		delete(b.scopes, scope)
		b.mu.Unlock()
		return
	}
	delete(b.scopes, scope)
	b.mu.Unlock()

	prompt := strings.Join(msgs, "\n\n")
	b.handler(scope, prompt)
}

// Cancel drains and discards any queued messages for a scope, stopping the
// timer. Used when a slash command like /new or /stop should preempt the
// pending batch.
func (b *Batcher) Cancel(scope string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.scopes[scope]
	if !ok {
		return
	}
	if st.timer != nil {
		st.timer.Stop()
	}
	delete(b.scopes, scope)
}

// Pending returns the number of queued messages for a scope (for /status).
func (b *Batcher) Pending(scope string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.scopes[scope]
	if !ok {
		return 0
	}
	return len(st.messages)
}

// HasActive reports whether a scope has messages waiting to be flushed.
func (b *Batcher) HasActive(scope string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.scopes[scope]
	return ok
}
