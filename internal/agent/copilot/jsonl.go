// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package copilot

import (
	"encoding/json"
	"fmt"

	"github.com/cognition/lark-acp-bridge/internal/agent"
)

// jsonlTranslator converts `copilot -p --output-format json` NDJSON events
// into the bridge's agent.Event stream. Copilot emits one JSON object per
// line with a `type` discriminator and a `data` payload; the terminal
// `result` event is the exception, carrying sessionId/exitCode/usage at the
// top level. The translator is stateful: the session id arrives in
// `session.start` and must be attached to the terminal event, and streamed
// `assistant.message_delta` chunks are tracked per messageId so the final
// `assistant.message` only emits the suffix that was not already streamed.
type jsonlTranslator struct {
	sessionID   string
	terminal    bool
	lastNonTerm string
	// deltaLens maps messageId -> number of content chars already emitted
	// via assistant.message_delta, so assistant.message does not re-emit.
	deltaLens map[string]int
}

func newTranslator() *jsonlTranslator {
	return &jsonlTranslator{deltaLens: map[string]int{}}
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
	data, _ := raw["data"].(map[string]any)
	switch typ {
	case "session.start":
		return t.translateSessionStart(data)
	case "assistant.message_delta":
		return t.translateMessageDelta(data)
	case "assistant.reasoning_delta":
		return t.translateReasoningDelta(data)
	case "assistant.message":
		return t.translateAssistantMessage(data)
	case "assistant.usage":
		return t.translateUsage(data)
	case "tool.execution_start":
		return t.translateToolStart(data)
	case "tool.execution_complete":
		return t.translateToolComplete(data)
	case "session.error":
		return t.translateTerminalError(data, "copilot session error")
	case "session.warning", "session.info":
		t.translateNonTerminalError(data)
		return nil
	case "abort":
		t.terminal = true
		return []agent.Event{{Type: agent.EventDone, SessionID: t.sessionID, StopReason: agent.StopCancelled}}
	case "result":
		return t.translateResult(raw)
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
		return []agent.Event{{Type: agent.EventDone, SessionID: t.sessionID, StopReason: agent.StopCancelled}}
	}
	detail := ""
	if t.lastNonTerm != "" {
		detail = ": " + t.lastNonTerm
	}
	return []agent.Event{{Type: agent.EventError, SessionID: t.sessionID, Err: fmt.Errorf("copilot stream ended before a terminal event%s", detail)}}
}

func (t *jsonlTranslator) translateSessionStart(data map[string]any) []agent.Event {
	if id := strVal(data["sessionId"]); id != "" {
		t.sessionID = id
	}
	return nil
}

func (t *jsonlTranslator) translateMessageDelta(data map[string]any) []agent.Event {
	delta := strVal(data["deltaContent"])
	if delta == "" {
		return nil
	}
	if id := strVal(data["messageId"]); id != "" {
		t.deltaLens[id] += len(delta)
	}
	return []agent.Event{{Type: agent.EventText, Delta: delta}}
}

func (t *jsonlTranslator) translateReasoningDelta(data map[string]any) []agent.Event {
	delta := strVal(data["deltaContent"])
	if delta == "" {
		return nil
	}
	return []agent.Event{{Type: agent.EventThinking, Delta: delta}}
}

func (t *jsonlTranslator) translateAssistantMessage(data map[string]any) []agent.Event {
	content := strVal(data["content"])
	id := strVal(data["messageId"])
	streamed := t.deltaLens[id]
	delete(t.deltaLens, id)
	var events []agent.Event
	// Emit only what the deltas did not already cover. When streaming is off
	// no deltas arrived, so the whole content is new.
	if content != "" && streamed < len(content) {
		events = append(events, agent.Event{Type: agent.EventText, Delta: content[streamed:]})
	}
	// assistant.usage is not always emitted (e.g. prompt mode); the message's
	// own outputTokens keeps the usage display populated in that case. When
	// both fire, the later assistant.usage event overwrites on the card.
	if out, ok := numVal(data["outputTokens"]); ok && out != 0 {
		events = append(events, agent.Event{Type: agent.EventUsage, OutputTokens: out})
	}
	return events
}

func (t *jsonlTranslator) translateUsage(data map[string]any) []agent.Event {
	in, _ := numVal(data["inputTokens"])
	out, _ := numVal(data["outputTokens"])
	cached, _ := numVal(data["cacheReadTokens"])
	if in == 0 && out == 0 && cached == 0 {
		return nil
	}
	return []agent.Event{{Type: agent.EventUsage, InputTokens: in + cached, OutputTokens: out}}
}

func (t *jsonlTranslator) translateToolStart(data map[string]any) []agent.Event {
	id := strVal(data["toolCallId"])
	name := strVal(data["toolName"])
	if id == "" || name == "" {
		return nil
	}
	if server := strVal(data["mcpServerName"]); server != "" {
		name = server + "/" + name
	}
	return []agent.Event{{
		Type:      agent.EventToolUse,
		ToolID:    id,
		ToolName:  name,
		ToolInput: mustJSON(data["arguments"]),
	}}
}

func (t *jsonlTranslator) translateToolComplete(data map[string]any) []agent.Event {
	id := strVal(data["toolCallId"])
	if id == "" {
		return nil
	}
	success, _ := data["success"].(bool)
	output := ""
	if result, ok := data["result"].(map[string]any); ok {
		output = strVal(result["content"])
	}
	if !success {
		if e, ok := data["error"].(map[string]any); ok {
			output = strVal(e["message"])
		}
	}
	return []agent.Event{{
		Type:       agent.EventToolResult,
		ToolID:     id,
		ToolOutput: output,
		ToolError:  !success,
	}}
}

// translateResult handles the terminal `result` event, whose fields live at
// the top level (no data wrapper): sessionId, exitCode, usage.
func (t *jsonlTranslator) translateResult(raw map[string]any) []agent.Event {
	t.terminal = true
	if id := strVal(raw["sessionId"]); id != "" {
		t.sessionID = id
	}
	exitCode, hasExit := numVal(raw["exitCode"])
	if hasExit && exitCode != 0 {
		return []agent.Event{{Type: agent.EventError, SessionID: t.sessionID, Err: fmt.Errorf("copilot exited with code %d", exitCode)}}
	}
	return []agent.Event{{Type: agent.EventDone, SessionID: t.sessionID, StopReason: agent.StopEndTurn}}
}

func (t *jsonlTranslator) translateTerminalError(data map[string]any, fallback string) []agent.Event {
	t.terminal = true
	return []agent.Event{{Type: agent.EventError, SessionID: t.sessionID, Err: fmt.Errorf("%s", errMsg(data, fallback))}}
}

func (t *jsonlTranslator) translateNonTerminalError(data map[string]any) {
	if msg := strVal(data["message"]); msg != "" {
		t.lastNonTerm = msg
	}
}

func errMsg(data map[string]any, fallback string) string {
	if m := strVal(data["message"]); m != "" {
		return m
	}
	if nested, ok := data["error"].(map[string]any); ok {
		if m := strVal(nested["message"]); m != "" {
			return m
		}
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
	if v == nil {
		return json.RawMessage("null")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}
