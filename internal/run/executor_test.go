package run

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
	"github.com/cognition/lark-acp-bridge/internal/card"
	larktypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
)

// fakeAdapter implements agent.AgentAdapter for testing. It returns a
// fakeRun that emits a scripted sequence of events.
type fakeAdapter struct {
	events []agent.Event
}

func (a *fakeAdapter) ID() string         { return "fake" }
func (a *fakeAdapter) DisplayName() string { return "Fake" }
func (a *fakeAdapter) Available(context.Context) error { return nil }
func (a *fakeAdapter) Run(_ context.Context, _ agent.RunOptions) (agent.Run, error) {
	return &fakeRun{events: a.events}, nil
}

// fakeRun implements agent.Run. It emits the scripted events then closes.
type fakeRun struct {
	events []agent.Event
}

func (r *fakeRun) Events() <-chan agent.Event {
	ch := make(chan agent.Event, len(r.events)+1)
	for _, ev := range r.events {
		ch <- ev
	}
	close(ch)
	return ch
}
func (r *fakeRun) Stop() error { return nil }
func (r *fakeRun) Wait() error { return nil }

// fakeSender implements Sender for testing. It returns a fakeStreamController.
type fakeSender struct {
	mu       sync.Mutex
	streams  []*fakeStreamController
}

func (f *fakeSender) StreamCard(_ context.Context, chatID string, _ card.Card) (larktypes.StreamController, string, error) {
	ctrl := &fakeStreamController{chatID: chatID}
	f.mu.Lock()
	f.streams = append(f.streams, ctrl)
	f.mu.Unlock()
	return ctrl, "msg_fake", nil
}

type fakeStreamController struct {
	chatID     string
	appends    []string
	cardUpdates []string
	closed     bool
	mu         sync.Mutex
}

func (c *fakeStreamController) Append(_ context.Context, text string) error {
	c.mu.Lock()
	c.appends = append(c.appends, text)
	c.mu.Unlock()
	return nil
}
func (c *fakeStreamController) UpdateCard(_ context.Context, cardJSON string) error {
	c.mu.Lock()
	c.cardUpdates = append(c.cardUpdates, cardJSON)
	c.mu.Unlock()
	return nil
}
func (c *fakeStreamController) Flush(context.Context) error { return nil }
func (c *fakeStreamController) Close(context.Context) error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

func TestExecuteStreamsEventsAndCallsOnDone(t *testing.T) {
	adapter := &fakeAdapter{
		events: []agent.Event{
			{Type: agent.EventText, Delta: "Hello "},
			{Type: agent.EventText, Delta: "world"},
			{Type: agent.EventDone, SessionID: "sess_123", StopReason: agent.StopEndTurn},
		},
	}
	sender := &fakeSender{}
	active := NewActiveRuns()
	executor := NewExecutor(sender, active)

	var doneScope, doneSession, doneReason string
	var doneMu sync.Mutex
	doneCalled := make(chan struct{})

	in := ExecuteInput{
		ChatID:  "chat_1",
		Scope:   "chat_1",
		Prompt:  "hi",
		Cwd:     "/tmp",
		Adapter: adapter,
		OnDone: func(scope, sid, reason string) {
			doneMu.Lock()
			doneScope, doneSession, doneReason = scope, sid, reason
			doneMu.Unlock()
			close(doneCalled)
		},
	}

	if err := executor.Execute(context.Background(), in); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	select {
	case <-doneCalled:
	case <-time.After(time.Second):
		t.Fatal("OnDone was not called")
	}

	doneMu.Lock()
	if doneScope != "chat_1" || doneSession != "sess_123" || doneReason != agent.StopEndTurn {
		t.Errorf("OnDone = (%q %q %q), want (chat_1 sess_123 end_turn)", doneScope, doneSession, doneReason)
	}
	doneMu.Unlock()

	// Verify at least one card update was sent (the terminal flush).
	sender.mu.Lock()
	if len(sender.streams) != 1 {
		t.Fatalf("expected 1 stream, got %d", len(sender.streams))
	}
	ctrl := sender.streams[0]
	ctrl.mu.Lock()
	updates := len(ctrl.cardUpdates)
	closed := ctrl.closed
	ctrl.mu.Unlock()
	sender.mu.Unlock()

	if updates == 0 {
		t.Error("expected at least one card update (terminal flush)")
	}
	if !closed {
		t.Error("expected stream to be closed")
	}
}

func TestExecuteActiveRunTracking(t *testing.T) {
	adapter := &fakeAdapter{
		events: []agent.Event{
			{Type: agent.EventDone, SessionID: "s1", StopReason: agent.StopEndTurn},
		},
	}
	sender := &fakeSender{}
	active := NewActiveRuns()
	executor := NewExecutor(sender, active)

	// Start a run in a goroutine; it should be tracked while running.
	done := make(chan struct{})
	go func() {
		_ = executor.Execute(context.Background(), ExecuteInput{
			ChatID: "c1", Scope: "scope1", Prompt: "x", Cwd: "/tmp", Adapter: adapter,
		})
		close(done)
	}()

	// The run completes almost instantly; just verify it doesn't panic
	// and the active run is cleared after.
	<-done
	if h := active.Get("scope1"); h != nil {
		t.Error("active run should be cleared after Execute returns")
	}
}

func TestExecuteErrorEventCallsOnDone(t *testing.T) {
	adapter := &fakeAdapter{
		events: []agent.Event{
			{Type: agent.EventError, Err: errSimple{"boom"}},
		},
	}
	sender := &fakeSender{}
	active := NewActiveRuns()
	executor := NewExecutor(sender, active)

	called := make(chan struct{}, 1)
	var gotReason string
	in := ExecuteInput{
		ChatID: "c1", Scope: "s1", Prompt: "x", Cwd: "/tmp", Adapter: adapter,
		OnDone: func(_, _, reason string) {
			gotReason = reason
			called <- struct{}{}
		},
	}
	_ = executor.Execute(context.Background(), in)

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("OnDone not called for error event")
	}
	// On error, stopReason is empty.
	if gotReason != "" {
		t.Errorf("reason = %q, want empty for error event", gotReason)
	}
}

func TestExecuteCardUpdateContainsText(t *testing.T) {
	adapter := &fakeAdapter{
		events: []agent.Event{
			{Type: agent.EventText, Delta: "response text"},
			{Type: agent.EventDone, SessionID: "s1", StopReason: agent.StopEndTurn},
		},
	}
	sender := &fakeSender{}
	executor := NewExecutor(sender, NewActiveRuns())

	_ = executor.Execute(context.Background(), ExecuteInput{
		ChatID: "c1", Scope: "s1", Prompt: "x", Cwd: "/tmp", Adapter: adapter,
	})

	sender.mu.Lock()
	ctrl := sender.streams[0]
	ctrl.mu.Lock()
	updates := ctrl.cardUpdates
	ctrl.mu.Unlock()
	sender.mu.Unlock()

	if len(updates) == 0 {
		t.Fatal("no card updates sent")
	}
	// The last update should contain "response text" in the card JSON.
	var lastCard card.Card
	if err := json.Unmarshal([]byte(updates[len(updates)-1]), &lastCard); err != nil {
		t.Fatalf("unmarshal last card: %v", err)
	}
	// Find the text element. Agent text renders as a top-level markdown
	// element (Content field), not a div with lark_md text.
	found := false
	for _, el := range lastCard.Elements {
		if el.Tag == "markdown" && el.Content == "response text" {
			found = true
		}
		if el.Text != nil && el.Text.Content == "response text" {
			found = true
		}
	}
	if !found {
		t.Error("card text element does not contain 'response text'")
	}
}

type errSimple struct{ msg string }

func (e errSimple) Error() string { return e.msg }
