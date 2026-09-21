// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Systemctl runs systemctl with the given args, scoped to the user manager
// when userScope is true (so it operates on `systemctl --user`).
func Systemctl(userScope bool, args ...string) ([]byte, error) {
	cmd := exec.Command("systemctl", args...)
	if userScope {
		cmd = exec.Command("systemctl", append([]string{"--user"}, args...)...)
	}
	return cmd.CombinedOutput()
}

// UnitDir returns the systemd unit directory for the given scope.
func UnitDir(userScope bool) (string, error) {
	if userScope {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".config", "systemd", "user"), nil
	}
	return "/etc/systemd/system", nil
}

// UnitPath returns the full path of the bridge unit file.
func UnitPath(userScope bool) (string, error) {
	dir, err := UnitDir(userScope)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, UnitName), nil
}

// renderUnit builds the systemd unit file content. execStart is the full
// command line the service runs (typically `<binary> run --foreground ...`).
func renderUnit(execStart, configPath string, userScope bool) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=lark-acp-bridge Feishu/Lark to coding agent bridge\n")
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	b.WriteString("ExecStart=" + execStart + "\n")
	b.WriteString("Restart=on-failure\n")
	b.WriteString("RestartSec=5\n")
	// systemd does not set HOME unless User= is present; agent CLIs (copilot,
	// gh, codex) need it to locate their credentials and config.
	if home, err := os.UserHomeDir(); err == nil {
		b.WriteString(fmt.Sprintf("Environment=HOME=%s\n", home))
	}
	if configPath != "" {
		b.WriteString(fmt.Sprintf("Environment=LARK_ACP_BRIDGE_MANAGED_BY=systemd\n"))
		b.WriteString(fmt.Sprintf("Environment=LARK_ACP_BRIDGE_UNIT=%s\n", UnitName))
		b.WriteString(fmt.Sprintf("Environment=LARK_ACP_BRIDGE_USER_SCOPE=%t\n", userScope))
		if configPath != "" {
			b.WriteString(fmt.Sprintf("Environment=LARK_ACP_BRIDGE_CONFIG=%s\n", configPath))
		}
	}
	b.WriteString("\n[Install]\n")
	if userScope {
		b.WriteString("WantedBy=default.target\n")
	} else {
		b.WriteString("WantedBy=multi-user.target\n")
	}
	return b.String()
}

// InstallUnit writes the unit file, reloads the manager, and enables+starts
// the service. execStart is the full command line for ExecStart.
func InstallUnit(userScope bool, execStart, configPath string) error {
	dir, err := UnitDir(userScope)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create unit dir %s: %w", dir, err)
	}
	path := filepath.Join(dir, UnitName)
	content := renderUnit(execStart, configPath, userScope)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write unit file %s: %w", path, err)
	}
	if out, err := Systemctl(userScope, "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := Systemctl(userScope, "enable", "--now", UnitName); err != nil {
		return fmt.Errorf("systemctl enable --now: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// UninstallUnit stops, disables, and removes the bridge unit file, then
// reloads the manager. A missing unit file is not an error.
func UninstallUnit(userScope bool) error {
	Systemctl(userScope, "disable", "--now", UnitName) // best-effort
	path, err := UnitPath(userScope)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unit file %s: %w", path, err)
	}
	if out, err := Systemctl(userScope, "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
