// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Command lark-acp-bridge starts the Feishu/Lark to Devin ACP bridge.
//
// Usage:
//
//	lark-acp-bridge run [--detach|--mode systemd] [-c config.json]
//	lark-acp-bridge status
//	lark-acp-bridge stop
//	lark-acp-bridge uninstall [--user]
//
// `run` with no flags starts the bridge in the foreground. `--detach` spawns
// it as a background daemon (PID file under the bridge home). `--mode systemd`
// installs and starts a systemd unit (system scope by default, `--user` for
// the user manager) then exits; the bridge itself runs under systemd.
// `status` reports whether the bridge is running and how it was launched.
// `stop` stops a running bridge (SIGTERM for process mode, `systemctl stop`
// for systemd mode). `uninstall` removes the systemd unit.
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
	"github.com/cognition/lark-acp-bridge/internal/daemon"
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
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}

	switch cmd {
	case "run":
		runCmd(args)
	case "status":
		statusCmd(args)
	case "stop":
		stopCmd(args)
	case "uninstall":
		uninstallCmd(args)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `lark-acp-bridge bridges Feishu/Lark with local CLI coding agents.

Usage:
  lark-acp-bridge run [--detach|--mode systemd] [--user] [-c config.json]
  lark-acp-bridge status
  lark-acp-bridge stop
  lark-acp-bridge uninstall [--user]

Commands:
  run          Start the bridge. Default: foreground. --detach: background
               daemon. --mode systemd: install+start a systemd unit.
  status       Show whether the bridge is running and how it was launched.
  stop         Stop a running bridge (SIGTERM or systemctl stop).
  uninstall    Remove the systemd unit (--user for the user manager).

Run flags:
  -c PATH      Config file path (defaults to ~/.lark-acp-bridge/config.json).
  --detach     Run as a background daemon (process mode).
  --mode MODE  Launch mode: process (default) or systemd.
  --user       With --mode systemd/uninstall, target the user systemd manager.
  --binary P   Override the binary path used in the systemd ExecStart.
  --foreground Internal: run the bridge loop (used by --detach and systemd).
`)
}

// runCmd implements the `run` subcommand.
func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("c", "", "path to config file (defaults to ~/.lark-acp-bridge/config.json)")
	detach := fs.Bool("detach", false, "run as a background daemon")
	mode := fs.String("mode", "process", "launch mode: process or systemd")
	userScope := fs.Bool("user", false, "target the user systemd manager (systemd mode)")
	binaryPath := fs.String("binary", "", "override binary path for systemd ExecStart")
	foreground := fs.Bool("foreground", false, "internal: run the bridge loop directly")
	_ = fs.Parse(args)

	resolvedConfig := *configPath
	if resolvedConfig == "" {
		if envCfg := os.Getenv("LARK_ACP_BRIDGE_CONFIG"); envCfg != "" {
			resolvedConfig = envCfg
		}
	}

	switch *mode {
	case "systemd":
		if err := runSystemd(*binaryPath, resolvedConfig, *userScope); err != nil {
			fmt.Fprintf(os.Stderr, "systemd start failed: %v\n", err)
			os.Exit(1)
		}
	case "process", "":
		if *detach {
			if err := runDetach(resolvedConfig); err != nil {
				fmt.Fprintf(os.Stderr, "detach failed: %v\n", err)
				os.Exit(1)
			}
			return
		}
		runForeground(resolvedConfig, *foreground)
	default:
		fmt.Fprintf(os.Stderr, "unknown --mode %q (want process or systemd)\n", *mode)
		os.Exit(2)
	}
}

// runDetach spawns the bridge as a detached background process.
func runDetach(configPath string) error {
	if alreadyRunning() {
		fmt.Fprintln(os.Stderr, "bridge is already running; use `stop` first.")
		os.Exit(1)
	}
	if err := daemon.Detach("", configPath); err != nil {
		return err
	}
	st, _ := daemon.LoadState()
	if st != nil {
		fmt.Printf("bridge detached, pid=%d (logs: %s/logs/)\n", st.PID, config.HomeDir())
	}
	return nil
}

// runSystemd installs and starts the systemd unit, then exits.
func runSystemd(binaryPath, configPath string, userScope bool) error {
	if binaryPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable: %w", err)
		}
		binaryPath = exe
	}
	if configPath == "" {
		configPath = config.Path()
	}
	execStart := binaryPath + " run --foreground -c " + configPath
	if err := daemon.InstallUnit(userScope, execStart, configPath); err != nil {
		return err
	}
	if err := daemon.SaveState(&daemon.State{
		Mode:       daemon.ModeSystemd,
		Unit:       daemon.UnitName,
		UserScope:  userScope,
		ConfigPath: configPath,
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warn: save daemon state: %v\n", err)
	}
	scope := "system"
	if userScope {
		scope = "user"
	}
	fmt.Printf("bridge installed and started via systemd (%s scope, unit=%s)\n", scope, daemon.UnitName)
	return nil
}

// runForeground runs the bridge loop in the foreground. managed is true when
// the process was launched by a manager (the --detach parent or systemd via
// the --foreground flag); such processes skip the already-running guard (the
// launcher already checked) but still record their own daemon state. A direct
// user invocation (managed=false) guards against double-starts.
func runForeground(configPath string, managed bool) {
	if !managed && alreadyRunning() {
		fmt.Fprintln(os.Stderr, "bridge is already running; use `stop` first.")
		os.Exit(1)
	}

	if configPath != "" {
		os.Setenv("LARK_ACP_BRIDGE_HOME", strings.TrimSuffix(configPath, "/config.json"))
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

	// Record daemon state so status/stop can find this process. The launch
	// mode is taken from env vars set by the detach/systemd launchers; the
	// default is plain process mode.
	recordStart(configPath)

	// Start.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		bridgetlog.Info("main", "shutdown", "signal received, closing all sessions")
		closeAllProviders(registry)
		recordStop()
		cancel()
	}()

	bridgetlog.Info("main", "start", "bridge is running")
	if err := ch.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "channel stopped: %v\n", err)
		recordStop()
		os.Exit(1)
	}
	recordStop()
}

// recordStart writes the PID file and daemon state for this foreground
// process, honoring the managed-by env vars set by the launcher.
func recordStart(configPath string) {
	mode, unit, userScope := managedMode()
	pid := os.Getpid()
	if err := daemon.WritePID(pid); err != nil {
		bridgetlog.Warn("main", "pid-write", err.Error())
	}
	st := &daemon.State{
		Mode:       mode,
		PID:        pid,
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
		ConfigPath: configPath,
	}
	if mode == daemon.ModeSystemd {
		st.Unit = unit
		st.UserScope = userScope
	}
	if err := daemon.SaveState(st); err != nil {
		bridgetlog.Warn("main", "state-write", err.Error())
	}
}

// recordStop clears the PID file and daemon state on shutdown.
func recordStop() {
	_ = daemon.RemovePID()
	_ = daemon.ClearState()
}

// managedMode reads the launcher-provided env vars to determine how this
// process was launched. Defaults to process mode.
func managedMode() (daemon.Mode, string, bool) {
	mode := daemon.ModeProcess
	if os.Getenv("LARK_ACP_BRIDGE_MANAGED_BY") == "systemd" {
		mode = daemon.ModeSystemd
	}
	unit := os.Getenv("LARK_ACP_BRIDGE_UNIT")
	userScope := os.Getenv("LARK_ACP_BRIDGE_USER_SCOPE") == "true"
	return mode, unit, userScope
}

// alreadyRunning reports whether a live bridge is already recorded.
func alreadyRunning() bool {
	st, err := daemon.LoadState()
	if err != nil || st == nil {
		return false
	}
	switch st.Mode {
	case daemon.ModeSystemd:
		out, _ := daemon.Systemctl(st.UserScope, "is-active", st.Unit)
		return strings.TrimSpace(string(out)) == "active"
	default:
		return daemon.Alive(st.PID)
	}
}

// statusCmd implements the `status` subcommand.
func statusCmd(args []string) {
	_ = flag.NewFlagSet("status", flag.ExitOnError).Parse(args)
	info, err := daemon.Status()
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: %v\n", err)
		os.Exit(1)
	}
	if !info.Running {
		fmt.Println("bridge is not running")
		if info.Stale {
			fmt.Println("(stale state was cleaned up)")
		}
		return
	}
	switch info.Mode {
	case daemon.ModeSystemd:
		scope := "system"
		if info.UserScope {
			scope = "user"
		}
		fmt.Printf("bridge is running (systemd, %s scope, unit=%s, status=%s)\n", scope, info.Unit, info.SystemdActive)
	default:
		fmt.Printf("bridge is running (process, pid=%d)\n", info.PID)
	}
	if !info.StartedAt.IsZero() {
		fmt.Printf("started: %s (uptime %s)\n", info.StartedAt.Local().Format(time.RFC3339), roundUptime(info.Uptime))
	}
	if info.ConfigPath != "" {
		fmt.Printf("config: %s\n", info.ConfigPath)
	}
}

func roundUptime(d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	return d.Round(time.Minute).String()
}

// stopCmd implements the `stop` subcommand.
func stopCmd(args []string) {
	_ = flag.NewFlagSet("stop", flag.ExitOnError).Parse(args)
	if !alreadyRunning() {
		// Clean up any stale bookkeeping, then report.
		_ = daemon.ClearState()
		_ = daemon.RemovePID()
		fmt.Println("bridge is not running")
		return
	}
	if err := daemon.Stop(); err != nil {
		if err == daemon.ErrNotRunning {
			fmt.Println("bridge is not running")
			return
		}
		fmt.Fprintf(os.Stderr, "stop: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("bridge stopped")
}

// uninstallCmd implements the `uninstall` subcommand.
func uninstallCmd(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	userScope := fs.Bool("user", false, "target the user systemd manager")
	_ = fs.Parse(args)
	if err := daemon.UninstallUnit(*userScope); err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: %v\n", err)
		os.Exit(1)
	}
	_ = daemon.ClearState()
	_ = daemon.RemovePID()
	scope := "system"
	if *userScope {
		scope = "user"
	}
	fmt.Printf("systemd unit removed (%s scope)\n", scope)
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
