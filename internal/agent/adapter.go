// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package agent defines the provider-agnostic interface between the Feishu
// side of the bridge and the local coding agent subprocess. v1 has one
// implementation (DevinAdapter); v2 will add Claude and Codex without
// changing this interface.
package agent

import (
	"context"
	"encoding/json"
	"time"
)

// AgentAdapter is the seam between the Feishu layer and a local agent.
// Implementations wrap a CLI subprocess (e.g. `devin acp`) and expose a
// streaming Run API.
type AgentAdapter interface {
	// ID is the stable provider identifier, e.g. "devin".
	ID() string
	// DisplayName is the human-friendly provider name shown on cards.
	DisplayName() string
	// Available returns nil if the agent binary is installed and responsive.
	Available(ctx context.Context) error
	// Run starts one prompt turn. The returned Run streams events until
	// EventDone or EventError, after which the channel is closed.
	Run(ctx context.Context, opts RunOptions) (Run, error)
}

// RunOptions configures one prompt turn.
type RunOptions struct {
	Prompt    string
	Cwd       string // required
	Scope     string // scope key for client pooling (chatId or chatId:threadId)
	SessionID string // empty = start a new session (unused by devin adapter v1)
	Model     string // empty = agent default
	StopGrace time.Duration
}

// Run is a live prompt turn. Events is closed after a terminal event
// (EventDone or EventError).
type Run interface {
	Events() <-chan Event
	// Stop cancels the current turn. It sends session/cancel (or SIGTERM
	// fallback) and waits up to StopGrace for the process to exit.
	Stop() error
	// Wait blocks until the underlying subprocess exits and returns its
	// exit error (nil for clean exit).
	Wait() error
}

// EventType enumerates the stream events one run can emit.
type EventType int

const (
	EventText EventType = iota
	EventThinking
	EventToolUse
	EventToolResult
	EventUsage
	EventDone
	EventError
)

// Event is one streamed event from the agent. Only fields relevant to Type
// are populated.
type Event struct {
	Type EventType

	// EventText, EventThinking
	Delta string

	// EventToolUse, EventToolResult
	ToolID    string
	ToolName  string
	ToolInput json.RawMessage
	ToolOutput string
	ToolError  bool

	// EventUsage
	InputTokens  int
	OutputTokens int
	CostUSD      float64

	// EventDone, EventError
	SessionID  string
	StopReason string // end_turn, max_tokens, cancelled, ...
	Err        error
}

// StopReason values mirrored from ACP for convenience.
const (
	StopEndTurn         = "end_turn"
	StopMaxTokens       = "max_tokens"
	StopCancelled       = "cancelled"
	StopRefusal         = "refusal"
	StopMaxTurnRequests = "max_turn_requests"
)
