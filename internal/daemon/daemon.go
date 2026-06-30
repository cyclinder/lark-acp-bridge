// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package daemon tracks the running bridge process and provides start/stop/
// status primitives for the CLI. It supports two launch modes:
//
//   - process: the bridge runs as a plain (optionally detached) OS process.
//     A PID file and a state file under the bridge home record the PID and
//     start time so `status` and `stop` can find it.
//   - systemd: the bridge runs as a systemd service. The state file records
//     the unit name and scope (user vs system); `status`/`stop` delegate to
//     systemctl.
//
// The foreground bridge process writes its own state on startup (honoring
// the LARK_ACP_BRIDGE_MANAGED_BY / _UNIT / _USER_SCOPE env vars set by the
// launcher) and clears it on shutdown, so the recorded state always reflects
// the real launch mode.
package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/config"
)

// UnitName is the default systemd unit name for the bridge.
const UnitName = "lark-acp-bridge.service"

// Mode describes how the bridge was launched.
type Mode string

const (
	ModeProcess Mode = "process"
	ModeSystemd Mode = "systemd"
)

// State is the persisted daemon descriptor written under the bridge home.
type State struct {
	Mode       Mode   `json:"mode"`
	PID        int    `json:"pid,omitempty"`
	StartedAt  string `json:"startedAt,omitempty"`
	Unit       string `json:"unit,omitempty"`
	UserScope  bool   `json:"userScope,omitempty"`
	ConfigPath string `json:"configPath,omitempty"`
}

// StatePath returns the path to the daemon state file.
func StatePath() string { return filepath.Join(config.HomeDir(), "daemon.json") }

// PIDPath returns the path to the PID file.
func PIDPath() string { return filepath.Join(config.HomeDir(), "bridge.pid") }

// LoadState reads the daemon state file. A missing file is not an error; a
// nil state is returned instead.
func LoadState() (*State, error) {
	data, err := os.ReadFile(StatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", StatePath(), err)
	}
	return &s, nil
}

// SaveState writes the daemon state file, creating the home dir if needed.
func SaveState(s *State) error {
	if err := config.EnsureDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(StatePath(), data, 0o600)
}

// ClearState removes the daemon state file. A missing file is not an error.
func ClearState() error {
	if err := os.Remove(StatePath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// WritePID writes the PID file. The home dir is created if needed.
func WritePID(pid int) error {
	if err := config.EnsureDir(); err != nil {
		return err
	}
	return os.WriteFile(PIDPath(), []byte(strconv.Itoa(pid)+"\n"), 0o600)
}

// ReadPID reads the PID file. Returns 0 and no error if the file is missing.
func ReadPID() (int, error) {
	data, err := os.ReadFile(PIDPath())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", PIDPath(), err)
	}
	return pid, nil
}

// RemovePID removes the PID file. A missing file is not an error.
func RemovePID() error {
	if err := os.Remove(PIDPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Alive reports whether the given PID is an existing, running process.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// signal 0 probes existence without sending a real signal.
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	// Guard against the PID having been reused by a non-bridge process:
	// require the process to still be killable by us. On Linux, kill 0
	// returning nil is sufficient to say "a process with this pid exists".
	return true
}

// StatusInfo is the resolved, human-readable view of the daemon state.
type StatusInfo struct {
	Running       bool
	Mode          Mode
	PID           int
	StartedAt     time.Time
	Uptime        time.Duration
	Unit          string
	UserScope     bool
	ConfigPath    string
	SystemdActive string // systemctl is-active result (systemd mode only)
	Stale         bool   // state recorded but process not alive
}

// Status inspects the recorded state and the live process/service to produce
// a StatusInfo. It cleans up stale state files when the process is gone.
func Status() (*StatusInfo, error) {
	st, err := LoadState()
	if err != nil {
		return nil, err
	}
	if st == nil {
		return &StatusInfo{Running: false}, nil
	}

	info := &StatusInfo{
		Mode:       st.Mode,
		Unit:       st.Unit,
		UserScope:  st.UserScope,
		ConfigPath: st.ConfigPath,
		PID:        st.PID,
	}
	if st.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339, st.StartedAt); err == nil {
			info.StartedAt = t
			info.Uptime = time.Since(t)
		}
	}

	switch st.Mode {
	case ModeSystemd:
		active, _ := Systemctl(st.UserScope, "is-active", unitOrDefault(st.Unit))
		info.SystemdActive = strings.TrimSpace(string(active))
		info.Running = info.SystemdActive == "active"
		if !info.Running {
			info.Stale = true
		}
	default:
		info.Mode = ModeProcess
		info.Running = Alive(st.PID)
		if !info.Running {
			info.Stale = true
		}
	}

	if info.Stale {
		// Best-effort cleanup of stale bookkeeping.
		_ = ClearState()
		_ = RemovePID()
	}
	return info, nil
}

// Stop terminates the running bridge according to its launch mode. Returns
// ErrNotRunning if no live bridge is recorded.
func Stop() error {
	st, err := LoadState()
	if err != nil {
		return err
	}
	if st == nil {
		return ErrNotRunning
	}

	switch st.Mode {
	case ModeSystemd:
		if out, err := Systemctl(st.UserScope, "stop", unitOrDefault(st.Unit)); err != nil {
			return fmt.Errorf("systemctl stop: %w: %s", err, strings.TrimSpace(string(out)))
		}
	default:
		if !Alive(st.PID) {
			_ = ClearState()
			_ = RemovePID()
			return ErrNotRunning
		}
		if err := syscall.Kill(st.PID, syscall.SIGTERM); err != nil {
			return fmt.Errorf("send SIGTERM to pid %d: %w", st.PID, err)
		}
	}
	return ClearState()
}

// ErrNotRunning is returned by Stop when no live bridge is recorded.
var ErrNotRunning = errors.New("bridge is not running")

func unitOrDefault(unit string) string {
	if unit != "" {
		return unit
	}
	return UnitName
}
