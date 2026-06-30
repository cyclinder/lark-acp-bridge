// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/config"
)

// useTempHome points the bridge home at a temp dir for the duration of the
// test and returns the cleanup function.
func useTempHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LARK_ACP_BRIDGE_HOME", dir)
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
}

func TestStateRoundTrip(t *testing.T) {
	useTempHome(t)
	want := &State{
		Mode:       ModeProcess,
		PID:        4242,
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
		ConfigPath: "/tmp/config.json",
	}
	if err := SaveState(want); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got == nil {
		t.Fatal("LoadState returned nil state")
	}
	if got.Mode != want.Mode || got.PID != want.PID || got.ConfigPath != want.ConfigPath {
		t.Errorf("LoadState = %+v, want %+v", got, want)
	}
	if err := ClearState(); err != nil {
		t.Fatalf("ClearState: %v", err)
	}
	if st, _ := LoadState(); st != nil {
		t.Errorf("LoadState after ClearState = %+v, want nil", st)
	}
}

func TestLoadStateMissing(t *testing.T) {
	useTempHome(t)
	st, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState on missing file: %v", err)
	}
	if st != nil {
		t.Errorf("LoadState = %+v, want nil", st)
	}
}

func TestPIDFileRoundTrip(t *testing.T) {
	useTempHome(t)
	if err := WritePID(1234); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	pid, err := ReadPID()
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if pid != 1234 {
		t.Errorf("ReadPID = %d, want 1234", pid)
	}
	if err := RemovePID(); err != nil {
		t.Fatalf("RemovePID: %v", err)
	}
	if pid, _ := ReadPID(); pid != 0 {
		t.Errorf("ReadPID after RemovePID = %d, want 0", pid)
	}
}

func TestAliveCurrentProcess(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Error("Alive(self) = false, want true")
	}
	// PID 1 is init on Linux; an impossible PID should be dead.
	if Alive(999999) {
		t.Error("Alive(999999) = true, want false")
	}
}

func TestStatusNotRunning(t *testing.T) {
	useTempHome(t)
	info, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if info.Running {
		t.Error("Status().Running = true, want false with no state")
	}
}

func TestStatusStaleProcessCleansUp(t *testing.T) {
	useTempHome(t)
	// Record a state pointing at a dead PID.
	if err := SaveState(&State{
		Mode:      ModeProcess,
		PID:       999999,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	if err := WritePID(999999); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	info, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if info.Running {
		t.Error("Status().Running = true for dead PID")
	}
	if !info.Stale {
		t.Error("Status().Stale = false, want true")
	}
	// Stale bookkeeping must be cleaned up.
	if _, err := os.Stat(StatePath()); !os.IsNotExist(err) {
		t.Errorf("state file still exists after stale Status: %v", err)
	}
	if _, err := os.Stat(PIDPath()); !os.IsNotExist(err) {
		t.Errorf("pid file still exists after stale Status: %v", err)
	}
}

func TestStopNotRunning(t *testing.T) {
	useTempHome(t)
	if err := Stop(); err != ErrNotRunning {
		t.Errorf("Stop with no state = %v, want ErrNotRunning", err)
	}
}

func TestStopDeadProcessCleansUp(t *testing.T) {
	useTempHome(t)
	if err := SaveState(&State{Mode: ModeProcess, PID: 999999}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	if err := Stop(); err != ErrNotRunning {
		t.Errorf("Stop dead process = %v, want ErrNotRunning", err)
	}
	if _, err := os.Stat(StatePath()); !os.IsNotExist(err) {
		t.Errorf("state file still exists after Stop: %v", err)
	}
}

func TestRenderUnit(t *testing.T) {
	got := renderUnit("/usr/local/bin/lark-acp-bridge run --foreground -c /home/me/.lark-acp-bridge/config.json", "/home/me/.lark-acp-bridge/config.json", false)
	for _, want := range []string{
		"Type=simple",
		"ExecStart=/usr/local/bin/lark-acp-bridge run --foreground -c /home/me/.lark-acp-bridge/config.json",
		"Restart=on-failure",
		"WantedBy=multi-user.target",
		"LARK_ACP_BRIDGE_MANAGED_BY=systemd",
		"LARK_ACP_BRIDGE_UNIT=" + UnitName,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderUnit(system) missing %q\n---\n%s", want, got)
		}
	}

	gotUser := renderUnit("/usr/local/bin/lark-acp-bridge run --foreground", "/cfg.json", true)
	if !strings.Contains(gotUser, "WantedBy=default.target") {
		t.Errorf("renderUnit(user) missing WantedBy=default.target\n---\n%s", gotUser)
	}
}

func TestUnitDir(t *testing.T) {
	dir, err := UnitDir(false)
	if err != nil {
		t.Fatalf("UnitDir(system): %v", err)
	}
	if dir != "/etc/systemd/system" {
		t.Errorf("UnitDir(system) = %q, want /etc/systemd/system", dir)
	}
	udir, err := UnitDir(true)
	if err != nil {
		t.Fatalf("UnitDir(user): %v", err)
	}
	if !filepath.IsAbs(udir) {
		t.Errorf("UnitDir(user) = %q, want absolute path", udir)
	}
}
