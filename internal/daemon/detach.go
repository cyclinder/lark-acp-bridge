// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/config"
)

// Detach spawns the bridge as a detached background process running
// `binary run --foreground` with the given config path, and records the
// daemon state + PID file. The caller (parent) is expected to exit shortly
// after.
//
// The child is placed in its own session (setsid) so it survives the parent
// exiting, and its stdout/stderr are redirected to a daemon log file under
// the bridge home.
func Detach(binary, configPath string) error {
	if binary == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable: %w", err)
		}
		binary = exe
	}

	args := []string{"run", "--foreground"}
	if configPath != "" {
		args = append(args, "-c", configPath)
	}

	if err := config.EnsureDir(); err != nil {
		return err
	}
	logPath := filepath.Join(config.HomeDir(), "logs", "daemon-stdout.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(binary, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	// Start the child in a new session so it is not killed when the parent
	// exits and so it does not hold the controlling terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start detached bridge: %w", err)
	}

	pid := cmd.Process.Pid
	// Release the child so it is not reaped by this parent.
	_ = cmd.Process.Release()

	if err := WritePID(pid); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	if err := SaveState(&State{
		Mode:       ModeProcess,
		PID:        pid,
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
		ConfigPath: configPath,
	}); err != nil {
		return fmt.Errorf("save daemon state: %w", err)
	}
	return nil
}
