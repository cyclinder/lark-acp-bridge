// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Command lark-acp-bridge starts the Feishu/Lark to Devin ACP bridge.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent/codex"
	"github.com/cognition/lark-acp-bridge/internal/agent/devin"
	"github.com/cognition/lark-acp-bridge/internal/card"
	"github.com/cognition/lark-acp-bridge/internal/chatbind"
	"github.com/cognition/lark-acp-bridge/internal/commands"
	"github.com/cognition/lark-acp-bridge/internal/config"
	"github.com/cognition/lark-acp-bridge/internal/intake"
	"github.com/cognition/lark-acp-bridge/internal/lark"
	bridgetlog "github.com/cognition/lark-acp-bridge/internal/log"
	"github.com/cognition/lark-acp-bridge/internal/provider"
	"github.com/cognition/lark-acp-bridge/internal/run"
	"github.com/cognition/lark-acp-bridge/internal/session"
	"github.com/cognition/lark-acp-bridge/internal/workspace"
	larktypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
)

func main() {
	configPath := flag.String("c", "", "path to config file (defaults to ~/.lark-acp-bridge/config.json)")
	flag.Parse()

	if *configPath != "" {
		os.Setenv("LARK_ACP_BRIDGE_HOME", strings.TrimSuffix(*configPath, "/config.json"))
	}

	if err := config.EnsureDir(); err != nil {
		fmt.Fprintf(os.Stderr, "create config dir: %v\n", err)
		os.Exit(1)
	}
	if err := bridgetlog.Init(config.HomeDir()); err != nil {
		fmt.Fprintf(os.Stderr, "init logger: %v\n", err)
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		fmt.Fprintf(os.Stderr, "Create ~/.lark-acp-bridge/config.json first. Example:\n")
		fmt.Fprintf(os.Stderr, `{"app":{"id":"cli_xxx","secret":"xxx","tenant":"feishu"},"agent":{"binary":"devin","permissionMode":"dangerous"}}`+"\n")
		os.Exit(1)
	}

	// Preflight: check the default provider's binary is installed. The
	// default is normally devin; if the user set defaultProvider=codex, the
	// codex binary is probed instead. Non-default providers are probed
	// lazily by /provider (their absence is reported there, not fatal here).
	registry := buildRegistry(cfg)
	defAdapter := registry.Get(registry.Default())
	if defAdapter == nil {
		fmt.Fprintf(os.Stderr, "preflight failed: default provider %q is not configured\n", registry.Default())
		os.Exit(1)
	}
	if err := defAdapter.Available(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "preflight failed: %v\n", err)
		os.Exit(1)
	}
	bridgetlog.Info("main", "preflight", fmt.Sprintf("default provider %s ready", registry.Default()))

	// Build stores.
	sessions := session.New(config.HomeDir())
	if err := sessions.Load(); err != nil {
		bridgetlog.Warn("main", "sessions-load", err.Error())
	}
	workspaces := workspace.New(config.HomeDir())
	if err := workspaces.Load(); err != nil {
		bridgetlog.Warn("main", "workspaces-load", err.Error())
	}
	chatBinds := chatbind.New(config.HomeDir())
	if err := chatBinds.Load(); err != nil {
		bridgetlog.Warn("main", "chatbinds-load", err.Error())
	}
	if err := registry.Selection().Load(); err != nil {
		bridgetlog.Warn("main", "providers-load", err.Error())
	}

	// Build Feishu channel.
	ch, err := lark.New(cfg.App.ID, cfg.App.Secret, cfg.App.Tenant)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create lark channel: %v\n", err)
		os.Exit(1)
	}

	active := run.NewActiveRuns()
	executor := run.NewExecutor(ch, active)

	// Build the message intake batcher. When a scope's debounce window
	// expires, the handler starts one agent run with the concatenated
	// prompt.
	app := &appCtx{
		cfg:        cfg,
		registry:   registry,
		sessions:   sessions,
		workspaces: workspaces,
		chatBinds:  chatBinds,
		ch:         ch,
		executor:   executor,
		active:     active,
	}
	debounce := time.Duration(cfg.DebounceMs) * time.Millisecond
	batcher := intake.New(debounce, app.startRun)

	// Register message handler.
	ch.OnMessage(func(ctx context.Context, msg *larktypes.NormalizedMessage) error {
		return app.handleMessage(msg, batcher)
	})

	// Start.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		bridgetlog.Info("main", "shutdown", "signal received, closing all sessions")
		closeAllProviders(registry)
		cancel()
	}()

	bridgetlog.Info("main", "start", "bridge is running")
	if err := ch.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "channel stopped: %v\n", err)
		os.Exit(1)
	}
}

// appCtx holds all shared dependencies so the message handler doesn't need
// a long parameter list.
type appCtx struct {
	cfg        *config.Config
	registry   *provider.Registry
	sessions   *session.Store
	workspaces *workspace.Store
	chatBinds  *chatbind.Store
	ch         *lark.Channel
	executor   *run.Executor
	active     *run.ActiveRuns
}

func (a *appCtx) handleMessage(msg *larktypes.NormalizedMessage, batcher *intake.Batcher) error {
	chatID := msg.ChatID
	scope := chatID
	chatMode := "p2p"
	if msg.ChatType == "group" {
		chatMode = "group"
	}

	// In groups, require @bot mention (the SDK sets MentionedBot).
	if chatMode == "group" && !msg.MentionedBot {
		return nil
	}

	// Acknowledge the user message with a quick reaction before doing any work.
	// Errors are logged but ignored so the rest of the handling still runs.
	if err := a.ch.SendReaction(msg.MessageID, lark.ReactionTyping); err != nil {
		bridgetlog.Warn("main", "reaction", err.Error())
	}

	content := stripMentions(msg.Content, msg.Mentions)

	// Resolve the effective adapter for this scope (per-chat /provider
	// override, else the default). The same adapter is used for slash
	// commands and for the run so /status and /help reflect what the next
	// prompt will actually use.
	adapter := a.registry.Resolve(scope)

	// Build command context.
	cmdCtx := &commands.Context{
		Sender:     a.ch,
		ChatID:     chatID,
		Scope:      scope,
		ChatMode:   chatMode,
		MessageID:  msg.MessageID,
		SenderID:   msg.UserID,
		Sessions:   a.sessions,
		Workspaces: a.workspaces,
		Active:     a.active,
		Config:     a.cfg,
		Adapter:    adapter,
		Providers:  a.registry,
		ChatAdmin:  a.ch,
		ChatBinds:  a.chatBinds,
		Executor:   a.executor,
	}

	// Try slash command dispatch first. Slash commands cancel any pending
	// message batch so they take effect immediately.
	handled, err := commands.TryDispatch(content, cmdCtx)
	if err != nil {
		bridgetlog.Error("main", "command", err.Error())
		_ = a.ch.SendMarkdown(chatID, fmt.Sprintf("Command error: %s", err), msg.MessageID)
		return nil
	}
	if handled {
		batcher.Cancel(scope)
		return nil
	}

	// Plain message: queue it for the debounce batcher.
	if content == "" {
		return nil
	}

	// Verify a working directory is set before accepting messages.
	cwd := a.workspaces.CwdFor(scope, a.cfg.Workspace.Default)
	if cwd == "" {
		_ = a.ch.SendMarkdown(chatID, "No working directory set. Use `/cd <path>` first.", msg.MessageID)
		return nil
	}

	// If a run is already active, the new message is queued but will only
	// fire after the current run ends and the debounce window elapses.
	// For v1 this is acceptable; the user sees the active run card update.
	batcher.Push(scope, content)
	return nil
}

// startRun is the batcher flush handler. It resolves the current cwd/session
// and the effective adapter, then launches one agent run with the
// concatenated prompt.
func (a *appCtx) startRun(scope, prompt string) {
	chatID := scope // v1: scope == chatID
	cwd := a.workspaces.CwdFor(scope, a.cfg.Workspace.Default)
	if cwd == "" {
		_ = a.ch.SendMarkdown(chatID, "No working directory set. Use `/cd <path>` first.", "")
		return
	}
	adapter := a.registry.Resolve(scope)
	if adapter == nil {
		_ = a.ch.SendMarkdown(chatID, "No provider available for this chat. Use `/provider` to pick one.", "")
		return
	}
	entry, _ := a.sessions.Get(scope)
	model := entry.Model
	if model == "" {
		model = a.cfg.Agent.DefaultModel
	}
	sessionID := entry.SessionID

	bridgetlog.Info("main", "run-start", fmt.Sprintf("scope=%s provider=%s cwd=%s prompt=%d chars", scope, adapter.ID(), cwd, len(prompt)))

	go func() {
		err := a.executor.Execute(context.Background(), run.ExecuteInput{
			ChatID:    chatID,
			Scope:     scope,
			Prompt:    prompt,
			Cwd:       cwd,
			Model:     model,
			SessionID: sessionID,
			Adapter:   adapter,
			OnDone: func(scope, sid, stopReason string) {
				if sid == "" {
					return
				}
				if err := a.sessions.Set(scope, session.Entry{
					SessionID: sid,
					Cwd:       cwd,
					Model:     model,
				}); err != nil {
					bridgetlog.Error("main", "session-save", err.Error())
				}
				bridgetlog.Info("main", "run-done", fmt.Sprintf("scope=%s session=%s stop=%s", scope, sid, stopReason))
			},
		})
		if err != nil {
			bridgetlog.Error("main", "run", err.Error())
			_ = a.ch.SendMarkdown(chatID, fmt.Sprintf("Run failed: %s", err), "")
			return
		}
	}()
}

// buildRegistry constructs the provider registry from config. Devin is always
// registered (it is the v1 default). Codex is registered when a `codex` block
// is present, so `/provider codex` can switch to it even if the codex binary
// is not yet installed (its absence is reported by /provider, not here).
func buildRegistry(cfg *config.Config) *provider.Registry {
	selection := provider.NewSelection(config.HomeDir())
	registry := provider.NewRegistry(cfg.DefaultProvider, selection)

	devinAdapter := devin.New(
		devin.WithBinary(cfg.Agent.Binary),
		devin.WithPermissionMode(cfg.Agent.PermissionMode),
		devin.WithDefaultModel(cfg.Agent.DefaultModel),
		devin.WithIdleTimeout(time.Duration(cfg.IdleTimeoutMinutes)*time.Minute),
		devin.WithMaxConcurrent(cfg.MaxConcurrentRuns),
	)
	registry.Register(devinAdapter)

	if cfg.Codex != nil {
		registry.Register(codex.New(
			codex.WithBinary(cfg.Codex.Binary),
			codex.WithSandbox(cfg.Codex.Sandbox),
			codex.WithDefaultModel(cfg.Codex.DefaultModel),
		))
	}
	return registry
}

// closeAllProviders shuts down every registered adapter that owns long-lived
// subprocesses. Devin implements CloseAll; Codex spawns a fresh process per
// run and has nothing to close.
func closeAllProviders(registry *provider.Registry) {
	for _, e := range registry.List() {
		if c, ok := registry.Get(e.ID).(interface{ CloseAll() }); ok {
			c.CloseAll()
		}
	}
}

// Suppress unused-import warnings for card until card is wired into the
// streaming path via the executor (it already is internally).
var _ = card.HelpCard

// mentionPlaceholderRe matches Feishu mention placeholders embedded in text
// content, e.g. "@_user_1" or "@_all". These appear in group messages where
// the sender @-mentioned the bot; they must be stripped before command
// dispatch (so "@_user_1 /help" is recognized as "/help") and before
// forwarding the prompt to the agent (the agent has no use for the raw
// placeholder tokens).
var mentionPlaceholderRe = regexp.MustCompile(`@_user_\d+|@_all`)

// stripMentions removes Feishu @-mention placeholders from content and
// collapses leftover whitespace. Mentions listed in msg.Mentions are also
// removed by key in case the platform ever emits a non-numeric placeholder.
func stripMentions(content string, mentions []larktypes.Mention) string {
	s := content
	for _, m := range mentions {
		if m.Key != "" {
			s = strings.ReplaceAll(s, m.Key, " ")
		}
	}
	s = mentionPlaceholderRe.ReplaceAllString(s, " ")
	// Collapse runs of whitespace introduced by the replacements so a
	// leading mention followed by a command becomes "/help" rather than
	// "  /help" (TrimSpace handles the edges, but internal double spaces
	// would otherwise shift arg parsing).
	s = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(s, " "))
	return s
}
