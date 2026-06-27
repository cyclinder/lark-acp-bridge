// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"encoding/json"
	"fmt"

	"github.com/cognition/lark-acp-bridge/internal/agent"
)

// jsonlTranslator converts codex `exec --json` NDJSON events into the bridge's
// agent.Event stream. Codex emits one JSON object per line; the translator is
// stateful because the thread id arrives in `thread.started` and must be
// attached to the terminal `turn.completed`/`turn.failed` event so the run
// executor can persist it as the session id.
type jsonlTranslator struct {
	threadID      string
	terminal      bool
	startedItems  map[string]struct{}
	lastNonTerm   string
}

func newTranslator() *jsonlTranslator {
	return &jsonlTranslator{startedItems: map[string]struct{}{}}
}

// translate parses one NDJSON line and returns zero or more agent events.
// Unknown event types are ignored (protocol drift is tolerated).
func (t *jsonlTranslator) translate(line []byte) []agent.Event {
	if t.terminal {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil
	}
	typ, _ := raw["type"].(string)
	switch typ {
	case "thread.started":
		return t.translateThreadStarted(raw)
	case "turn.started":
		return nil
	case "item.started":
		return t.translateItemStarted(raw)
	case "item.completed":
		return t.translateItemCompleted(raw)
	case "agent_message":
		return t.translateAgentMessage(raw)
	case "turn.completed":
		return t.translateTurnCompleted(raw)
	case "turn.failed":
		return t.translateTerminalError(raw, "codex turn failed")
	case "error":
		return t.translateNonTerminalError(raw, "codex error")
	default:
		return nil
	}
}

// finish emits a terminal event when the stream ends without one.
func (t *jsonlTranslator) finish(reason string) []agent.Event {
	if t.terminal {
		return nil
	}
	t.terminal = true
	if reason == "" {
		reason = "failed"
	}
	if reason == "cancelled" {
		return []agent.Event{{Type: agent.EventDone, SessionID: t.threadID, StopReason: agent.StopCancelled}}
	}
	detail := ""
	if t.lastNonTerm != "" {
		detail = ": " + t.lastNonTerm
	}
	return []agent.Event{{Type: agent.EventError, SessionID: t.threadID, Err: fmt.Errorf("codex stream ended before a terminal event%s", detail)}}
}

func (t *jsonlTranslator) translateThreadStarted(raw map[string]any) []agent.Event {
	id := strVal(raw["thread_id"])
	if id == "" {
		id = strVal(raw["threadId"])
	}
	if id == "" {
		return nil
	}
	t.threadID = id
	return nil
}

func (t *jsonlTranslator) translateItemStarted(raw map[string]any) []agent.Event {
	item, ok := raw["item"].(map[string]any)
	if !ok {
		return nil
	}
	if strVal(item["type"]) != "command_execution" {
		return nil
	}
	id := strVal(item["id"])
	if id == "" {
		return nil
	}
	t.startedItems[id] = struct{}{}
	return []agent.Event{{
		Type:     agent.EventToolUse,
		ToolID:   id,
		ToolName: "command_execution",
		ToolInput: mustJSON(map[string]any{"command": strVal(item["command"])}),
	}}
}

func (t *jsonlTranslator) translateItemCompleted(raw map[string]any) []agent.Event {
	item, ok := raw["item"].(map[string]any)
	if !ok {
		return nil
	}
	switch strVal(item["type"]) {
	case "agent_message":
		if msg := strVal(item["text"]); msg != "" {
			return []agent.Event{{Type: agent.EventText, Delta: msg}}
		}
		if msg := strVal(item["message"]); msg != "" {
			return []agent.Event{{Type: agent.EventText, Delta: msg}}
		}
		return nil
	case "command_execution":
		id := strVal(item["id"])
		if id == "" {
			return nil
		}
		delete(t.startedItems, id)
		output := strVal(item["output"])
		if output == "" {
			output = strVal(item["aggregated_output"])
		}
		if output == "" {
			output = strVal(item["stdout"])
		}
		exitCode, hasExit := numVal(item["exit_code"])
		return []agent.Event{{
			Type:       agent.EventToolResult,
			ToolID:     id,
			ToolOutput: output,
			ToolError:  hasExit && exitCode != 0,
		}}
	}
	return nil
}

func (t *jsonlTranslator) translateAgentMessage(raw map[string]any) []agent.Event {
	msg := strVal(raw["message"])
	if msg == "" {
		msg = strVal(raw["text"])
	}
	if msg == "" {
		return nil
	}
	return []agent.Event{{Type: agent.EventText, Delta: msg}}
}

func (t *jsonlTranslator) translateTurnCompleted(raw map[string]any) []agent.Event {
	t.terminal = true
	var events []agent.Event
	if usage, ok := raw["usage"].(map[string]any); ok {
		in, _ := numVal(usage["input_tokens"])
		out, _ := numVal(usage["output_tokens"])
		if in != 0 || out != 0 {
			// Codex reports per-turn token usage; map onto the usage event.
			events = append(events, agent.Event{Type: agent.EventUsage, InputTokens: in, OutputTokens: out})
		}
	}
	events = append(events, agent.Event{Type: agent.EventDone, SessionID: t.threadID, StopReason: agent.StopEndTurn})
	return events
}

func (t *jsonlTranslator) translateTerminalError(raw map[string]any, fallback string) []agent.Event {
	t.terminal = true
	return []agent.Event{{Type: agent.EventError, SessionID: t.threadID, Err: fmt.Errorf("%s", errMsg(raw, fallback))}}
}

func (t *jsonlTranslator) translateNonTerminalError(raw map[string]any, fallback string) []agent.Event {
	t.lastNonTerm = errMsg(raw, fallback)
	return nil
}

func errMsg(raw map[string]any, fallback string) string {
	if m := strVal(raw["message"]); m != "" {
		return m
	}
	if nested, ok := raw["error"].(map[string]any); ok {
		if m := strVal(nested["message"]); m != "" {
			return m
		}
	}
	if e := strVal(raw["error"]); e != "" {
		return e
	}
	return fallback
}

func strVal(v any) string {
	s, _ := v.(string)
	return s
}

func numVal(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	return int(f), true
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}
