// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package card

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
)

func TestRunStateTextAccumulation(t *testing.T) {
	s := NewRunState()
	s.Reduce(agent.Event{Type: agent.EventText, Delta: "Hello "})
	s.Reduce(agent.Event{Type: agent.EventText, Delta: "world"})
	c := s.Render()
	// The text region is a markdown element; find it.
	var text string
	for _, el := range c.Elements {
		if el.Tag == "markdown" {
			text = el.Content
			break
		}
	}
	if text != "Hello world" {
		t.Errorf("text = %q, want %q", text, "Hello world")
	}
	if s.IsTerminal() {
		t.Error("state should not be terminal after text only")
	}
}

func TestRunStateDoneTransition(t *testing.T) {
	s := NewRunState()
	s.Reduce(agent.Event{Type: agent.EventText, Delta: "hi"})
	s.Reduce(agent.Event{Type: agent.EventDone, SessionID: "sess_1", StopReason: agent.StopEndTurn})
	if !s.IsTerminal() {
		t.Fatal("expected terminal after EventDone")
	}
	if s.sessionID != "sess_1" {
		t.Errorf("sessionID = %q, want sess_1", s.sessionID)
	}
	if s.stopReason != agent.StopEndTurn {
		t.Errorf("stopReason = %q, want %q", s.stopReason, agent.StopEndTurn)
	}
}

func TestRunStateCancelledTransition(t *testing.T) {
	s := NewRunState()
	s.Reduce(agent.Event{Type: agent.EventDone, StopReason: agent.StopCancelled})
	if !s.IsTerminal() {
		t.Fatal("expected terminal after cancelled")
	}
	if s.status != statusCancelled {
		t.Error("expected statusCancelled")
	}
}

// TestRenderToolBody asserts the tool panel body surfaces the labeled input
// (Command/File/Pattern/URL/Query) plus the output/error block, for both
// Devin-style (command_execution) and Copilot/Codex-style (Bash/Read/...)
// tool names.
func TestRenderToolBody(t *testing.T) {
	cases := []struct {
		name   string
		tool   toolEntry
		wantIn string
	}{
		{
			name: "command_execution with output",
			tool: toolEntry{
				name:   "command_execution",
				input:  json.RawMessage(`{"command":"ls -la"}`),
				output: "file.txt",
				status: "done",
			},
			wantIn: "**Command**",
		},
		{
			name: "Bash error shows Error label",
			tool: toolEntry{
				name:   "Bash",
				input:  json.RawMessage(`{"command":"git status"}`),
				output: "fatal: not a repo",
				status: "failed",
			},
			wantIn: "**Error**",
		},
		{
			name: "Read shows File",
			tool: toolEntry{
				name:   "Read",
				input:  json.RawMessage(`{"file_path":"/a/b/c.go"}`),
				status: "done",
			},
			wantIn: "**File** `/a/b/c.go`",
		},
		{
			name: "Grep shows Pattern and Path",
			tool: toolEntry{
				name:   "Grep",
				input:  json.RawMessage(`{"pattern":"foo","path":"src"}`),
				status: "running",
			},
			wantIn: "**Pattern** `foo`",
		},
		{
			name: "running no output shows running marker",
			tool: toolEntry{
				name:   "Bash",
				input:  json.RawMessage(`{"command":"sleep 1"}`),
				status: "running",
			},
			wantIn: "_running..._",
		},
		{
			name: "terminal no input no output shows no-output marker",
			tool: toolEntry{name: "unknown", status: "done"},
			wantIn: "_no output_",
		},
	}
	for _, c := range cases {
		got := renderToolBody(c.tool)
		if !strings.Contains(got, c.wantIn) {
			t.Errorf("%s: renderToolBody = %q, want to contain %q", c.name, got, c.wantIn)
		}
	}
}

// TestRenderToolBodyOutputCap asserts a huge output is bounded so the panel
// cannot push the card past Feishu's per-element size limit.
func TestRenderToolBodyOutputCap(t *testing.T) {
	big := strings.Repeat("x", outputMax*4)
	got := renderToolBody(toolEntry{name: "Bash", output: big, status: "done"})
	if len(got) > bodyTotalMax+64 {
		t.Errorf("body not bounded: %d bytes", len(got))
	}
}

func TestRunStateToolTracking(t *testing.T) {
	s := NewRunState()
	s.Reduce(agent.Event{Type: agent.EventToolUse, ToolID: "t1", ToolName: "ls"})
	s.Reduce(agent.Event{Type: agent.EventToolResult, ToolID: "t1", ToolOutput: "file.txt"})
	tools := s.toolBlocks()
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(tools))
	}
	if tools[0].status != "done" {
		t.Errorf("tool status = %q, want done", tools[0].status)
	}
	if tools[0].output != "file.txt" {
		t.Errorf("tool output = %q, want file.txt", tools[0].output)
	}
}

// TestRunStateHeaderReflectsPhase asserts the card header tells the user what
// the agent is currently doing — the core fix for "no idea what it's doing".
func TestRunStateHeaderReflectsPhase(t *testing.T) {
	s := NewRunState()
	if got := s.Render().Header.Title.Content; !strings.Contains(got, "Thinking") {
		t.Errorf("thinking header = %q, want to contain Thinking", got)
	}

	s.Reduce(agent.Event{Type: agent.EventToolUse, ToolID: "t1", ToolName: "grep"})
	if got := s.Render().Header.Title.Content; !strings.Contains(got, "Running tool: grep") {
		t.Errorf("tool header = %q, want to contain Running tool: grep", got)
	}

	s.Reduce(agent.Event{Type: agent.EventText, Delta: "hi"})
	if got := s.Render().Header.Title.Content; !strings.Contains(got, "Writing response") {
		t.Errorf("writing header = %q, want to contain Writing response", got)
	}

	s.Reduce(agent.Event{Type: agent.EventThinking, Delta: "step 1", Snapshot: true})
	if got := s.Render().Header.Title.Content; !strings.Contains(got, "Planning") {
		t.Errorf("planning header = %q, want to contain Planning", got)
	}

	s.Reduce(agent.Event{Type: agent.EventDone, StopReason: agent.StopEndTurn})
	if got := s.Render().Header.Title.Content; !strings.Contains(got, "Done") {
		t.Errorf("done header = %q, want to contain Done", got)
	}
}

// TestRunStateCancelledFinalizesTools asserts that a cancelled run does not
// leave tools stuck on the running spinner, which previously made the final
// card look like the agent was still busy.
func TestRunStateCancelledFinalizesTools(t *testing.T) {
	s := NewRunState()
	s.Reduce(agent.Event{Type: agent.EventToolUse, ToolID: "t1", ToolName: "ls"})
	s.Reduce(agent.Event{Type: agent.EventDone, StopReason: agent.StopCancelled})
	tools := s.toolBlocks()
	if tools[0].status != "cancelled" {
		t.Errorf("tool status = %q, want cancelled", tools[0].status)
	}
	if got := s.Render().Header.Title.Content; !strings.Contains(got, "Cancelled") {
		t.Errorf("cancelled header = %q, want to contain Cancelled", got)
	}
}

// TestSummarizeToolInput asserts the tool region surfaces what the tool is
// operating on (e.g. which shell command), not just the tool name.
func TestSummarizeToolInput(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"command_execution", `{"command":"ls -la"}`, "ls -la"},
		{"Bash", `{"command":"git status"}`, "git status"},
		{"Read", `{"file_path":"/a/b/c.go"}`, "/a/b/c.go"},
		{"Grep", `{"pattern":"foo","path":"src"}`, "foo in src"},
		{"", `null`, ""},
		{"Bash", `{}`, ""},
	}
	for _, c := range cases {
		got := summarizeToolInput(c.name, json.RawMessage(c.input))
		if got != c.want {
			t.Errorf("summarizeToolInput(%q,%s) = %q, want %q", c.name, c.input, got, c.want)
		}
	}
}

// TestRunStateTextTailCap asserts that a very long agent response is bounded:
// the text region keeps the tail (most recent output) and prepends a marker
// so the card does not grow unbounded.
func TestRunStateTextTailCap(t *testing.T) {
	s := NewRunState()
	// Feed ~2x the cap in two chunks so we can verify the tail is kept.
	var big strings.Builder
	for i := 0; i < maxTextRunes*2; i++ {
		big.WriteByte('x')
	}
	s.Reduce(agent.Event{Type: agent.EventText, Delta: big.String()})
	s.Reduce(agent.Event{Type: agent.EventText, Delta: "TAIL"})
	got := s.lastTextBlock()
	if !strings.Contains(got, "TAIL") {
		t.Error("tail content was dropped; expected most recent text to be kept")
	}
	if !strings.Contains(got, "earlier content omitted") {
		t.Error("missing truncation marker")
	}
	if len([]rune(got)) > maxTextRunes+64 { // +64 for the marker line
		t.Errorf("text buffer not bounded: %d runes", len([]rune(got)))
	}
}

// TestRunStateToolCollapse asserts that when more than maxVisibleTools tools
// are emitted, only the most recent ones are listed and older ones are folded
// into a count line.
func TestRunStateToolCollapse(t *testing.T) {
	s := NewRunState()
	for i := 0; i < maxVisibleTools+3; i++ {
		s.Reduce(agent.Event{Type: agent.EventToolUse, ToolID: fmt.Sprintf("t%d", i), ToolName: "ls"})
	}
	c := s.Render()
	// Collect all text content (markdown + divs + panel headers) and check for the marker.
	var allMd string
	for _, el := range c.Elements {
		switch el.Tag {
		case "markdown":
			allMd += el.Content + "\n"
		case "div":
			if el.Text != nil {
				allMd += el.Text.Content + "\n"
			}
		case "collapsible_panel":
			if el.Header != nil {
				allMd += el.Header.Title.Content + "\n"
			}
		}
	}
	if !strings.Contains(allMd, "+ 3 earlier tool calls") {
		t.Errorf("expected collapse marker for 3 hidden tools, got: %s", allMd)
	}
}

// TestRunStatePlanReplacedNotAppended asserts that consecutive plan SNAPSHOT
// events (Devin ACP, which emits the full plan on every update) replace the
// plan text instead of stacking copies.
func TestRunStatePlanReplacedNotAppended(t *testing.T) {
	s := NewRunState()
	s.Reduce(agent.Event{Type: agent.EventThinking, Delta: "step A", Snapshot: true})
	s.Reduce(agent.Event{Type: agent.EventThinking, Delta: "step B", Snapshot: true})
	if s.planText != "step B" {
		t.Errorf("planText = %q, want %q (snapshot should replace not append)", s.planText, "step B")
	}
}

// TestRunStateReasoningDeltaAccumulated asserts that reasoning DELTA events
// (Copilot assistant.reasoning_delta) are appended, not replaced — the bug
// being that the old reducer treated every thinking event as a snapshot, so
// only the last chunk survived.
func TestRunStateReasoningDeltaAccumulated(t *testing.T) {
	s := NewRunState()
	s.Reduce(agent.Event{Type: agent.EventThinking, Delta: "alpha "})
	s.Reduce(agent.Event{Type: agent.EventThinking, Delta: "beta "})
	s.Reduce(agent.Event{Type: agent.EventThinking, Delta: "gamma"})
	if s.planText != "alpha beta gamma" {
		t.Errorf("planText = %q, want %q (delta should accumulate)", s.planText, "alpha beta gamma")
	}
}

// TestRunStateReasoningDeltaTailCap asserts a long reasoning delta stream is
// bounded, keeping the tail (most recent reasoning) with a truncation marker.
func TestRunStateReasoningDeltaTailCap(t *testing.T) {
	s := NewRunState()
	var big strings.Builder
	for i := 0; i < maxPlanRunes*2; i++ {
		big.WriteByte('y')
	}
	s.Reduce(agent.Event{Type: agent.EventThinking, Delta: big.String()})
	s.Reduce(agent.Event{Type: agent.EventThinking, Delta: "TAIL"})
	if !strings.Contains(s.planText, "TAIL") {
		t.Error("tail reasoning was dropped; expected most recent chunk kept")
	}
	if !s.planDropped {
		t.Error("expected planDropped=true after truncation")
	}
}

// TestRunStateToolsInterleavedWithText asserts that tool calls render inline
// in chronological order, so the agent's final text lands at the bottom.
func TestRunStateToolsInterleavedWithText(t *testing.T) {
	s := NewRunState()
	s.Reduce(agent.Event{Type: agent.EventText, Delta: "intro"})
	s.Reduce(agent.Event{Type: agent.EventToolUse, ToolID: "t1", ToolName: "ls"})
	s.Reduce(agent.Event{Type: agent.EventToolResult, ToolID: "t1", ToolOutput: "out"})
	s.Reduce(agent.Event{Type: agent.EventText, Delta: "conclusion"})
	c := s.Render()
	// Collect a label for each element in order: text blocks yield their
	// markdown content; collapsible_panel elements yield their header title.
	var contents []string
	for _, el := range c.Elements {
		switch el.Tag {
		case "markdown":
			contents = append(contents, el.Content)
		case "div":
			if el.Text != nil {
				contents = append(contents, el.Text.Content)
			}
		case "collapsible_panel":
			if el.Header != nil {
				contents = append(contents, el.Header.Title.Content)
			}
		}
	}
	// Find positions: intro should come before the tool block, conclusion after.
	introIdx, toolIdx, conclIdx := -1, -1, -1
	for i, ct := range contents {
		if strings.Contains(ct, "intro") {
			introIdx = i
		}
		if strings.Contains(ct, "ls") {
			toolIdx = i
		}
		if strings.Contains(ct, "conclusion") {
			conclIdx = i
		}
	}
	if introIdx < 0 || toolIdx < 0 || conclIdx < 0 {
		t.Fatalf("missing blocks: intro=%d tool=%d concl=%d in %v", introIdx, toolIdx, conclIdx, contents)
	}
	if !(introIdx < toolIdx && toolIdx < conclIdx) {
		t.Errorf("expected intro < tool < conclusion, got intro=%d tool=%d concl=%d", introIdx, toolIdx, conclIdx)
	}
}

func TestRunStateErrorTransition(t *testing.T) {
	s := NewRunState()
	s.Reduce(agent.Event{Type: agent.EventError, Err: errFoo})
	if !s.IsTerminal() {
		t.Fatal("expected terminal after error")
	}
	if s.status != statusError {
		t.Error("expected statusError")
	}
}

var errFoo = &simpleErr{"boom"}

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }

func TestModelsCardSequence(t *testing.T) {
	models := []ModelEntry{
		{Value: "opus", Name: "Opus"},
		{Value: "sonnet", Name: "Sonnet"},
	}
	c := ModelsCard(models, "sonnet")
	// Just verify it renders without error and has a header.
	if c.Header.Title.Content != "Model" {
		t.Errorf("title = %q, want Model", c.Header.Title.Content)
	}
}

func TestHelpCardContainsGroupedSections(t *testing.T) {
	c := HelpCard("Devin", []string{"`/model` — list models"})
	if c.Header.Title.Content != "Help" {
		t.Errorf("title = %q, want Help", c.Header.Title.Content)
	}
	// Find the markdown element and check it mentions all groups.
	var md string
	for _, el := range c.Elements {
		if el.Tag == "markdown" {
			md = el.Content
			break
		}
	}
	if md == "" {
		t.Fatal("no markdown element found")
	}
	// All group headers should be present, frequent first.
	for _, header := range []string{"Frequent", "Workspace", "Session & model"} {
		if !contains(md, header) {
			t.Errorf("missing %s section", header)
		}
	}
	if indexOf(md, "Frequent") > indexOf(md, "Workspace") {
		t.Error("Frequent group should come before Workspace")
	}
	// The provider's own commands are merged into the session group.
	if !contains(md, "`/model` — list models") {
		t.Error("missing agent command line")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestSessionsCard(t *testing.T) {
	ts := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	c := SessionsCard([]SessionRow{
		{Index: 1, SessionID: "s1", Title: "First", Cwd: "/a", UpdatedAt: ts, Current: true},
		{Index: 2, SessionID: "s2", Cwd: "", Locked: true},
	}, 2)
	content := c.Elements[0].Content
	for _, want := range []string{
		"**1.** **First**", "`s1`", "cwd `/a`", "<- current chat",
		"**2.** **(untitled)**", "cwd `(unset)`", "in use elsewhere", "unknown time",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("SessionsCard content missing %q:\n%s", want, content)
		}
	}
	// "unknown time" applies to row 2 (zero UpdatedAt); row 1 must render
	// a formatted timestamp instead.
	if !strings.Contains(content, ts.Local().Format("2006-01-02 15:04")) {
		t.Errorf("SessionsCard missing formatted timestamp:\n%s", content)
	}
	// total == len(rows): no truncation footer.
	if strings.Contains(content, "most recent of") {
		t.Errorf("unexpected truncation footer:\n%s", content)
	}

	empty := SessionsCard(nil, 0)
	if !strings.Contains(empty.Elements[0].Content, "no sessions") {
		t.Errorf("empty SessionsCard = %q", empty.Elements[0].Content)
	}
}

func TestSessionsCardTruncationFooter(t *testing.T) {
	rows := make([]SessionRow, MaxSessionRows)
	for i := range rows {
		rows[i] = SessionRow{Index: i + 1, SessionID: "s", Cwd: "/a"}
	}
	c := SessionsCard(rows, 299)
	content := c.Elements[0].Content
	if !strings.Contains(content, "Showing the 20 most recent of 299 sessions.") {
		t.Errorf("missing truncation footer:\n%s", content)
	}
}
