package card

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

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

	s.Reduce(agent.Event{Type: agent.EventThinking, Delta: "step 1"})
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

// TestRunStatePlanReplacedNotAppended asserts that consecutive plan events
// replace the plan snapshot instead of stacking copies (Devin emits the full
// plan on every update).
func TestRunStatePlanReplacedNotAppended(t *testing.T) {
	s := NewRunState()
	s.Reduce(agent.Event{Type: agent.EventThinking, Delta: "step A"})
	s.Reduce(agent.Event{Type: agent.EventThinking, Delta: "step B"})
	if s.planText != "step B" {
		t.Errorf("planText = %q, want %q (should replace not append)", s.planText, "step B")
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

func TestHelpCardContainsBothSections(t *testing.T) {
	c := HelpCard("Devin", []string{"`/model` — list models"})
	if c.Header.Title.Content != "Help" {
		t.Errorf("title = %q, want Help", c.Header.Title.Content)
	}
	// Find the markdown element and check it mentions both sections.
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
	// Both section headers should be present.
	if !contains(md, "Bridge commands") {
		t.Error("missing Bridge commands section")
	}
	if !contains(md, "Devin commands") {
		t.Error("missing Devin commands section")
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
