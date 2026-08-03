// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/cognition/lark-acp-bridge/internal/agent"
	"github.com/cognition/lark-acp-bridge/internal/card"
	"github.com/cognition/lark-acp-bridge/internal/config"
	"github.com/cognition/lark-acp-bridge/internal/run"
	"github.com/cognition/lark-acp-bridge/internal/session"
	"github.com/cognition/lark-acp-bridge/internal/workspace"
)

// fakeAdapter implements AdapterInfo for testing.
type fakeAdapter struct {
	name string
}

func (a *fakeAdapter) ID() string          { return "fake" }
func (a *fakeAdapter) DisplayName() string  { return a.name }

// fakeSender records sends for assertion in tests.
type fakeSender struct {
	markdowns []sentMarkdown
	cards     []sentCard
}

type sentMarkdown struct {
	chatID, markdown, replyTo string
}

type sentCard struct {
	chatID string
	card   card.Card
}

func (f *fakeSender) SendMarkdown(chatID, markdown, replyTo string) error {
	f.markdowns = append(f.markdowns, sentMarkdown{chatID, markdown, replyTo})
	return nil
}

func (f *fakeSender) SendCard(chatID string, c card.Card) (string, error) {
	f.cards = append(f.cards, sentCard{chatID, c})
	return "msg_fake", nil
}

func newTestContext(t *testing.T) *Context {
	t.Helper()
	dir := t.TempDir()
	return &Context{
		Sender:     &fakeSender{},
		ChatID:     "chat_1",
		Scope:      "chat_1",
		ChatMode:   "p2p",
		MessageID:  "msg_1",
		Sessions:   session.New(dir),
		Workspaces: workspace.New(dir),
		Active:     run.NewActiveRuns(),
		Config:     &config.Config{App: config.App{ID: "x", Secret: "s"}, Agent: config.Agent{Binary: "devin"}},
		Adapter:    &fakeAdapter{name: "Devin"},
	}
}

func TestTryDispatchNonCommand(t *testing.T) {
	ctx := newTestContext(t)
	handled, err := TryDispatch("hello world", ctx)
	if handled || err != nil {
		t.Errorf("TryDispatch(non-command) = (%v, %v), want (false, nil)", handled, err)
	}
}

func TestTryDispatchNew(t *testing.T) {
	ctx := newTestContext(t)
	handled, err := TryDispatch("/new", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /new = (%v, %v), want (true, nil)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.markdowns) != 1 {
		t.Fatalf("expected 1 markdown reply, got %d", len(fs.markdowns))
	}
	if fs.markdowns[0].markdown != "Session cleared." {
		t.Errorf("reply = %q, want %q", fs.markdowns[0].markdown, "Session cleared.")
	}
}

func TestTryDispatchHelp(t *testing.T) {
	ctx := newTestContext(t)
	handled, err := TryDispatch("/help", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /help = (%v, %v), want (true, nil)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.cards) != 1 {
		t.Fatalf("expected 1 card, got %d", len(fs.cards))
	}
}

func TestTryDispatchUnknownCommand(t *testing.T) {
	ctx := newTestContext(t)
	handled, _ := TryDispatch("/nonexistent", ctx)
	if handled {
		t.Error("unknown command should not be handled")
	}
}

func TestTryDispatchCdNoArg(t *testing.T) {
	ctx := newTestContext(t)
	handled, err := TryDispatch("/cd", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /cd = (%v, %v), want (true, nil)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.markdowns) == 0 {
		t.Fatal("expected a usage reply")
	}
}

func TestTryDispatchPwd(t *testing.T) {
	ctx := newTestContext(t)
	dir := t.TempDir()
	if err := ctx.Workspaces.SetCwd(ctx.Scope, dir); err != nil {
		t.Fatal(err)
	}
	handled, err := TryDispatch("/pwd", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /pwd = (%v, %v), want (true, nil)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.markdowns) != 1 {
		t.Fatalf("expected 1 markdown reply, got %d", len(fs.markdowns))
	}
	if !strings.Contains(fs.markdowns[0].markdown, dir) {
		t.Errorf("pwd reply = %q, want it to contain %q", fs.markdowns[0].markdown, dir)
	}
}

func TestTryDispatchPwdNoCwd(t *testing.T) {
	ctx := newTestContext(t)
	// No /cd run and no default workspace — expect a /cd hint, not an error.
	ctx.Config.Workspace.Default = ""
	handled, err := TryDispatch("/pwd", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /pwd = (%v, %v), want (true, nil)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.markdowns) != 1 {
		t.Fatalf("expected 1 markdown reply, got %d", len(fs.markdowns))
	}
	if !strings.Contains(fs.markdowns[0].markdown, "/cd") {
		t.Errorf("expected /cd hint when no cwd set, got %q", fs.markdowns[0].markdown)
	}
}

func TestTryDispatchModelNoArgsShowsCard(t *testing.T) {
	ctx := newTestContext(t)
	handled, err := TryDispatch("/model", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /model = (%v, %v), want (true, nil)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.cards) != 1 {
		t.Fatalf("expected 1 model card, got %d", len(fs.cards))
	}
}

func TestTryDispatchModelsAliasRemoved(t *testing.T) {
	ctx := newTestContext(t)
	handled, _ := TryDispatch("/models", ctx)
	if handled {
		t.Error("/models should no longer be a handled command; use /model")
	}
}

func TestTryDispatchModelByIndex(t *testing.T) {
	ctx := newTestContext(t)
	handled, err := TryDispatch("/model 1", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /model 1 = (%v, %v), want (true, nil)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.markdowns) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(fs.markdowns))
	}
}

// fakeModelListAdapter also implements ModelLister with a dynamic list that
// shares nothing with the fallback tables.
type fakeModelListAdapter struct{ fakeAdapter }

func (a *fakeModelListAdapter) ListModels(_ context.Context) ([]agent.ModelInfo, string, error) {
	return []agent.ModelInfo{
		{Value: "claude-fable-5", Name: "Claude Fable 5"},
		{Value: "gpt-5.6-sol", Name: "GPT-5.6 Sol"},
	}, "claude-fable-5", nil
}

func TestHandleModelUsesDynamicList(t *testing.T) {
	ctx := newTestContext(t)
	ctx.Adapter = &fakeModelListAdapter{fakeAdapter{name: "GitHub Copilot"}}
	// A model absent from every fallback table must be accepted when the
	// provider reports it dynamically.
	handled, err := TryDispatch("/model gpt-5.6-sol", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /model gpt-5.6-sol = (%v, %v), want (true, nil)", handled, err)
	}
	entry, ok := ctx.Sessions.Get(ctx.Scope)
	if !ok || entry.Model != "gpt-5.6-sol" {
		t.Fatalf("stashed model = %+v, want gpt-5.6-sol", entry)
	}
}

func TestHandleModelsShowsProviderDefault(t *testing.T) {
	ctx := newTestContext(t)
	ctx.Adapter = &fakeModelListAdapter{fakeAdapter{name: "GitHub Copilot"}}
	handled, err := TryDispatch("/model", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /model = (%v, %v), want (true, nil)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.cards) != 1 {
		t.Fatalf("expected 1 model card, got %d", len(fs.cards))
	}
}

func TestTryDispatchModelStashesChoiceAndClearsSession(t *testing.T) {
	ctx := newTestContext(t)
	if err := ctx.Sessions.Set(ctx.Scope, session.Entry{SessionID: "sess_old", Cwd: "/tmp"}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	handled, err := TryDispatch("/model 2", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /model 2 = (%v, %v), want (true, nil)", handled, err)
	}
	entry, ok := ctx.Sessions.Get(ctx.Scope)
	if !ok {
		t.Fatal("session entry should survive the switch (it holds the stashed model)")
	}
	// Index 2 in the fallback (Devin) table.
	if entry.Model != "claude-sonnet-4-6" {
		t.Errorf("stashed model = %q, want claude-sonnet-4-6", entry.Model)
	}
	if entry.SessionID != "" {
		t.Errorf("session id should be cleared, got %q", entry.SessionID)
	}
}

// fakeProviderResolver satisfies ProviderResolver for /provider tests.
type fakeProviderResolver struct {
	entries   []ProviderEntry
	current   string
	def       string
	setCalls  map[string]string
	clears    map[string]bool
}

func newFakeProviderResolver() *fakeProviderResolver {
	return &fakeProviderResolver{
		entries: []ProviderEntry{
			{ID: "devin", DisplayName: "Devin", Available: true},
			{ID: "codex", DisplayName: "Codex", Available: true},
		},
		current:  "devin",
		def:      "devin",
		setCalls: map[string]string{},
		clears:   map[string]bool{},
	}
}

func (f *fakeProviderResolver) List() []ProviderEntry { return f.entries }
func (f *fakeProviderResolver) Current(scope string) string { return f.current }
func (f *fakeProviderResolver) Default() string             { return f.def }
func (f *fakeProviderResolver) Set(scope, id string) error  { f.setCalls[scope] = id; f.current = id; return nil }
func (f *fakeProviderResolver) Clear(scope string) error    { f.clears[scope] = true; f.current = f.def; return nil }

func TestProviderListCard(t *testing.T) {
	ctx := newTestContext(t)
	ctx.Providers = newFakeProviderResolver()
	handled, err := TryDispatch("/provider", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /provider = (%v, %v), want (true, nil)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.cards) != 1 {
		t.Fatalf("expected 1 provider card, got %d cards", len(fs.cards))
	}
}

func TestProviderSwitchSetsSelectionAndClearsSession(t *testing.T) {
	ctx := newTestContext(t)
	pr := newFakeProviderResolver()
	ctx.Providers = pr
	// Seed a session so we can assert it gets cleared.
	if err := ctx.Sessions.Set(ctx.Scope, session.Entry{SessionID: "s1", Cwd: "/x"}); err != nil {
		t.Fatal(err)
	}
	handled, err := TryDispatch("/provider codex", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /provider codex = (%v, %v)", handled, err)
	}
	if pr.setCalls[ctx.Scope] != "codex" {
		t.Fatalf("expected Set(codex), got %q", pr.setCalls[ctx.Scope])
	}
	if _, ok := ctx.Sessions.Get(ctx.Scope); ok {
		t.Fatal("session should have been cleared on provider switch")
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.markdowns) != 1 || !strings.Contains(fs.markdowns[0].markdown, "codex") {
		t.Fatalf("expected a codex confirmation reply, got %+v", fs.markdowns)
	}
}

func TestProviderDefaultClearsOverride(t *testing.T) {
	ctx := newTestContext(t)
	pr := newFakeProviderResolver()
	pr.current = "codex"
	ctx.Providers = pr
	handled, err := TryDispatch("/provider default", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /provider default = (%v, %v)", handled, err)
	}
	if !pr.clears[ctx.Scope] {
		t.Fatal("expected Clear to be called")
	}
}

func TestProviderUnknownIdRejected(t *testing.T) {
	ctx := newTestContext(t)
	pr := newFakeProviderResolver()
	ctx.Providers = pr
	handled, err := TryDispatch("/provider nope", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch = (%v, %v)", handled, err)
	}
	if _, ok := pr.setCalls[ctx.Scope]; ok {
		t.Fatal("Set should not be called for unknown provider")
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.markdowns) != 1 || !strings.Contains(fs.markdowns[0].markdown, "Unknown") {
		t.Fatalf("expected unknown-provider reply, got %+v", fs.markdowns)
	}
}

func TestProviderUnavailableRejected(t *testing.T) {
	ctx := newTestContext(t)
	pr := newFakeProviderResolver()
	pr.entries = []ProviderEntry{
		{ID: "devin", DisplayName: "Devin", Available: true},
		{ID: "codex", DisplayName: "Codex", Available: false},
	}
	ctx.Providers = pr
	handled, err := TryDispatch("/provider codex", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch = (%v, %v)", handled, err)
	}
	if _, ok := pr.setCalls[ctx.Scope]; ok {
		t.Fatal("Set should not be called for unavailable provider")
	}
	fs := ctx.Sender.(*fakeSender)
	if !strings.Contains(fs.markdowns[0].markdown, "not available") {
		t.Fatalf("expected unavailable reply, got %q", fs.markdowns[0].markdown)
	}
}

func TestProviderNotConfigured(t *testing.T) {
	ctx := newTestContext(t)
	// ctx.Providers left nil
	handled, err := TryDispatch("/provider", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch = (%v, %v)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if !strings.Contains(fs.markdowns[0].markdown, "not configured") {
		t.Fatalf("expected not-configured reply, got %q", fs.markdowns[0].markdown)
	}
}

// fakeSessionListAdapter also implements ProviderSessionLister and
// SessionCloser for /sessions tests.
type fakeSessionListAdapter struct {
	fakeAdapter
	sessions []agent.SessionInfo
	err      error
	closed   []string
}

func (a *fakeSessionListAdapter) ListSessions(context.Context) ([]agent.SessionInfo, error) {
	return a.sessions, a.err
}

func (a *fakeSessionListAdapter) Close(scope string) { a.closed = append(a.closed, scope) }

func TestHandleSessionsUnsupported(t *testing.T) {
	ctx := newTestContext(t)
	handled, err := TryDispatch("/sessions", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /sessions = (%v, %v), want (true, nil)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.markdowns) != 1 || !strings.Contains(fs.markdowns[0].markdown, "does not support") {
		t.Fatalf("expected unsupported-provider reply, got %+v", fs.markdowns)
	}
}

func TestHandleSessionsList(t *testing.T) {
	ctx := newTestContext(t)
	ctx.Adapter = &fakeSessionListAdapter{
		fakeAdapter: fakeAdapter{name: "Devin"},
		sessions: []agent.SessionInfo{
			{ID: "s1", Title: "First", Cwd: "/a"},
			{ID: "s2", Title: "Second", Cwd: "/b", Locked: true},
		},
	}
	// Bind s1 to the scope so the card marks it as current.
	if err := ctx.Sessions.Set(ctx.Scope, session.Entry{SessionID: "s1", Model: "opus"}); err != nil {
		t.Fatal(err)
	}
	handled, err := TryDispatch("/sessions", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /sessions = (%v, %v), want (true, nil)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.cards) != 1 {
		t.Fatalf("expected 1 sessions card, got %d", len(fs.cards))
	}
	content := fs.cards[0].card.Elements[0].Content
	for _, want := range []string{"First", "Second", "<- current chat", "in use elsewhere"} {
		if !strings.Contains(content, want) {
			t.Errorf("sessions card missing %q:\n%s", want, content)
		}
	}
}

func TestHandleSessionsSelect(t *testing.T) {
	ctx := newTestContext(t)
	ad := &fakeSessionListAdapter{
		fakeAdapter: fakeAdapter{name: "Devin"},
		sessions: []agent.SessionInfo{
			{ID: "s1", Title: "First", Cwd: "/a"},
			{ID: "s2", Title: "Second", Cwd: "/b"},
		},
	}
	ctx.Adapter = ad
	// Pre-existing model preference must survive the switch.
	if err := ctx.Sessions.Set(ctx.Scope, session.Entry{Model: "opus"}); err != nil {
		t.Fatal(err)
	}
	handled, err := TryDispatch("/sessions 2", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /sessions 2 = (%v, %v), want (true, nil)", handled, err)
	}
	entry, ok := ctx.Sessions.Get(ctx.Scope)
	if !ok || entry.SessionID != "s2" || entry.Cwd != "/b" || entry.Model != "opus" {
		t.Errorf("session entry after select = %+v ok=%v, want s2//b/opus", entry, ok)
	}
	if cwd := ctx.Workspaces.CwdFor(ctx.Scope, ""); cwd != "/b" {
		t.Errorf("workspace cwd = %q, want /b", cwd)
	}
	if len(ad.closed) != 1 || ad.closed[0] != ctx.Scope {
		t.Errorf("closed scopes = %v, want [%s]", ad.closed, ctx.Scope)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.markdowns) != 1 || !strings.Contains(fs.markdowns[0].markdown, "Switched to session `s2`") {
		t.Fatalf("unexpected reply: %+v", fs.markdowns)
	}
}

func TestHandleSessionsSelectLocked(t *testing.T) {
	ctx := newTestContext(t)
	ctx.Adapter = &fakeSessionListAdapter{
		fakeAdapter: fakeAdapter{name: "Devin"},
		sessions:    []agent.SessionInfo{{ID: "s1", Cwd: "/a", Locked: true}},
	}
	handled, err := TryDispatch("/sessions 1", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /sessions 1 = (%v, %v), want (true, nil)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.markdowns) != 1 || !strings.Contains(fs.markdowns[0].markdown, "open in another client") {
		t.Fatalf("expected locked refusal, got %+v", fs.markdowns)
	}
	if _, ok := ctx.Sessions.Get(ctx.Scope); ok {
		t.Error("session store must not change when the pick is refused")
	}
}

func TestHandleSessionsInvalidIndex(t *testing.T) {
	ctx := newTestContext(t)
	ctx.Adapter = &fakeSessionListAdapter{
		fakeAdapter: fakeAdapter{name: "Devin"},
		sessions:    []agent.SessionInfo{{ID: "s1", Cwd: "/a"}},
	}
	handled, err := TryDispatch("/sessions 9", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /sessions 9 = (%v, %v), want (true, nil)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.markdowns) != 1 || !strings.Contains(fs.markdowns[0].markdown, "Invalid session number") {
		t.Fatalf("expected invalid-number reply, got %+v", fs.markdowns)
	}
}

// TestHandleSessionsListCapped verifies the card renders at most
// card.MaxSessionRows rows even when the provider reports many sessions,
// and notes the truncation.
func TestHandleSessionsListCapped(t *testing.T) {
	ctx := newTestContext(t)
	sessions := make([]agent.SessionInfo, 50)
	for i := range sessions {
		sessions[i] = agent.SessionInfo{ID: "s", Cwd: "/a"}
	}
	ctx.Adapter = &fakeSessionListAdapter{fakeAdapter: fakeAdapter{name: "Codex"}, sessions: sessions}
	handled, err := TryDispatch("/sessions", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /sessions = (%v, %v), want (true, nil)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.cards) != 1 {
		t.Fatalf("expected 1 card, got %d", len(fs.cards))
	}
	content := fs.cards[0].card.Elements[0].Content
	if !strings.Contains(content, "Showing the 20 most recent of 50 sessions.") {
		t.Errorf("missing truncation footer:\n%s", content)
	}
	if strings.Contains(content, "21. ") {
		t.Errorf("card renders more than %d rows", card.MaxSessionRows)
	}
}
