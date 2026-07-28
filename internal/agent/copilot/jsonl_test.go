// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package copilot

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

func TestTranslateSessionStartCapturesSessionID(t *testing.T) {
	tr := newTranslator()
	tr.translate([]byte(mustJSONLine(t, map[string]any{
		"type": "session.start",
		"data": map[string]any{"sessionId": "sess_123"},
	})))
	if tr.sessionID != "sess_123" {
		t.Fatalf("sessionID = %q, want sess_123", tr.sessionID)
	}
}

func TestTranslateMessageDeltaStreamsText(t *testing.T) {
	evs := translateAll(t, mustJSONLine(t, map[string]any{
		"type": "assistant.message_delta",
		"data": map[string]any{"messageId": "m1", "deltaContent": "hello "},
	}))
	if len(evs) != 1 || evs[0].Type != agent.EventText || evs[0].Delta != "hello " {
		t.Fatalf("got %+v, want one EventText 'hello '", evs)
	}
}

func TestTranslateReasoningDeltaStreamsThinking(t *testing.T) {
	evs := translateAll(t, mustJSONLine(t, map[string]any{
		"type": "assistant.reasoning_delta",
		"data": map[string]any{"deltaContent": "hmm"},
	}))
	if len(evs) != 1 || evs[0].Type != agent.EventThinking || evs[0].Delta != "hmm" {
		t.Fatalf("got %+v, want one EventThinking 'hmm'", evs)
	}
}

func TestTranslateAssistantMessageWithoutStreamingEmitsFullContent(t *testing.T) {
	evs := translateAll(t, mustJSONLine(t, map[string]any{
		"type": "assistant.message",
		"data": map[string]any{"messageId": "m1", "content": "full answer"},
	}))
	if len(evs) != 1 || evs[0].Type != agent.EventText || evs[0].Delta != "full answer" {
		t.Fatalf("got %+v, want one EventText 'full answer'", evs)
	}
}

func TestTranslateAssistantMessageAfterDeltasEmitsOnlySuffix(t *testing.T) {
	delta1 := mustJSONLine(t, map[string]any{
		"type": "assistant.message_delta",
		"data": map[string]any{"messageId": "m1", "deltaContent": "full "},
	})
	delta2 := mustJSONLine(t, map[string]any{
		"type": "assistant.message_delta",
		"data": map[string]any{"messageId": "m1", "deltaContent": "ans"},
	})
	message := mustJSONLine(t, map[string]any{
		"type": "assistant.message",
		"data": map[string]any{"messageId": "m1", "content": "full answer"},
	})
	evs := translateAll(t, delta1, delta2, message)
	if len(evs) != 3 {
		t.Fatalf("got %d events, want 3 (2 deltas + suffix)", len(evs))
	}
	if evs[2].Delta != "wer" {
		t.Fatalf("suffix = %q, want 'wer'", evs[2].Delta)
	}
}

func TestTranslateAssistantMessageFullyStreamedEmitsNothing(t *testing.T) {
	delta := mustJSONLine(t, map[string]any{
		"type": "assistant.message_delta",
		"data": map[string]any{"messageId": "m1", "deltaContent": "done"},
	})
	message := mustJSONLine(t, map[string]any{
		"type": "assistant.message",
		"data": map[string]any{"messageId": "m1", "content": "done"},
	})
	evs := translateAll(t, delta, message)
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1 (delta only)", len(evs))
	}
}

func TestTranslateAssistantMessageOutputTokensEmitUsage(t *testing.T) {
	// Prompt mode does not always emit assistant.usage; the message's own
	// outputTokens must still surface on the card.
	evs := translateAll(t, mustJSONLine(t, map[string]any{
		"type": "assistant.message",
		"data": map[string]any{"messageId": "m1", "content": "ok", "outputTokens": 17},
	}))
	if len(evs) != 2 || evs[0].Type != agent.EventText || evs[1].Type != agent.EventUsage || evs[1].OutputTokens != 17 {
		t.Fatalf("got %+v, want EventText + EventUsage(out=17)", evs)
	}
}

func TestTranslateUsageEvent(t *testing.T) {
	evs := translateAll(t, mustJSONLine(t, map[string]any{
		"type": "assistant.usage",
		"data": map[string]any{"inputTokens": 100, "outputTokens": 20, "cacheReadTokens": 40},
	}))
	if len(evs) != 1 || evs[0].Type != agent.EventUsage {
		t.Fatalf("got %+v, want one EventUsage", evs)
	}
	if evs[0].InputTokens != 140 || evs[0].OutputTokens != 20 {
		t.Fatalf("usage = %+v, want in=140 (with cache) out=20", evs[0])
	}
}

func TestTranslateToolExecutionStartAndComplete(t *testing.T) {
	started := mustJSONLine(t, map[string]any{
		"type": "tool.execution_start",
		"data": map[string]any{"toolCallId": "tc1", "toolName": "shell", "arguments": map[string]any{"command": "ls"}},
	})
	completed := mustJSONLine(t, map[string]any{
		"type": "tool.execution_complete",
		"data": map[string]any{"toolCallId": "tc1", "success": true, "result": map[string]any{"content": "file1\nfile2"}},
	})
	evs := translateAll(t, started, completed)
	if len(evs) != 2 {
		t.Fatalf("got %d events, want 2", len(evs))
	}
	if evs[0].Type != agent.EventToolUse || evs[0].ToolID != "tc1" || evs[0].ToolName != "shell" {
		t.Fatalf("tool_use event = %+v", evs[0])
	}
	if !strings.Contains(string(evs[0].ToolInput), `"command":"ls"`) {
		t.Fatalf("tool input = %s, want command arg", evs[0].ToolInput)
	}
	if evs[1].Type != agent.EventToolResult || evs[1].ToolError || evs[1].ToolOutput != "file1\nfile2" {
		t.Fatalf("tool_result event = %+v", evs[1])
	}
}

func TestTranslateToolExecutionFailed(t *testing.T) {
	evs := translateAll(t, mustJSONLine(t, map[string]any{
		"type": "tool.execution_complete",
		"data": map[string]any{"toolCallId": "tc2", "success": false, "error": map[string]any{"message": "permission denied"}},
	}))
	if len(evs) != 1 || !evs[0].ToolError || evs[0].ToolOutput != "permission denied" {
		t.Fatalf("got %+v, want ToolError with error message", evs)
	}
}

func TestTranslateMcpToolNamePrefixedWithServer(t *testing.T) {
	evs := translateAll(t, mustJSONLine(t, map[string]any{
		"type": "tool.execution_start",
		"data": map[string]any{"toolCallId": "tc3", "toolName": "search", "mcpServerName": "github"},
	}))
	if len(evs) != 1 || evs[0].ToolName != "github/search" {
		t.Fatalf("got %+v, want tool name github/search", evs)
	}
}

func TestTranslateResultEmitsDoneWithSessionID(t *testing.T) {
	tr := newTranslator()
	tr.translate([]byte(mustJSONLine(t, map[string]any{
		"type": "session.start",
		"data": map[string]any{"sessionId": "sess_1"},
	})))
	evs := tr.translate([]byte(mustJSONLine(t, map[string]any{
		"type":      "result",
		"sessionId": "sess_1",
		"exitCode":  0,
		"usage":     map[string]any{"premiumRequests": 2},
	})))
	if len(evs) != 1 || evs[0].Type != agent.EventDone {
		t.Fatalf("got %+v, want one EventDone", evs)
	}
	if evs[0].SessionID != "sess_1" || evs[0].StopReason != agent.StopEndTurn {
		t.Fatalf("done event = %+v", evs[0])
	}
	if !tr.terminal {
		t.Fatal("translator should be terminal after result")
	}
}

func TestTranslateResultNonZeroExitIsError(t *testing.T) {
	tr := newTranslator()
	evs := tr.translate([]byte(mustJSONLine(t, map[string]any{
		"type":      "result",
		"sessionId": "sess_2",
		"exitCode":  1,
	})))
	if len(evs) != 1 || evs[0].Type != agent.EventError || evs[0].SessionID != "sess_2" {
		t.Fatalf("got %+v, want one EventError with session id", evs)
	}
}

func TestTranslateResultWithoutSessionStartStillCapturesID(t *testing.T) {
	// Resumed sessions may not emit session.start; the result event must
	// still carry the id forward for the next turn's --resume.
	evs := translateAll(t, mustJSONLine(t, map[string]any{
		"type":      "result",
		"sessionId": "sess_resumed",
		"exitCode":  0,
	}))
	if len(evs) != 1 || evs[0].SessionID != "sess_resumed" {
		t.Fatalf("got %+v, want EventDone with resumed session id", evs)
	}
}

func TestTranslateSessionErrorIsTerminal(t *testing.T) {
	tr := newTranslator()
	evs := tr.translate([]byte(mustJSONLine(t, map[string]any{
		"type": "session.error",
		"data": map[string]any{"message": "model overloaded"},
	})))
	if len(evs) != 1 || evs[0].Type != agent.EventError {
		t.Fatalf("got %+v, want one EventError", evs)
	}
	if !strings.Contains(evs[0].Err.Error(), "model overloaded") {
		t.Fatalf("error = %v, want it to contain 'model overloaded'", evs[0].Err)
	}
}

func TestTranslateAbortIsDoneCancelled(t *testing.T) {
	evs := translateAll(t, mustJSONLine(t, map[string]any{"type": "abort"}))
	if len(evs) != 1 || evs[0].Type != agent.EventDone || evs[0].StopReason != agent.StopCancelled {
		t.Fatalf("got %+v, want EventDone cancelled", evs)
	}
}

func TestFinishAfterStreamEndsWithoutTerminal(t *testing.T) {
	tr := newTranslator()
	tr.translate([]byte(mustJSONLine(t, map[string]any{
		"type": "session.start",
		"data": map[string]any{"sessionId": "s1"},
	})))
	evs := tr.finish("failed")
	if len(evs) != 1 || evs[0].Type != agent.EventError || evs[0].SessionID != "s1" {
		t.Fatalf("got %+v, want one EventError with session id", evs)
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
	evs := translateAll(t, mustJSONLine(t, map[string]any{"type": "session.task_complete", "data": map[string]any{"summary": "done"}}))
	if len(evs) != 0 {
		t.Fatalf("got %d events, want 0 for ignored event", len(evs))
	}
}

func TestTranslateMalformedJSONIsIgnored(t *testing.T) {
	evs := translateAll(t, "{not valid json")
	if len(evs) != 0 {
		t.Fatalf("got %d events, want 0 for malformed json", len(evs))
	}
}

func TestTranslateWarningDoesNotEmitButSurfacesInFinish(t *testing.T) {
	tr := newTranslator()
	evs := tr.translate([]byte(mustJSONLine(t, map[string]any{
		"type": "session.warning",
		"data": map[string]any{"message": "rate limited"},
	})))
	if len(evs) != 0 {
		t.Fatalf("warning should emit nothing, got %d", len(evs))
	}
	evs = tr.finish("failed")
	if len(evs) != 1 || evs[0].Type != agent.EventError {
		t.Fatalf("got %+v", evs)
	}
	if !strings.Contains(evs[0].Err.Error(), "rate limited") {
		t.Fatalf("error = %v, want warning detail", evs[0].Err)
	}
}

func TestBuildArgsFresh(t *testing.T) {
	args, err := buildArgs(agent.RunOptions{Cwd: "/repo", Prompt: "hi"}, "allow-all", "gpt-4.1")
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-p hi --output-format json") {
		t.Fatalf("missing prompt/output flags: %s", joined)
	}
	if !strings.Contains(joined, "--allow-all") {
		t.Fatalf("missing --allow-all: %s", joined)
	}
	if !strings.Contains(joined, "--model gpt-4.1") {
		t.Fatalf("missing --model: %s", joined)
	}
	if !strings.Contains(joined, "--no-ask-user") {
		t.Fatalf("missing --no-ask-user: %s", joined)
	}
	if strings.Contains(joined, "--resume") {
		t.Fatalf("fresh run should not include --resume: %s", joined)
	}
}

func TestBuildArgsResume(t *testing.T) {
	args, err := buildArgs(agent.RunOptions{Cwd: "/repo", Prompt: "hi", SessionID: "sess_1"}, "allow-all-tools", "")
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--resume sess_1") {
		t.Fatalf("missing --resume sess_1: %s", joined)
	}
	if strings.Contains(joined, "--allow-all ") || strings.HasSuffix(joined, "--allow-all") {
		t.Fatalf("allow-all-tools should not expand to --allow-all: %s", joined)
	}
	if strings.Contains(joined, "--model") {
		t.Fatalf("should not include --model when no model: %s", joined)
	}
}

func TestBuildArgsReadOnly(t *testing.T) {
	args, err := buildArgs(agent.RunOptions{Cwd: "/repo", Prompt: "hi"}, "read-only", "")
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, tool := range []string{"shell", "edit", "create"} {
		if !strings.Contains(joined, "--deny-tool "+tool) {
			t.Fatalf("missing --deny-tool %s: %s", tool, joined)
		}
	}
}

func TestBuildArgsRejectsUnknownPermissions(t *testing.T) {
	if _, err := buildArgs(agent.RunOptions{Cwd: "/repo", Prompt: "hi"}, "yolo-mode", ""); err == nil {
		t.Fatal("expected error for unknown permissions mode")
	}
}

func TestAdapterAvailableRejectsMissingBinary(t *testing.T) {
	a := New(WithBinary("/nonexistent/copilot-binary-xyz"))
	err := a.Available(nil)
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}
