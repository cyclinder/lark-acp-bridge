// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package run orchestrates one agent prompt turn: it starts the adapter,
// pumps events into the run-card state machine, and pushes card updates to
// the Feishu channel via a StreamController. It also tracks active runs per
// scope so /stop can cancel them.
package run

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
	"github.com/cognition/lark-acp-bridge/internal/card"
	larktypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
)

// Sender abstracts the Feishu channel's ability to open a streaming card.
// The lark package implements it.
type Sender interface {
	StreamCard(ctx context.Context, chatID string, c card.Card) (larktypes.StreamController, string, error)
}

// ActiveRuns tracks the one live run per scope.
type ActiveRuns struct {
	mu   sync.Mutex
	runs map[string]*Handle
}

// Handle is a handle to one active run.
type Handle struct {
	run    agent.Run
	cancel context.CancelFunc
}

// NewActiveRuns creates an empty ActiveRuns.
func NewActiveRuns() *ActiveRuns { return &ActiveRuns{runs: map[string]*Handle{}} }

// Set records an active run for a scope. Returns the previous handle if one
// existed (caller may stop it).
func (a *ActiveRuns) Set(scope string, h *Handle) *Handle {
	a.mu.Lock()
	defer a.mu.Unlock()
	prev := a.runs[scope]
	a.runs[scope] = h
	return prev
}

// Get returns the active run handle for a scope, or nil.
func (a *ActiveRuns) Get(scope string) *Handle {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runs[scope]
}

// Clear removes the active run for a scope.
func (a *ActiveRuns) Clear(scope string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.runs, scope)
}

// Interrupt cancels and removes the active run for a scope, if any.
func (a *ActiveRuns) Interrupt(scope string) {
	a.mu.Lock()
	h := a.runs[scope]
	delete(a.runs, scope)
	a.mu.Unlock()
	if h != nil {
		_ = h.run.Stop()
		h.cancel()
	}
}

// Executor drives one run: adapter -> events -> card updates.
type Executor struct {
	sender Sender
	active *ActiveRuns
}

// NewExecutor creates an Executor that streams cards via sender and tracks
// active runs in active.
func NewExecutor(sender Sender, active *ActiveRuns) *Executor {
	return &Executor{sender: sender, active: active}
}

// ExecuteInput configures one run.
type ExecuteInput struct {
	ChatID    string
	Scope     string
	Prompt    string
	Cwd       string
	Model     string
	SessionID string // empty = start a new session
	Adapter   agent.AgentAdapter
	// OnDone is called once when the run reaches a terminal state, before
	// the streaming card is closed. It receives the session id (may differ
	// from the input when a new session was created) and the stop reason.
	// Optional; used by the caller to persist the session id.
	OnDone func(scope, sessionID, stopReason string)
}

// Execute runs one prompt turn to completion. It blocks until the run ends.
// It opens a streaming card, then updates it as events arrive (debounced),
// and closes the stream when the run terminates.
func (e *Executor) Execute(ctx context.Context, in ExecuteInput) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	agentRun, err := in.Adapter.Run(runCtx, agent.RunOptions{
		Prompt: in.Prompt, Cwd: in.Cwd, Scope: in.Scope,
		Model: in.Model, SessionID: in.SessionID,
	})
	if err != nil {
		return err
	}
	h := &Handle{run: agentRun, cancel: cancel}
	if prev := e.active.Set(in.Scope, h); prev != nil {
		_ = prev.run.Stop()
		prev.cancel()
	}
	defer e.active.Clear(in.Scope)

	// Open a streaming card with the initial running state.
	state := card.NewRunState()
	ctrl, _, err := e.sender.StreamCard(ctx, in.ChatID, state.Render())
	if err != nil {
		// If the stream open fails, still drain the run so the agent
		// process does not leak; events just go nowhere.
		drainAndNotify(agentRun, in)
		return err
	}
	defer func() { _ = ctrl.Close(ctx) }()

	// Pump events, debouncing card updates.
	var mu sync.Mutex
	lastUpdate := time.Now()
	flush := func(force bool) {
		mu.Lock()
		defer mu.Unlock()
		if !force && time.Since(lastUpdate) < 300*time.Millisecond {
			return
		}
		cardJSON, err := json.Marshal(state.Render())
		if err != nil {
			return
		}
		_ = ctrl.UpdateCard(ctx, string(cardJSON))
		lastUpdate = time.Now()
	}

	// Heartbeat: while the run is in progress, force a card refresh on a
	// fixed interval so the footer ("running 12s · last: ... (3s ago)")
	// keeps advancing even when the agent emits no ACP updates. The
	// ticker is stopped as soon as the run reaches a terminal state.
	heartbeatDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				flush(true)
			case <-heartbeatDone:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	var doneSessionID, doneStopReason string
	var notified bool
	for ev := range agentRun.Events() {
		state.Reduce(ev)
		if state.IsTerminal() {
			flush(true)
			doneSessionID = ev.SessionID
			doneStopReason = ev.StopReason
			notified = true
			break
		}
		flush(false)
	}
	close(heartbeatDone)
	if notified && in.OnDone != nil {
		in.OnDone(in.Scope, doneSessionID, doneStopReason)
	}
	return nil
}

// drainAndNotify consumes all events from a run without rendering cards,
// ensuring the agent process is not left hanging. If a terminal event is
// observed and in.OnDone is set, it is called with the session id.
func drainAndNotify(r agent.Run, in ExecuteInput) {
	for ev := range r.Events() {
		if ev.Type == agent.EventDone || ev.Type == agent.EventError {
			if in.OnDone != nil {
				in.OnDone(in.Scope, ev.SessionID, ev.StopReason)
			}
			return
		}
	}
}
