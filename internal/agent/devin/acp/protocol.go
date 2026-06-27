// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package acp implements a minimal Agent Client Protocol (ACP) client that
// speaks JSON-RPC 2.0 over stdio with a `devin acp` subprocess.
//
// Reference: https://agentclientprotocol.com/protocol/v1/
//
// This package only covers the subset of ACP used by lark-acp-bridge:
// initialize, session/new, session/resume, session/prompt, session/cancel,
// session/set_config_option, session/close, and the session/update
// notifications emitted during a prompt turn.
package acp

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion is the ACP major version this client negotiates.
const ProtocolVersion = 1

// JSONRPCMessage is the common envelope for every ACP frame. A frame is a
// Request when Method is set and ID is non-nil; a Response when ID is non-nil
// and Method is empty; a Notification when Method is set and ID is nil.
type JSONRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is the standard JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("acp rpc error %d: %s", e.Code, e.Message)
}

// --- initialize -----------------------------------------------------------

type InitializeParams struct {
	ProtocolVersion    int                 `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities  `json:"clientCapabilities"`
	ClientInfo         ImplementationInfo  `json:"clientInfo"`
}

type ClientCapabilities struct {
	Fs       *FSCapabilities `json:"fs,omitempty"`
	Terminal bool            `json:"terminal,omitempty"`
}

type FSCapabilities struct {
	ReadTextFile  bool `json:"readTextFile,omitempty"`
	WriteTextFile bool `json:"writeTextFile,omitempty"`
}

type ImplementationInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

type InitializeResult struct {
	ProtocolVersion    int                  `json:"protocolVersion"`
	AgentCapabilities  AgentCapabilities    `json:"agentCapabilities"`
	AgentInfo          ImplementationInfo   `json:"agentInfo,omitempty"`
	AuthMethods        []AuthMethod         `json:"authMethods,omitempty"`
	// Raw is the full initialize result for accessing vendor-specific
	// _meta fields without modelling them all.
	Raw json.RawMessage `json:"-"`
}

type AgentCapabilities struct {
	LoadSession         bool                      `json:"loadSession,omitempty"`
	PromptCapabilities  *PromptCapabilities       `json:"promptCapabilities,omitempty"`
	SessionCapabilities *SessionCapabilities      `json:"sessionCapabilities,omitempty"`
	McpCapabilities     *McpCapabilities          `json:"mcpCapabilities,omitempty"`
}

type PromptCapabilities struct {
	Image            bool `json:"image,omitempty"`
	Audio            bool `json:"audio,omitempty"`
	EmbeddedContext  bool `json:"embeddedContext,omitempty"`
}

// SessionCapabilities mirrors the ACP spec. The real devin acp (2026.8.18)
// advertises list and additionalDirectories but NOT resume or close.
// We check for nil before using any capability.
type SessionCapabilities struct {
	List           *struct{} `json:"list,omitempty"`
	Resume         *struct{} `json:"resume,omitempty"`
	Close          *struct{} `json:"close,omitempty"`
	AdditionalDirs *struct{} `json:"additionalDirectories,omitempty"`
}

type McpCapabilities struct {
	HTTP bool `json:"http,omitempty"`
	SSE  bool `json:"sse,omitempty"`
}

type AuthMethod struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MethodType  string `json:"methodType,omitempty"` // legacy field name
}

// --- session lifecycle ----------------------------------------------------

// SessionNewParams creates a new session. McpServers is required by the
// real devin acp (it rejects the request if the field is absent), so we
// do not use omitempty. Pass an empty slice when no MCP servers are needed.
type SessionNewParams struct {
	Cwd        string      `json:"cwd"`
	McpServers []MCPServer `json:"mcpServers"`
}

type SessionResumeParams struct {
	SessionID  string      `json:"sessionId"`
	Cwd        string      `json:"cwd"`
	McpServers []MCPServer `json:"mcpServers"`
}

type SessionCloseParams struct {
	SessionID string `json:"sessionId"`
}

type MCPServer struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     []MCPServerEnv    `json:"env,omitempty"`
}

type MCPServerEnv struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SessionNewResult is returned by session/new and (as a subset) session/resume.
type SessionNewResult struct {
	SessionID       string          `json:"sessionId"`
	ConfigOptions   []ConfigOption  `json:"configOptions,omitempty"`
}

// --- session/prompt -------------------------------------------------------

type SessionPromptParams struct {
	SessionID string          `json:"sessionId"`
	Prompt    []ContentBlock  `json:"prompt"`
}

type ContentBlock struct {
	Type     string          `json:"type"` // "text", "image", "resource"
	Text     string          `json:"text,omitempty"`
	Resource *ResourceBlock  `json:"resource,omitempty"`
}

type ResourceBlock struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

type SessionPromptResult struct {
	StopReason string `json:"stopReason"` // end_turn, max_tokens, cancelled, ...
}

// --- session/cancel -------------------------------------------------------

type SessionCancelParams struct {
	SessionID string `json:"sessionId"`
}

// --- session/set_config_option -------------------------------------------

type SetConfigOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}

type SetConfigOptionResult struct {
	ConfigOptions []ConfigOption `json:"configOptions"`
}

// --- session/update notification payload ---------------------------------

type SessionUpdateParams struct {
	SessionID string  `json:"sessionId"`
	Update    Update  `json:"update"`
}

// Update is the discriminated union carried by session/update. The
// SessionUpdate field selects the variant; only the fields relevant to the
// variant are populated.
//
// Note: ACP uses the "content" JSON key for both a single ContentBlock
// (agent_message_chunk) and an array of content blocks (tool_call_update).
// We capture it as RawMessage and decode per-variant in stream.go to avoid
// a duplicate struct tag.
type Update struct {
	SessionUpdate string          `json:"sessionUpdate"`
	MessageID     string          `json:"messageId,omitempty"`
	Content       json.RawMessage `json:"content,omitempty"`
	ToolCallID    string          `json:"toolCallId,omitempty"`
	Title         string          `json:"title,omitempty"`
	Kind          string          `json:"kind,omitempty"`
	Status        string          `json:"status,omitempty"`
	Entries       []PlanEntry     `json:"entries,omitempty"`
	Used          *int            `json:"used,omitempty"`
	Size          *int            `json:"size,omitempty"`
	Cost          *Cost           `json:"cost,omitempty"`
	ConfigOptions []ConfigOption  `json:"configOptions,omitempty"`
}

// Content is the single-block form used by agent_message_chunk.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// UpdateContent is the multi-block form used by tool_call_update.content.
type UpdateContent struct {
	Type    string   `json:"type"` // "text" or "content"
	Content *Content `json:"content,omitempty"`
	Text    string   `json:"text,omitempty"`
}

type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

type Cost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// --- config options -------------------------------------------------------

type ConfigOption struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Description  string             `json:"description,omitempty"`
	Category     string             `json:"category,omitempty"`
	Type         string             `json:"type"` // "select"
	CurrentValue string             `json:"currentValue"`
	Options      []ConfigOptionVal  `json:"options"`
}

type ConfigOptionVal struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// SessionUpdate variant names. Not exhaustive; unknown variants are passed
// through as raw events so the caller can decide.
const (
	UpdateAgentMessageChunk  = "agent_message_chunk"
	UpdateUserMessageChunk   = "user_message_chunk"
	UpdateToolCall           = "tool_call"
	UpdateToolCallUpdate     = "tool_call_update"
	UpdatePlan               = "plan"
	UpdateUsage              = "usage_update"
	UpdateConfigOption       = "config_option_update"
	UpdateSessionInfo        = "session_info_update"
)

// StopReason values.
const (
	StopEndTurn         = "end_turn"
	StopMaxTokens       = "max_tokens"
	StopMaxTurnRequests = "max_turn_requests"
	StopRefusal         = "refusal"
	StopCancelled       = "cancelled"
)
