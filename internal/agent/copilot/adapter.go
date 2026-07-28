// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package copilot implements the AgentAdapter for the GitHub Copilot CLI,
// driven through `copilot -p --output-format json` (NDJSON event stream over
// stdout). Like the Codex adapter, each Run spawns a fresh process that
// exits at the end of the turn; session continuity is achieved by passing
// `--resume <sessionId>` on the next spawn.
package copilot

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/agent"
	bridgetlog "github.com/cognition/lark-acp-bridge/internal/log"
)

// Adapter is the GitHub Copilot implementation of agent.AgentAdapter.
type Adapter struct {
	binary       string
	permissions  string
	defaultModel string

	// Model-list probe cache (see models.go).
	modelsMu        sync.Mutex
	modelsCache     []agent.ModelInfo
	acpCurrentID    string
	modelsFetchedAt time.Time
}

// Option configures an Adapter.
type Option func(*Adapter)

// WithBinary overrides the copilot binary path (default: "copilot" from PATH).
func WithBinary(p string) Option { return func(a *Adapter) { a.binary = p } }

// WithPermissions sets the permission mode: "allow-all", "allow-all-tools",
// or "read-only" (default "allow-all").
func WithPermissions(p string) Option { return func(a *Adapter) { a.permissions = p } }

// WithDefaultModel sets a model to pass via --model on every spawn.
func WithDefaultModel(m string) Option { return func(a *Adapter) { a.defaultModel = m } }

// New creates a CopilotAdapter.
func New(opts ...Option) *Adapter {
	a := &Adapter{binary: "copilot", permissions: "allow-all"}
	for _, o := range opts {
		o(a)
	}
	return a
}

func (a *Adapter) ID() string          { return "copilot" }
func (a *Adapter) DisplayName() string { return "GitHub Copilot" }

// Available checks that the copilot binary responds to --version.
func (a *Adapter) Available(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, a.binary, "--version")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("copilot not available: %w", err)
	}
	if len(out) == 0 {
		return errors.New("copilot --version returned empty output")
	}
	return nil
}

// Run spawns one `copilot -p --output-format json` turn. The prompt is passed
// as the -p argument; events stream over stdout until the `result` event,
// after which the process exits and the events channel is closed.
func (a *Adapter) Run(ctx context.Context, opts agent.RunOptions) (agent.Run, error) {
	if opts.Cwd == "" {
		return nil, errors.New("cwd is required for CopilotAdapter.Run")
	}
	stopGrace := opts.StopGrace
	if stopGrace == 0 {
		stopGrace = 5 * time.Second
	}

	args, err := buildArgs(opts, a.permissions, a.defaultModel)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(a.binary, args...)
	cmd.Dir = opts.Cwd
	cmd.Env = os.Environ()
	// Stdin stays /dev/null: in -p mode Copilot reads piped stdin as extra
	// prompt input, which we never want.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("copilot stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start copilot: %w", err)
	}

	bridgetlog.Info("copilot", "spawn", fmt.Sprintf("pid=%d cwd=%s resume=%t", cmd.Process.Pid, opts.Cwd, opts.SessionID != ""))

	events := make(chan agent.Event, 64)
	r := &copilotRun{
		cmd:       cmd,
		stdout:    stdout,
		events:    events,
		stopGrace: stopGrace,
	}
	go r.pump(ctx)
	return r, nil
}

// buildArgs assembles the `copilot -p` argv. --no-ask-user disables the
// ask_user tool so the agent runs fully unattended; permission flags
// auto-approve tool/path/URL requests per the configured mode.
func buildArgs(opts agent.RunOptions, permissions, defaultModel string) ([]string, error) {
	permFlags, err := permissionFlags(permissions)
	if err != nil {
		return nil, err
	}
	model := opts.Model
	if model == "" {
		model = defaultModel
	}
	args := []string{
		"-p", opts.Prompt,
		"--output-format", "json",
		"--silent",
		"--no-ask-user",
		"--no-color",
	}
	args = append(args, permFlags...)
	if model != "" {
		args = append(args, "--model", model)
	}
	if opts.SessionID != "" {
		args = append(args, "--resume", opts.SessionID)
	}
	return args, nil
}

func permissionFlags(permissions string) ([]string, error) {
	switch permissions {
	case "allow-all", "":
		return []string{"--allow-all"}, nil
	case "allow-all-tools":
		return []string{"--allow-all-tools"}, nil
	case "read-only":
		return []string{"--deny-tool", "shell", "--deny-tool", "edit", "--deny-tool", "create"}, nil
	default:
		return nil, fmt.Errorf("unknown copilot permissions mode %q (want allow-all, allow-all-tools, or read-only)", permissions)
	}
}

// copilotRun is one live copilot -p turn.
type copilotRun struct {
	cmd       *exec.Cmd
	stdout    io.ReadCloser
	events    chan agent.Event
	stopGrace time.Duration

	stopOnce sync.Once
}

func (r *copilotRun) Events() <-chan agent.Event { return r.events }

func (r *copilotRun) Stop() error {
	r.stopOnce.Do(func() {
		// SIGTERM lets copilot flush; the pump goroutine waits up to stopGrace
		// for the process to exit before emitting a terminal event.
		if r.cmd.Process != nil {
			_ = r.cmd.Process.Signal(syscall.SIGTERM)
		}
	})
	return nil
}

func (r *copilotRun) Wait() error { return r.cmd.Wait() }

// pump reads NDJSON events from stdout, translates them, and pushes agent
// events onto the channel. On process exit it emits a terminal event (if the
// stream did not already) and closes the channel.
func (r *copilotRun) pump(ctx context.Context) {
	defer close(r.events)

	tr := newTranslator()
	scanner := bufio.NewScanner(r.stdout)
	// Copilot lines can be long (tool output); raise the per-line limit.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		for _, ev := range tr.translate(line) {
			select {
			case r.events <- ev:
			case <-ctx.Done():
				r.killAndFinish(tr, "cancelled")
				return
			}
		}
		if tr.terminal {
			// Terminal event already pushed; wait for process exit.
			r.waitForExit()
			return
		}
	}

	// stdout closed before a terminal event: distinguish cancel from crash.
	if ctx.Err() != nil {
		r.killAndFinish(tr, "cancelled")
		return
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		r.events <- agent.Event{Type: agent.EventError, SessionID: tr.sessionID, Err: fmt.Errorf("copilot stdout read: %w", err)}
		return
	}
	exitCode := r.waitForExit()
	if exitCode != 0 && exitCode != -1 {
		// Non-zero exit without a terminal event: surface as error unless the
		// translator already emitted one (it sets `terminal`).
		if !tr.terminal {
			r.events <- agent.Event{Type: agent.EventError, SessionID: tr.sessionID, Err: fmt.Errorf("copilot exited with code %d", exitCode)}
		}
		return
	}
	if !tr.terminal {
		for _, ev := range tr.finish("failed") {
			r.events <- ev
		}
	}
}

// killAndFinish SIGKILLs the process, reaps it, and emits a terminal event
// with the given reason. Used when the context is cancelled (/stop).
func (r *copilotRun) killAndFinish(tr *jsonlTranslator, reason string) {
	if r.cmd.Process != nil {
		_ = r.cmd.Process.Signal(syscall.SIGKILL)
		// Reap the zombie; SIGKILL makes this near-instant.
		_ = r.cmd.Wait()
	}
	for _, ev := range tr.finish(reason) {
		r.events <- ev
	}
}

// waitForExit blocks until the process exits or the stop grace elapses.
// Returns the exit code (-1 if unknown/timed out).
func (r *copilotRun) waitForExit() int {
	done := make(chan error, 1)
	go func() { done <- r.cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			return 0
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return -1
	case <-time.After(r.stopGrace):
		if r.cmd.Process != nil {
			_ = r.cmd.Process.Signal(syscall.SIGKILL)
		}
		<-done
		return -1
	}
}
