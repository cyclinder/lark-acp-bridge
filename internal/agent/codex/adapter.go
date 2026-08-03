// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package codex implements the AgentAdapter for the Codex CLI, driven
// through `codex exec --json` (NDJSON event stream over stdout). Unlike the
// Devin adapter, each Run spawns a fresh `codex exec` process that exits at
// the end of the turn; there is no warm process pool. Session continuity is
// achieved by passing `resume <threadId>` on the next spawn.
package codex

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

// Adapter is the Codex implementation of agent.AgentAdapter.
type Adapter struct {
	binary       string
	sandbox      string
	defaultModel string

	// Session-list scan cache (see sessions.go).
	sessionsMu        sync.Mutex
	sessionsCache     []agent.SessionInfo
	sessionsFetchedAt time.Time
}

// Option configures an Adapter.
type Option func(*Adapter)

// WithBinary overrides the codex binary path (default: "codex" from PATH).
func WithBinary(p string) Option { return func(a *Adapter) { a.binary = p } }

// WithSandbox sets the --sandbox flag (default "danger-full-access").
func WithSandbox(s string) Option { return func(a *Adapter) { a.sandbox = s } }

// WithDefaultModel sets a model to pass via -m on every spawn.
func WithDefaultModel(m string) Option { return func(a *Adapter) { a.defaultModel = m } }

// New creates a CodexAdapter.
func New(opts ...Option) *Adapter {
	a := &Adapter{binary: "codex", sandbox: "danger-full-access"}
	for _, o := range opts {
		o(a)
	}
	return a
}

func (a *Adapter) ID() string          { return "codex" }
func (a *Adapter) DisplayName() string  { return "Codex" }

// Available checks that the codex binary responds to --version.
func (a *Adapter) Available(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, a.binary, "--version")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("codex not available: %w", err)
	}
	if len(out) == 0 {
		return errors.New("codex --version returned empty output")
	}
	return nil
}

// Run spawns one `codex exec --json` turn. The prompt is read from stdin;
// events stream over stdout until turn.completed/turn.failed, after which
// the process exits and the events channel is closed.
func (a *Adapter) Run(ctx context.Context, opts agent.RunOptions) (agent.Run, error) {
	if opts.Cwd == "" {
		return nil, errors.New("cwd is required for CodexAdapter.Run")
	}
	stopGrace := opts.StopGrace
	if stopGrace == 0 {
		stopGrace = 5 * time.Second
	}

	args := buildArgs(opts, a.sandbox, a.defaultModel)
	cmd := exec.Command(a.binary, args...)
	cmd.Dir = opts.Cwd
	cmd.Env = os.Environ()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("codex stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("codex stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex exec: %w", err)
	}

	bridgetlog.Info("codex", "spawn", fmt.Sprintf("pid=%d cwd=%s resume=%t", cmd.Process.Pid, opts.Cwd, opts.SessionID != ""))

	events := make(chan agent.Event, 64)
	r := &codexRun{
		cmd:        cmd,
		stdin:      stdin,
		stdout:     stdout,
		events:     events,
		prompt:     opts.Prompt,
		stopGrace:  stopGrace,
	}
	go r.pump(ctx)
	return r, nil
}

// buildArgs assembles the `codex exec --json` argv. Global flags precede the
// `exec` subcommand; `resume <threadId>` is appended when a session id is
// supplied so the turn continues an existing thread.
func buildArgs(opts agent.RunOptions, sandbox, defaultModel string) []string {
	model := opts.Model
	if model == "" {
		model = defaultModel
	}
	args := []string{
		"exec",
		"--json",
		"--sandbox", sandbox,
		"-c", `approval_policy="never"`,
		"-c", "shell_environment_policy.inherit=\"all\"",
		"--skip-git-repo-check",
		"-C", opts.Cwd,
	}
	if model != "" {
		args = append(args, "-m", model)
	}
	if opts.SessionID != "" {
		args = append(args, "resume", opts.SessionID)
	}
	// Read the prompt from stdin.
	args = append(args, "-")
	return args
}

// codexRun is one live codex exec turn.
type codexRun struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	events    chan agent.Event
	prompt    string
	stopGrace time.Duration

	stopOnce sync.Once
}

func (r *codexRun) Events() <-chan agent.Event { return r.events }

func (r *codexRun) Stop() error {
	r.stopOnce.Do(func() {
		// SIGTERM lets codex flush; the pump goroutine waits up to stopGrace
		// for the process to exit before emitting a terminal event.
		if r.cmd.Process != nil {
			_ = r.cmd.Process.Signal(syscall.SIGTERM)
		}
	})
	return nil
}

func (r *codexRun) Wait() error { return r.cmd.Wait() }

// pump writes the prompt to stdin, then reads NDJSON events from stdout,
// translates them, and pushes agent events onto the channel. On process exit
// it emits a terminal event (if the stream did not already) and closes the
// channel.
func (r *codexRun) pump(ctx context.Context) {
	defer close(r.events)

	// Write the prompt and close stdin so codex begins processing.
	go func() {
		_, _ = r.stdin.Write([]byte(r.prompt))
		_ = r.stdin.Close()
	}()

	tr := newTranslator()
	scanner := bufio.NewScanner(r.stdout)
	// Codex lines can be long (tool output); raise the per-line limit.
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
		r.events <- agent.Event{Type: agent.EventError, SessionID: tr.threadID, Err: fmt.Errorf("codex stdout read: %w", err)}
		return
	}
	exitCode := r.waitForExit()
	if exitCode != 0 && exitCode != -1 {
		// Non-zero exit without a terminal event: surface as error unless the
		// translator already emitted one (it sets `terminal`).
		if !tr.terminal {
			r.events <- agent.Event{Type: agent.EventError, SessionID: tr.threadID, Err: fmt.Errorf("codex exited with code %d", exitCode)}
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
func (r *codexRun) killAndFinish(tr *jsonlTranslator, reason string) {
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
func (r *codexRun) waitForExit() int {
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
