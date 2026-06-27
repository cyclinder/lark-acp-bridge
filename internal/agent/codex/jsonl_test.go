// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cognition/lark-acp-bridge/internal/agent"
)

func translateAll(t *testing.T, lines ...string) []agent.Event {
	t.Helper()
	tr := newTranslator()
	var out []agent.Event
	for _, l := range lines {
		evs := tr.translate([]byte(l))
		out = append(out, evs...)
	}
	return out
}

func mustJSONLine(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestTranslateThreadStartedCapturesThreadID(t *testing.T) {
	tr := newTranslator()
	tr.translate([]byte(mustJSONLine(t, map[string]any{
		"type":      "thread.started",
		"thread_id": "thr_123",
	})))
	if tr.threadID != "thr_123" {
		t.Fatalf("threadID = %q, want thr_123", tr.threadID)
	}
}

func TestTranslateAgentMessageText(t *testing.T) {
	evs := translateAll(t, mustJSONLine(t, map[string]any{
		"type":    "agent_message",
		"message": "hello world",
	}))
	if len(evs) != 1 || evs[0].Type != agent.EventText || evs[0].Delta != "hello world" {
		t.Fatalf("got %+v, want one EventText hello world", evs)
	}
}

func TestTranslateItemCompletedAgentMessage(t *testing.T) {
	evs := translateAll(t, mustJSONLine(t, map[string]any{
		"type": "item.completed",
		"item": map[string]any{"type": "agent_message", "text": "hi"},
	}))
	if len(evs) != 1 || evs[0].Delta != "hi" {
		t.Fatalf("got %+v, want EventText hi", evs)
	}
}

func TestTranslateCommandExecutionToolUseAndResult(t *testing.T) {
	started := mustJSONLine(t, map[string]any{
		"type": "item.started",
		"item": map[string]any{"type": "command_execution", "id": "c1", "command": "ls"},
	})
	completed := mustJSONLine(t, map[string]any{
		"type": "item.completed",
		"item": map[string]any{"type": "command_execution", "id": "c1", "exit_code": 0, "output": "file1\nfile2"},
	})
	evs := translateAll(t, started, completed)
	if len(evs) != 2 {
		t.Fatalf("got %d events, want 2", len(evs))
	}
	if evs[0].Type != agent.EventToolUse || evs[0].ToolID != "c1" || evs[0].ToolName != "command_execution" {
		t.Fatalf("tool_use event = %+v", evs[0])
	}
	if evs[1].Type != agent.EventToolResult || evs[1].ToolError {
		t.Fatalf("tool_result event = %+v", evs[1])
	}
}

func TestTranslateCommandExecutionNonZeroExitIsError(t *testing.T) {
	started := mustJSONLine(t, map[string]any{
		"type": "item.started",
		"item": map[string]any{"type": "command_execution", "id": "c2", "command": "false"},
	})
	completed := mustJSONLine(t, map[string]any{
		"type": "item.completed",
		"item": map[string]any{"type": "command_execution", "id": "c2", "exit_code": 1, "output": "boom"},
	})
	evs := translateAll(t, started, completed)
	if !evs[1].ToolError {
		t.Fatalf("expected ToolError=true, got %+v", evs[1])
	}
}

func TestTranslateTurnCompletedEmitsDoneWithThreadID(t *testing.T) {
	tr := newTranslator()
	tr.translate([]byte(mustJSONLine(t, map[string]any{"type": "thread.started", "thread_id": "thr_9"})))
	evs := tr.translate([]byte(mustJSONLine(t, map[string]any{
		"type": "turn.completed",
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
	})))
	// Expect a usage event then a done event carrying the thread id.
	if len(evs) != 2 {
		t.Fatalf("got %d events, want 2", len(evs))
	}
	if evs[0].Type != agent.EventUsage || evs[0].InputTokens != 10 {
		t.Fatalf("usage event = %+v", evs[0])
	}
	if evs[1].Type != agent.EventDone || evs[1].SessionID != "thr_9" || evs[1].StopReason != agent.StopEndTurn {
		t.Fatalf("done event = %+v", evs[1])
	}
	if !tr.terminal {
		t.Fatal("translator should be terminal after turn.completed")
	}
}

func TestTranslateTurnFailedIsTerminalError(t *testing.T) {
	tr := newTranslator()
	evs := tr.translate([]byte(mustJSONLine(t, map[string]any{
		"type":    "turn.failed",
		"message": "model overloaded",
	})))
	if len(evs) != 1 || evs[0].Type != agent.EventError {
		t.Fatalf("got %+v, want one EventError", evs)
	}
	if !strings.Contains(evs[0].Err.Error(), "model overloaded") {
		t.Fatalf("error = %v, want it to contain 'model overloaded'", evs[0].Err)
	}
}

func TestFinishAfterStreamEndsWithoutTerminal(t *testing.T) {
	tr := newTranslator()
	tr.translate([]byte(mustJSONLine(t, map[string]any{"type": "thread.started", "thread_id": "t1"})))
	evs := tr.finish("failed")
	if len(evs) != 1 || evs[0].Type != agent.EventError {
		t.Fatalf("got %+v, want one EventError", evs)
	}
}

func TestFinishCancelledEmitsDoneCancelled(t *testing.T) {
	tr := newTranslator()
	evs := tr.finish("cancelled")
	if len(evs) != 1 || evs[0].Type != agent.EventDone || evs[0].StopReason != agent.StopCancelled {
		t.Fatalf("got %+v, want EventDone cancelled", evs)
	}
}

func TestTranslateIgnoresUnknownEvent(t *testing.T) {
	evs := translateAll(t, mustJSONLine(t, map[string]any{"type": "some_future_event"}))
	if len(evs) != 0 {
		t.Fatalf("got %d events, want 0 for unknown event", len(evs))
	}
}

func TestTranslateMalformedJSONIsIgnored(t *testing.T) {
	evs := translateAll(t, "{not valid json")
	if len(evs) != 0 {
		t.Fatalf("got %d events, want 0 for malformed json", len(evs))
	}
}

func TestTranslateNonTerminalErrorDoesNotEmit(t *testing.T) {
	tr := newTranslator()
	evs := tr.translate([]byte(mustJSONLine(t, map[string]any{"type": "error", "message": "transient"})))
	if len(evs) != 0 {
		t.Fatalf("non-terminal error should emit nothing, got %d", len(evs))
	}
	// But finish should surface it as the detail.
	evs = tr.finish("failed")
	if len(evs) != 1 || evs[0].Type != agent.EventError {
		t.Fatalf("got %+v", evs)
	}
	if !strings.Contains(evs[0].Err.Error(), "transient") {
		t.Fatalf("error = %v, want transient detail", evs[0].Err)
	}
}

func TestBuildArgsFresh(t *testing.T) {
	args := buildArgs(agent.RunOptions{Cwd: "/repo", Prompt: "hi"}, "danger-full-access", "gpt-5")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "exec --json --sandbox danger-full-access") {
		t.Fatalf("missing exec/sandbox flags: %s", joined)
	}
	if !strings.Contains(joined, "-C /repo") {
		t.Fatalf("missing -C /repo: %s", joined)
	}
	if !strings.Contains(joined, "-m gpt-5") {
		t.Fatalf("missing -m gpt-5: %s", joined)
	}
	if !strings.HasSuffix(joined, " -") {
		t.Fatalf("should end with stdin arg '-': %s", joined)
	}
	if strings.Contains(joined, "resume") {
		t.Fatalf("fresh run should not include resume: %s", joined)
	}
}

func TestBuildArgsResume(t *testing.T) {
	args := buildArgs(agent.RunOptions{Cwd: "/repo", SessionID: "thr_1"}, "read-only", "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "resume thr_1") {
		t.Fatalf("missing resume thr_1: %s", joined)
	}
	if strings.Contains(joined, " -m ") {
		t.Fatalf("should not include -m when no model: %s", joined)
	}
}

func TestAdapterAvailableRejectsMissingBinary(t *testing.T) {
	a := New(WithBinary("/nonexistent/codex-binary-xyz"))
	err := a.Available(nil)
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}
