// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package devin implements the AgentAdapter for the Devin CLI, driven
// through `devin acp` (Agent Client Protocol over stdio).
package devin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
	"github.com/cognition/lark-acp-bridge/internal/agent/devin/acp"
	bridgetlog "github.com/cognition/lark-acp-bridge/internal/log"
)

// Adapter is the Devin implementation of agent.AgentAdapter. It maintains a
// pool of long-lived `devin acp` subprocesses keyed by scope, so that
// consecutive messages in the same chat reuse the same session (preserving
// context). Idle processes are reaped after idleTimeout. The pool size is
// capped at maxConcurrent; new scopes are rejected when the cap is reached.
type Adapter struct {
	binary         string
	permissionMode string
	defaultModel   string
	agentConfigDir string // optional: where to write temp agent-config files
	idleTimeout    time.Duration
	maxConcurrent  int

	mu   sync.Mutex
	pool map[string]*sessionClient
}

// sessionClient wraps one long-lived devin acp process + ACP client for a
// single scope. It is kept warm between runs so consecutive prompts share
// the same session context.
type sessionClient struct {
	scope       string
	cmd         *exec.Cmd
	client      *acp.Client
	sessionID   string
	configOpts  []acp.ConfigOption
	cwd         string

	// idleTimer fires after a period of no activity; on fire the process
	// is killed and the client is removed from the pool.
	idleTimer *time.Timer
	idleTimeout time.Duration

	// runMu serializes prompt turns on this client (one prompt at a time).
	runMu sync.Mutex

	// alive is false once the process has been killed or detected dead.
	alive bool

	// modelMu protects modelOpts from concurrent SetModel/read.
	modelMu  sync.Mutex
	modelOpts []acp.ConfigOption
}

// Option configures an Adapter.
type Option func(*Adapter)

// WithBinary overrides the devin binary path (default: "devin" from PATH).
func WithBinary(p string) Option { return func(a *Adapter) { a.binary = p } }

// WithPermissionMode sets the --permission-mode flag (default "dangerous").
func WithPermissionMode(m string) Option { return func(a *Adapter) { a.permissionMode = m } }

// WithDefaultModel sets a model to pass via --model on every spawn.
func WithDefaultModel(m string) Option { return func(a *Adapter) { a.defaultModel = m } }

// WithIdleTimeout sets how long a scope's process stays alive after the
// last run ends. Zero disables idle reaping (processes live until /new,
// /cd, or bridge shutdown). Default is 10 minutes.
func WithIdleTimeout(d time.Duration) Option { return func(a *Adapter) { a.idleTimeout = d } }

// WithMaxConcurrent sets the maximum number of concurrent devin acp
// processes (one per scope). When the cap is reached, new scopes are
// rejected with an error until an existing scope's process is reaped by
// idle timeout or /new /cd. Zero disables the cap. Default is 4.
func WithMaxConcurrent(n int) Option { return func(a *Adapter) { a.maxConcurrent = n } }

// New creates a DevinAdapter.
func New(opts ...Option) *Adapter {
	a := &Adapter{
		binary:         "devin",
		permissionMode: "dangerous",
		idleTimeout:    10 * time.Minute,
		maxConcurrent:  4,
		pool:           make(map[string]*sessionClient),
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

func (a *Adapter) ID() string          { return "devin" }
func (a *Adapter) DisplayName() string  { return "Devin" }

// Available checks that the devin binary responds to --version.
func (a *Adapter) Available(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, a.binary, "--version")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("devin not available: %w (run `devin auth login` if not logged in)", err)
	}
	if len(out) == 0 {
		return errors.New("devin --version returned empty output")
	}
	return nil
}

// getOrCreate returns the sessionClient for the given scope, creating a new
// devin acp process if none exists or the existing one has died. The caller
// must hold the returned client's runMu for the duration of the prompt turn.
// If the pool is at capacity and this scope has no existing client, an error
// is returned.
func (a *Adapter) getOrCreate(ctx context.Context, scope, cwd, model string) (*sessionClient, error) {
	a.mu.Lock()
	sc, ok := a.pool[scope]
	a.mu.Unlock()

	if ok && sc.isAlive() {
		return sc, nil
	}
	if ok {
		// Process died; clean up and rebuild.
		sc.kill()
		a.mu.Lock()
		delete(a.pool, scope)
		a.mu.Unlock()
	}

	// Check concurrency cap before spawning a new process. Rebuilding a
	// dead scope's client doesn't increase the count (we just deleted it),
	// but a brand-new scope does.
	if a.maxConcurrent > 0 {
		a.mu.Lock()
		count := len(a.pool)
		_, exists := a.pool[scope]
		a.mu.Unlock()
		if !exists && count >= a.maxConcurrent {
			return nil, fmt.Errorf("too many concurrent sessions (%d/%d); use /new in an idle chat or wait for idle timeout", count, a.maxConcurrent)
		}
	}

	return a.spawn(ctx, scope, cwd, model)
}

// spawn starts a new devin acp process, performs the ACP handshake, creates
// a session, and caches the client in the pool.
func (a *Adapter) spawn(ctx context.Context, scope, cwd, model string) (*sessionClient, error) {
	// Build the devin acp command line. Global flags (--permission-mode,
	// --model) must come BEFORE the "acp" subcommand, not after it.
	args := []string{}
	if a.permissionMode != "" {
		args = append(args, "--permission-mode", a.permissionMode)
	}
	if model == "" {
		model = a.defaultModel
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, "acp")

	cmd := exec.Command(a.binary, args...)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("devin stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("devin stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start devin acp: %w", err)
	}

	client := acp.NewClient(stdin, stdout)

	// Initialize handshake.
	initCtx, initCancel := context.WithTimeout(ctx, 30*time.Second)
	defer initCancel()
	_, err = client.Initialize(initCtx, acp.InitializeParams{
		ProtocolVersion: acp.ProtocolVersion,
		ClientInfo:      acp.ImplementationInfo{Name: "lark-acp-bridge", Version: "0.1.0"},
	})
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("acp initialize: %w", err)
	}

	// Create a new session. (devin 2026.8.18 does not support resume.)
	sn, err := client.SessionNew(ctx, acp.SessionNewParams{
		Cwd:        cwd,
		McpServers: []acp.MCPServer{}, // required field, must not be nil
	})
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("acp session/new: %w", err)
	}

	// Force the session into "bypass" mode (auto-approve all tool calls).
	// The --permission-mode CLI flag does NOT propagate into ACP sessions:
	// every session starts at "accept-edits", which gates exec tool calls
	// behind an interactive approval prompt that nobody can answer in stdio
	// mode, hanging the run forever. Setting config option "mode"="bypass"
	// via session/set_config_option is the supported way to disable prompts.
	configOpts := sn.ConfigOptions
	if res, err := client.SetConfigOption(ctx, acp.SetConfigOptionParams{
		SessionID: sn.SessionID,
		ConfigID:  "mode",
		Value:     "bypass",
	}); err != nil {
		bridgetlog.Info("devin", "bypass-mode", fmt.Sprintf("scope=%s session=%s set failed: %v", scope, sn.SessionID, err))
	} else if res != nil {
		configOpts = res.ConfigOptions
	}

	sc := &sessionClient{
		scope:       scope,
		cmd:         cmd,
		client:      client,
		sessionID:   sn.SessionID,
		configOpts:  configOpts,
		cwd:         cwd,
		idleTimeout: a.idleTimeout,
		alive:       true,
	}

	a.mu.Lock()
	a.pool[scope] = sc
	a.mu.Unlock()

	bridgetlog.Info("devin", "spawn", fmt.Sprintf("scope=%s session=%s mode=bypass", scope, sn.SessionID))
	return sc, nil
}

// Run starts one prompt turn on the scope's sessionClient. If a client
// already exists for the scope (and is alive), it is reused so the prompt
// continues the same session context. Otherwise a new process is spawned.
func (a *Adapter) Run(ctx context.Context, opts agent.RunOptions) (agent.Run, error) {
	if opts.Cwd == "" {
		return nil, errors.New("cwd is required for DevinAdapter.Run")
	}
	stopGrace := opts.StopGrace
	if stopGrace == 0 {
		stopGrace = 5 * time.Second
	}

	sc, err := a.getOrCreate(ctx, opts.Scope, opts.Cwd, opts.Model)
	if err != nil {
		return nil, err
	}

	// Cancel any pending idle timer — the process is about to be active.
	sc.cancelIdleTimer()

	events := make(chan agent.Event, 64)
	r := &devinRun{
		adapter:    a,
		sc:         sc,
		events:     events,
		stopGrace:  stopGrace,
	}

	go r.pumpAndPrompt(ctx, opts.Prompt)
	return r, nil
}

// SetModel switches the model on the scope's active session via ACP
// set_config_option. If no session is active, the choice is stashed by the
// caller (session store) and applied on the next spawn. Returns
// (applied, error): applied=false means no active session, caller should
// stash for next spawn.
func (a *Adapter) SetModel(ctx context.Context, scope, model string) (bool, error) {
	a.mu.Lock()
	sc, ok := a.pool[scope]
	a.mu.Unlock()
	if !ok || !sc.isAlive() {
		return false, nil
	}

	// Find the model config option to get the configId.
	sc.modelMu.Lock()
	opts := sc.modelOpts
	if len(opts) == 0 {
		opts = sc.configOpts
	}
	sc.modelMu.Unlock()

	configID := "model"
	for _, co := range opts {
		if co.Category == "model" || co.ID == "model" {
			configID = co.ID
			break
		}
	}

	res, err := sc.client.SetConfigOption(ctx, acp.SetConfigOptionParams{
		SessionID: sc.sessionID,
		ConfigID:  configID,
		Value:     model,
	})
	if err != nil {
		return true, fmt.Errorf("set_config_option: %w", err)
	}

	sc.modelMu.Lock()
	sc.modelOpts = res.ConfigOptions
	sc.modelMu.Unlock()
	return true, nil
}

// Close kills the devin process for the given scope and removes it from the
// pool. Called by /new, /cd, /reset, and on bridge shutdown. Safe to call
// if no session exists for the scope.
func (a *Adapter) Close(scope string) {
	a.mu.Lock()
	sc, ok := a.pool[scope]
	if ok {
		delete(a.pool, scope)
	}
	a.mu.Unlock()
	if ok {
		sc.kill()
		bridgetlog.Info("devin", "close", fmt.Sprintf("scope=%s session=%s", scope, sc.sessionID))
	}
}

// CloseAll kills all devin processes in the pool. Called on bridge shutdown.
func (a *Adapter) CloseAll() {
	a.mu.Lock()
	scopes := make([]string, 0, len(a.pool))
	for s, sc := range a.pool {
		scopes = append(scopes, s)
		sc.kill()
	}
	a.pool = make(map[string]*sessionClient)
	a.mu.Unlock()
	for _, s := range scopes {
		bridgetlog.Info("devin", "close-all", fmt.Sprintf("scope=%s", s))
	}
}

// ModelOptions returns the model config option for a scope's active session,
// or the built-in fallback if no session is active.
func (a *Adapter) ModelOptions(scope string) []acp.ConfigOptionVal {
	a.mu.Lock()
	sc, ok := a.pool[scope]
	a.mu.Unlock()
	if !ok {
		return fallbackModels
	}
	sc.modelMu.Lock()
	defer sc.modelMu.Unlock()
	opts := sc.modelOpts
	if len(opts) == 0 {
		opts = sc.configOpts
	}
	for _, co := range opts {
		if co.ID == "model" || co.Category == "model" {
			return co.Options
		}
	}
	return fallbackModels
}

// --- sessionClient methods ---

// isAlive returns true only if the devin process is still running.
// We use signal 0 to probe the process rather than relying on
// cmd.ProcessState, because ProcessState is nil until Wait() is called
// and we intentionally don't call Wait() while keeping the process warm.
func (sc *sessionClient) isAlive() bool {
	if !sc.alive {
		return false
	}
	if sc.cmd == nil || sc.cmd.Process == nil {
		return false
	}
	// signal 0 does not actually send a signal; it returns nil if the
	// process exists or an error if it doesn't.
	if err := sc.cmd.Process.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

func (sc *sessionClient) kill() {
	sc.alive = false
	sc.cancelIdleTimer()
	if sc.client != nil {
		_ = sc.client.Close()
	}
	if sc.cmd != nil && sc.cmd.Process != nil {
		_ = sc.cmd.Process.Kill()
		_ = sc.cmd.Wait()
	}
}

func (sc *sessionClient) cancelIdleTimer() {
	if sc.idleTimer != nil {
		sc.idleTimer.Stop()
		sc.idleTimer = nil
	}
}

// startIdleTimer arms the idle reaper. Called after a run ends. When the
// timer fires, the process is killed and the client is removed from the
// adapter's pool.
func (sc *sessionClient) startIdleTimer(a *Adapter) {
	if sc.idleTimeout <= 0 {
		return
	}
	sc.cancelIdleTimer()
	sc.idleTimer = time.AfterFunc(sc.idleTimeout, func() {
		a.mu.Lock()
		cur, ok := a.pool[sc.scope]
		if ok && cur == sc {
			delete(a.pool, sc.scope)
		}
		a.mu.Unlock()
		sc.kill()
		bridgetlog.Info("devin", "idle-timeout", fmt.Sprintf("scope=%s session=%s", sc.scope, sc.sessionID))
	})
}

// --- devinRun ---

type devinRun struct {
	adapter   *Adapter
	sc        *sessionClient
	events    chan agent.Event
	stopGrace time.Duration

	stopOnce sync.Once
}

func (r *devinRun) Events() <-chan agent.Event { return r.events }

func (r *devinRun) Stop() error {
	r.stopOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = r.sc.client.SessionCancel(ctx, acp.SessionCancelParams{SessionID: r.sc.sessionID})
		cancel()
	})
	return nil
}

func (r *devinRun) Wait() error { return r.sc.cmd.Wait() }

// pumpAndPrompt drains ACP notifications into events, then issues
// session/prompt which blocks until the turn ends. Unlike the old
// implementation, it does NOT kill the process after the prompt — the
// sessionClient stays alive in the pool for the next message. An idle timer
// is armed after the run ends.
func (r *devinRun) pumpAndPrompt(ctx context.Context, prompt string) {
	// Acquire the per-client run lock so only one prompt runs at a time.
	r.sc.runMu.Lock()
	defer r.sc.runMu.Unlock()

	// Close the events channel and arm the idle timer when the function
	// returns. This is safe because pumpWg.Wait() (below) guarantees the
	// pump goroutine has stopped sending before we reach any return.
	defer func() {
		close(r.events)
		r.sc.startIdleTimer(r.adapter)
	}()

	// Forward notifications as agent events until the prompt response
	// arrives (signaled via promptDone). We do NOT wait for the Updates
	// channel to close — that only happens when the process exits, and we
	// intentionally keep the process alive for reuse.
	promptDone := make(chan struct{})
	var pumpWg sync.WaitGroup
	pumpWg.Add(1)
	go func() {
		defer pumpWg.Done()
		for {
			select {
			case msg, ok := <-r.sc.client.Updates:
				if !ok {
					return
				}
				ev, ok2 := acp.ParseUpdate(msg)
				if !ok2 {
					continue
				}
				ae := mapEvent(ev)
				ae.SessionID = r.sc.sessionID
				bridgetlog.Info("devin", "event", fmt.Sprintf("scope=%s session=%s kind=%s %s",
					r.sc.scope, r.sc.sessionID, eventKindLabel(ev.Kind), eventSummary(ev)))
				select {
				case r.events <- ae:
				case <-ctx.Done():
					return
				case <-promptDone:
					return
				}
			case <-promptDone:
				return
			}
		}
	}()

	// Issue the prompt; this blocks until stopReason.
	res, err := r.sc.client.SessionPrompt(ctx, acp.SessionPromptParams{
		SessionID: r.sc.sessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: prompt}},
	})

	// Signal the pump goroutine to stop reading notifications, then wait
	// for it to fully exit before closing the events channel. Without this
	// wait, the pump goroutine can panic on send to a closed channel.
	close(promptDone)
	pumpWg.Wait()

	if err != nil {
		// context.Canceled happens when /stop calls Interrupt, which
		// cancels the run context. The session/cancel notification may
		// or may not have arrived first. Either way, the process is
		// still alive — we must NOT mark it dead, or the next run will
		// spawn a fresh session and lose context. Treat cancel as a
		// normal stop with reason "cancelled".
		if errors.Is(err, context.Canceled) {
			r.events <- agent.Event{
				Type:       agent.EventDone,
				SessionID:  r.sc.sessionID,
				StopReason: agent.StopCancelled,
			}
			return
		}
		// Genuine error (process crash, protocol error): mark dead so
		// the next Run spawns a fresh one.
		r.sc.alive = false
		r.events <- agent.Event{Type: agent.EventError, SessionID: r.sc.sessionID, Err: err}
		return
	}

	stopReason := ""
	if res != nil {
		stopReason = res.StopReason
	}
	r.events <- agent.Event{
		Type:       agent.EventDone,
		SessionID:  r.sc.sessionID,
		StopReason: stopReason,
	}
}

// mapEvent translates an acp.Event into an agent.Event.
func mapEvent(ev acp.Event) agent.Event {
	switch ev.Kind {
	case acp.EventKindText:
		return agent.Event{Type: agent.EventText, Delta: ev.Text}
	case acp.EventKindPlan:
		return agent.Event{Type: agent.EventThinking, Delta: ev.Text}
	case acp.EventKindToolStart:
		return agent.Event{Type: agent.EventToolUse, ToolID: ev.ToolID, ToolName: ev.ToolTitle, ToolInput: ev.ToolInput}
	case acp.EventKindToolUpdate:
		return agent.Event{Type: agent.EventToolResult, ToolID: ev.ToolID, ToolOutput: ev.ToolOutput,
			ToolError: ev.ToolStatus == "failed"}
	case acp.EventKindUsage:
		return agent.Event{Type: agent.EventUsage, InputTokens: ev.UsedTokens, CostUSD: ev.CostAmount}
	default:
		return agent.Event{Type: agent.EventText, Delta: ""}
	}
}

// eventKindLabel returns a short human-readable label for an ACP event kind,
// used in structured logs so the operator can see what the agent is emitting.
func eventKindLabel(k acp.EventKind) string {
	switch k {
	case acp.EventKindText:
		return "text"
	case acp.EventKindPlan:
		return "plan"
	case acp.EventKindToolStart:
		return "tool_start"
	case acp.EventKindToolUpdate:
		return "tool_update"
	case acp.EventKindUsage:
		return "usage"
	case acp.EventKindConfig:
		return "config"
	case acp.EventKindOther:
		return "other"
	}
	return "unknown"
}

// eventSummary returns a short, log-safe summary of an ACP event's payload
// (truncated so a single log line stays readable).
func eventSummary(ev acp.Event) string {
	switch ev.Kind {
	case acp.EventKindText, acp.EventKindPlan:
		return truncateLog(ev.Text, 80)
	case acp.EventKindToolStart:
		return fmt.Sprintf("tool=%s kind=%s status=%s input=%s", ev.ToolTitle, ev.ToolKind, ev.ToolStatus, truncateLog(string(ev.ToolInput), 200))
	case acp.EventKindToolUpdate:
		return fmt.Sprintf("tool=%s status=%s out=%s", ev.ToolTitle, ev.ToolStatus, truncateLog(ev.ToolOutput, 80))
	case acp.EventKindUsage:
		return fmt.Sprintf("used=%d size=%d cost=%.4f%s", ev.UsedTokens, ev.SizeTokens, ev.CostAmount, ev.CostCurrency)
	case acp.EventKindConfig:
		return fmt.Sprintf("opts=%d", len(ev.ConfigOptions))
	case acp.EventKindOther:
		return truncateLog(ev.Text, 80)
	}
	return ""
}

func truncateLog(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// fallbackModels is used when the agent does not expose a dynamic model list.
var fallbackModels = []acp.ConfigOptionVal{
	{Value: "adaptive", Name: "Adaptive (auto)"},
	{Value: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
	{Value: "claude-opus-4-8-medium", Name: "Claude Opus 4.8 Medium"},
	{Value: "gpt-5-5-medium", Name: "GPT-5.5 Medium"},
}

// AgentConfigPath returns a temp file path for a declarative agent config,
// or empty if none is needed. Reserved for v1.1 system-prompt injection.
func (a *Adapter) AgentConfigPath() string {
	if a.agentConfigDir == "" {
		return ""
	}
	return filepath.Join(a.agentConfigDir, "bridge-agent-config.yaml")
}
