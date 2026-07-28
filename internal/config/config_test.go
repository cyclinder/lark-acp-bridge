// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAndValidate(t *testing.T) {
	c := &Config{App: App{ID: "cli_x", Secret: "s"}}
	defaults(c)
	if c.Agent.Binary != "devin" {
		t.Errorf("binary = %q, want devin", c.Agent.Binary)
	}
	if c.Agent.PermissionMode != "dangerous" {
		t.Errorf("permissionMode = %q, want dangerous", c.Agent.PermissionMode)
	}
	if c.MaxConcurrentRuns != 4 {
		t.Errorf("maxConcurrentRuns = %d, want 4", c.MaxConcurrentRuns)
	}
	if c.App.Tenant != "feishu" {
		t.Errorf("tenant = %q, want feishu", c.App.Tenant)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestValidateMissingApp(t *testing.T) {
	c := &Config{}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for missing app")
	}
}

func TestCopilotDefaults(t *testing.T) {
	c := &Config{App: App{ID: "cli_x", Secret: "s"}, Copilot: &Copilot{}}
	defaults(c)
	if c.Copilot.Binary != "copilot" {
		t.Errorf("copilot.binary = %q, want copilot", c.Copilot.Binary)
	}
	if c.Copilot.Permissions != CopilotPermAllowAll {
		t.Errorf("copilot.permissions = %q, want %q", c.Copilot.Permissions, CopilotPermAllowAll)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestCopilotValidateRejectsUnknownPermissions(t *testing.T) {
	c := &Config{App: App{ID: "cli_x", Secret: "s"}, Copilot: &Copilot{Permissions: "yolo"}}
	defaults(c)
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for unknown copilot.permissions")
	}
}

func TestDefaultModelFor(t *testing.T) {
	c := &Config{
		Agent:   Agent{DefaultModel: "devin-model"},
		Codex:   &Codex{DefaultModel: "codex-model"},
		Copilot: &Copilot{DefaultModel: "copilot-model"},
	}
	if got := c.DefaultModelFor("devin"); got != "devin-model" {
		t.Errorf("devin = %q", got)
	}
	if got := c.DefaultModelFor("codex"); got != "codex-model" {
		t.Errorf("codex = %q", got)
	}
	if got := c.DefaultModelFor("copilot"); got != "copilot-model" {
		t.Errorf("copilot = %q", got)
	}
	// Missing provider blocks fall back to the devin (Agent) model.
	c2 := &Config{Agent: Agent{DefaultModel: "devin-model"}}
	if got := c2.DefaultModelFor("copilot"); got != "devin-model" {
		t.Errorf("copilot without block = %q, want devin-model", got)
	}
}

func TestLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LARK_ACP_BRIDGE_HOME", dir)
	c := &Config{
		App:               App{ID: "cli_test", Secret: "secret", Tenant: "feishu"},
		Agent:             Agent{Binary: "devin", PermissionMode: "dangerous"},
		MaxConcurrentRuns: 8,
	}
	if err := Save(c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// File should exist.
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.App.ID != "cli_test" {
		t.Errorf("app.id = %q, want cli_test", loaded.App.ID)
	}
	if loaded.MaxConcurrentRuns != 8 {
		t.Errorf("maxConcurrentRuns = %d, want 8", loaded.MaxConcurrentRuns)
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Setenv("LARK_ACP_BRIDGE_HOME", t.TempDir())
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}
