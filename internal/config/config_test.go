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
