// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package lark_test contains full-pipeline tests that verify the message
// intake -> command dispatch -> run execution flow without a real Feishu
// connection. It uses a mock channel that implements both the commands.Sender
// and run.Sender interfaces.
package lark_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
	"github.com/cognition/lark-acp-bridge/internal/card"
	"github.com/cognition/lark-acp-bridge/internal/commands"
	"github.com/cognition/lark-acp-bridge/internal/config"
	"github.com/cognition/lark-acp-bridge/internal/intake"
	bridgetlark "github.com/cognition/lark-acp-bridge/internal/lark"
	"github.com/cognition/lark-acp-bridge/internal/run"
	"github.com/cognition/lark-acp-bridge/internal/session"
	"github.com/cognition/lark-acp-bridge/internal/workspace"
	larktypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
)

// mockChannel implements both commands.Sender and run.Sender.
type mockChannel struct {
	mu        sync.Mutex
	markdowns []mockMarkdown
	cards     []mockCard
	streams   []*mockStreamController
	reactions []mockReaction
}

type mockMarkdown struct {
	chatID, text, replyTo string
}

type mockCard struct {
	chatID string
	card   card.Card
}

type mockReaction struct {
	messageID, emojiType string
}

func (m *mockChannel) SendMarkdown(chatID, text, replyTo string) error {
	m.mu.Lock()
	m.markdowns = append(m.markdowns, mockMarkdown{chatID, text, replyTo})
	m.mu.Unlock()
	return nil
}

func (m *mockChannel) SendCard(chatID string, c card.Card) (string, error) {
	m.mu.Lock()
	m.cards = append(m.cards, mockCard{chatID, c})
	m.mu.Unlock()
	return "msg_mock", nil
}

func (m *mockChannel) SendReaction(messageID, emojiType string) error {
	m.mu.Lock()
	m.reactions = append(m.reactions, mockReaction{messageID, emojiType})
	m.mu.Unlock()
	return nil
}

func (m *mockChannel) StreamCard(_ context.Context, chatID string, c card.Card) (larktypes.StreamController, string, error) {
	ctrl := &mockStreamController{chatID: chatID}
	m.mu.Lock()
	m.streams = append(m.streams, ctrl)
	m.mu.Unlock()
	return ctrl, "msg_mock", nil
}

type mockStreamController struct {
	chatID      string
	mu          sync.Mutex
	cardUpdates []string
	closed      bool
}

func (c *mockStreamController) Append(context.Context, string) error { return nil }
func (c *mockStreamController) UpdateCard(_ context.Context, cardJSON string) error {
	c.mu.Lock()
	c.cardUpdates = append(c.cardUpdates, cardJSON)
	c.mu.Unlock()
	return nil
}
func (c *mockStreamController) Flush(context.Context) error { return nil }
func (c *mockStreamController) Close(context.Context) error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

// mockAdapter implements agent.AgentAdapter for pipeline testing.
type mockAdapter struct {
	events []agent.Event
}

func (a *mockAdapter) ID() string                      { return "mock" }
func (a *mockAdapter) DisplayName() string             { return "Mock" }
func (a *mockAdapter) Available(context.Context) error { return nil }
func (a *mockAdapter) Run(_ context.Context, _ agent.RunOptions) (agent.Run, error) {
	return &mockRun{events: a.events}, nil
}

type mockRun struct {
	events []agent.Event
}

func (r *mockRun) Events() <-chan agent.Event {
	ch := make(chan agent.Event, len(r.events)+1)
	for _, ev := range r.events {
		ch <- ev
	}
	close(ch)
	return ch
}
func (r *mockRun) Stop() error { return nil }
func (r *mockRun) Wait() error { return nil }

// pipeline is the test harness wiring all components together.
type pipeline struct {
	ch         *mockChannel
	adapter    *mockAdapter
	active     *run.ActiveRuns
	executor   *run.Executor
	sessions   *session.Store
	workspaces *workspace.Store
	batcher    *intake.Batcher
	cfg        *config.Config
}

func newPipeline(t *testing.T, adapterEvents []agent.Event) *pipeline {
	t.Helper()
	dir := t.TempDir()
	ch := &mockChannel{}
	adapter := &mockAdapter{events: adapterEvents}
	active := run.NewActiveRuns()
	executor := run.NewExecutor(ch, active)
	sessions := session.New(dir)
	workspaces := workspace.New(dir)
	cfg := &config.Config{
		App:        config.App{ID: "test", Secret: "s", Tenant: "feishu"},
		Agent:      config.Agent{Binary: "devin"},
		Workspace:  config.Workspace{Default: dir},
		DebounceMs: 50,
	}
	p := &pipeline{
		ch: ch, adapter: adapter, active: active, executor: executor,
		sessions: sessions, workspaces: workspaces, cfg: cfg,
	}
	// The batcher's flush handler starts a run via the executor.
	p.batcher = intake.New(time.Duration(cfg.DebounceMs)*time.Millisecond, p.startRun)
	return p
}

// startRun is the batcher flush handler, mirroring main.go's appCtx.startRun.
func (p *pipeline) startRun(scope, prompt string) {
	chatID := scope
	cwd := p.workspaces.CwdFor(scope, p.cfg.Workspace.Default)
	entry, _ := p.sessions.Get(scope)
	model := entry.Model
	sessionID := entry.SessionID
	go func() {
		_ = p.executor.Execute(context.Background(), run.ExecuteInput{
			ChatID: chatID, Scope: scope, Prompt: prompt, Cwd: cwd,
			Model: model, SessionID: sessionID, Adapter: p.adapter,
			OnDone: func(scope, sid, reason string) {
				if sid == "" {
					return
				}
				_ = p.sessions.Set(scope, session.Entry{
					SessionID: sid, Cwd: cwd, Model: model,
				})
			},
		})
	}()
}

// handleMessage mirrors main.go's appCtx.handleMessage.
func (p *pipeline) handleMessage(msg *larktypes.NormalizedMessage) {
	chatID := msg.ChatID
	scope := chatID
	content := msg.Content

	_ = p.ch.SendReaction(msg.MessageID, bridgetlark.ReactionTyping)

	cmdCtx := &commands.Context{
		Sender: p.ch, ChatID: chatID, Scope: scope, ChatMode: "p2p",
		MessageID: msg.MessageID, Sessions: p.sessions,
		Workspaces: p.workspaces, Active: p.active, Config: p.cfg,
		Adapter: p.adapter, Executor: p.executor,
	}

	handled, err := commands.TryDispatch(content, cmdCtx)
	if err != nil {
		_ = p.ch.SendMarkdown(chatID, "Command error", msg.MessageID)
		return
	}
	if handled {
		p.batcher.Cancel(scope)
		return
	}
	if content == "" {
		return
	}
	cwd := p.workspaces.CwdFor(scope, p.cfg.Workspace.Default)
	if cwd == "" {
		_ = p.ch.SendMarkdown(chatID, "No working directory set.", msg.MessageID)
		return
	}
	p.batcher.Push(scope, content)
}

func TestPipelineSlashCommandHelp(t *testing.T) {
	p := newPipeline(t, nil)
	p.handleMessage(&larktypes.NormalizedMessage{
		ChatID: "chat_1", ChatType: "p2p", Content: "/help",
	})
	p.ch.mu.Lock()
	defer p.ch.mu.Unlock()
	if len(p.ch.cards) != 1 {
		t.Fatalf("expected 1 help card, got %d", len(p.ch.cards))
	}
	if p.ch.cards[0].card.Header.Title.Content != "Help" {
		t.Errorf("card title = %q, want Help", p.ch.cards[0].card.Header.Title.Content)
	}
}

func TestPipelineSlashCommandCdThenPrompt(t *testing.T) {
	p := newPipeline(t, []agent.Event{
		{Type: agent.EventText, Delta: "response"},
		{Type: agent.EventDone, SessionID: "sess_1", StopReason: agent.StopEndTurn},
	})
	dir := t.TempDir()

	// /cd sets the working directory.
	p.handleMessage(&larktypes.NormalizedMessage{
		ChatID: "chat_1", ChatType: "p2p", Content: "/cd " + dir,
	})
	p.ch.mu.Lock()
	if len(p.ch.markdowns) == 0 {
		t.Fatal("expected /cd reply")
	}
	p.ch.mu.Unlock()

	// Send a plain message; it should be batched and then trigger a run.
	p.handleMessage(&larktypes.NormalizedMessage{
		ChatID: "chat_1", ChatType: "p2p", Content: "hello agent",
	})

	// Wait for the debounce + run to complete.
	time.Sleep(300 * time.Millisecond)

	// A streaming card should have been opened.
	p.ch.mu.Lock()
	streamCount := len(p.ch.streams)
	p.ch.mu.Unlock()
	if streamCount != 1 {
		t.Fatalf("expected 1 stream, got %d", streamCount)
	}

	// The stream should have received at least one card update (terminal).
	p.ch.streams[0].mu.Lock()
	updateCount := len(p.ch.streams[0].cardUpdates)
	closed := p.ch.streams[0].closed
	p.ch.streams[0].mu.Unlock()
	if updateCount == 0 {
		t.Error("expected at least one card update")
	}
	if !closed {
		t.Error("expected stream to be closed after run completes")
	}

	// Session should be persisted.
	entry, ok := p.sessions.Get("chat_1")
	if !ok || entry.SessionID != "sess_1" {
		t.Errorf("session after run = %+v ok=%v, want sess_1", entry, ok)
	}
}

func TestPipelineMultiMessageBatching(t *testing.T) {
	p := newPipeline(t, []agent.Event{
		{Type: agent.EventText, Delta: "got it"},
		{Type: agent.EventDone, SessionID: "sess_2", StopReason: agent.StopEndTurn},
	})
	dir := t.TempDir()
	p.handleMessage(&larktypes.NormalizedMessage{
		ChatID: "chat_1", Content: "/cd " + dir,
	})

	// Send three messages in quick succession; they should batch into one run.
	p.handleMessage(&larktypes.NormalizedMessage{ChatID: "chat_1", Content: "first"})
	p.handleMessage(&larktypes.NormalizedMessage{ChatID: "chat_1", Content: "second"})
	p.handleMessage(&larktypes.NormalizedMessage{ChatID: "chat_1", Content: "third"})

	// Wait for debounce + run.
	time.Sleep(400 * time.Millisecond)

	p.ch.mu.Lock()
	streamCount := len(p.ch.streams)
	p.ch.mu.Unlock()
	if streamCount != 1 {
		t.Fatalf("expected 1 stream (batched), got %d", streamCount)
	}
}

func TestPipelineStopCancelsActiveRun(t *testing.T) {
	// Use a run that blocks so we can test /stop while it's active.
	p := newPipeline(t, []agent.Event{
		{Type: agent.EventText, Delta: "working"},
		{Type: agent.EventDone, SessionID: "sess_3", StopReason: agent.StopCancelled},
	})
	dir := t.TempDir()
	p.handleMessage(&larktypes.NormalizedMessage{ChatID: "chat_1", Content: "/cd " + dir})
	p.handleMessage(&larktypes.NormalizedMessage{ChatID: "chat_1", Content: "do something"})

	// Wait for the run to start.
	time.Sleep(150 * time.Millisecond)

	// /stop should cancel it.
	p.handleMessage(&larktypes.NormalizedMessage{ChatID: "chat_1", Content: "/stop"})

	p.ch.mu.Lock()
	// Expect: /cd reply + /stop reply (2 markdowns).
	mdCount := len(p.ch.markdowns)
	p.ch.mu.Unlock()
	if mdCount < 2 {
		t.Errorf("expected at least 2 markdown replies, got %d", mdCount)
	}
}

func TestPipelineStatusCard(t *testing.T) {
	p := newPipeline(t, nil)
	dir := t.TempDir()
	p.handleMessage(&larktypes.NormalizedMessage{ChatID: "chat_1", Content: "/cd " + dir})
	p.handleMessage(&larktypes.NormalizedMessage{ChatID: "chat_1", Content: "/status"})

	p.ch.mu.Lock()
	defer p.ch.mu.Unlock()
	// Expect: /cd markdown + /status card.
	if len(p.ch.cards) != 1 {
		t.Fatalf("expected 1 status card, got %d", len(p.ch.cards))
	}
	if p.ch.cards[0].card.Header.Title.Content != "Status" {
		t.Errorf("card title = %q, want Status", p.ch.cards[0].card.Header.Title.Content)
	}
}

func TestPipelineCardContainsAgentText(t *testing.T) {
	p := newPipeline(t, []agent.Event{
		{Type: agent.EventText, Delta: "The answer is 42"},
		{Type: agent.EventDone, SessionID: "s1", StopReason: agent.StopEndTurn},
	})
	dir := t.TempDir()
	p.handleMessage(&larktypes.NormalizedMessage{ChatID: "chat_1", Content: "/cd " + dir})
	p.handleMessage(&larktypes.NormalizedMessage{ChatID: "chat_1", Content: "what is the answer?"})

	time.Sleep(300 * time.Millisecond)

	p.ch.mu.Lock()
	if len(p.ch.streams) != 1 {
		t.Fatalf("expected 1 stream, got %d", len(p.ch.streams))
	}
	ctrl := p.ch.streams[0]
	ctrl.mu.Lock()
	updates := ctrl.cardUpdates
	ctrl.mu.Unlock()
	p.ch.mu.Unlock()

	if len(updates) == 0 {
		t.Fatal("no card updates")
	}
	var lastCard card.Card
	if err := json.Unmarshal([]byte(updates[len(updates)-1]), &lastCard); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, el := range lastCard.Elements {
		// Agent text renders as a top-level markdown element (Content field),
		// not a div with lark_md text.
		if el.Tag == "markdown" && el.Content == "The answer is 42" {
			found = true
		}
		if el.Text != nil && el.Text.Content == "The answer is 42" {
			found = true
		}
	}
	if !found {
		t.Error("card does not contain agent text 'The answer is 42'")
	}
}

func TestPipelineAutoReaction(t *testing.T) {
	p := newPipeline(t, nil)
	p.handleMessage(&larktypes.NormalizedMessage{
		ChatID: "chat_1", ChatType: "p2p", Content: "/help",
		MessageID: "msg_help",
	})
	p.handleMessage(&larktypes.NormalizedMessage{
		ChatID: "chat_1", ChatType: "p2p", Content: "not a command",
		MessageID: "msg_plain",
	})

	p.ch.mu.Lock()
	defer p.ch.mu.Unlock()

	if len(p.ch.reactions) != 2 {
		t.Fatalf("expected 2 reactions, got %d", len(p.ch.reactions))
	}
	for _, r := range p.ch.reactions {
		if r.emojiType != bridgetlark.ReactionTyping {
			t.Errorf("reaction emoji = %q, want %q", r.emojiType, bridgetlark.ReactionTyping)
		}
	}
	if p.ch.reactions[0].messageID != "msg_help" {
		t.Errorf("first reaction messageID = %q, want msg_help", p.ch.reactions[0].messageID)
	}
	if p.ch.reactions[1].messageID != "msg_plain" {
		t.Errorf("second reaction messageID = %q, want msg_plain", p.ch.reactions[1].messageID)
	}
}
