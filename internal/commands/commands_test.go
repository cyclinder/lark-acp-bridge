package commands

import (
	"strings"
	"testing"

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
