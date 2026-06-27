// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package config loads and saves the bridge's root configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the root configuration for the bridge. v1 supports a single
// profile; multi-profile is deferred to v2.
type Config struct {
	App                 App       `json:"app"`
	Workspace           Workspace `json:"workspace"`
	Agent               Agent     `json:"agent"`
	Codex               *Codex    `json:"codex,omitempty"`
	DefaultProvider     string    `json:"defaultProvider,omitempty"`
	MaxConcurrentRuns   int       `json:"maxConcurrentRuns"`
	DebounceMs          int       `json:"debounceMs"`
	StopGraceMs         int       `json:"stopGraceMs"`
	IdleTimeoutMinutes  int       `json:"idleTimeoutMinutes"`
}

type App struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
	Tenant string `json:"tenant"` // "feishu" or "lark"
}

type Workspace struct {
	Default string `json:"default,omitempty"`
}

// Agent configures the Devin provider (the v1 default). The `Agent` key is
// kept for backward compatibility; provider-specific knobs live under their
// own keys (e.g. Codex).
type Agent struct {
	Binary         string `json:"binary,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
	DefaultModel   string `json:"defaultModel,omitempty"`
}

// Codex configures the Codex provider. When present and the codex binary is
// available, `/provider codex` can switch a chat to it.
type Codex struct {
	Binary       string `json:"binary,omitempty"`
	Sandbox      string `json:"sandbox,omitempty"`
	DefaultModel string `json:"defaultModel,omitempty"`
}

// Defaults applied when fields are zero.
func defaults(c *Config) {
	if c.Agent.Binary == "" {
		c.Agent.Binary = "devin"
	}
	if c.Agent.PermissionMode == "" {
		c.Agent.PermissionMode = "dangerous"
	}
	if c.DefaultProvider == "" {
		c.DefaultProvider = "devin"
	}
	if c.Codex != nil {
		if c.Codex.Binary == "" {
			c.Codex.Binary = "codex"
		}
		if c.Codex.Sandbox == "" {
			c.Codex.Sandbox = "danger-full-access"
		}
	}
	if c.MaxConcurrentRuns == 0 {
		c.MaxConcurrentRuns = 4
	}
	if c.DebounceMs == 0 {
		c.DebounceMs = 600
	}
	if c.StopGraceMs == 0 {
		c.StopGraceMs = 5000
	}
	if c.IdleTimeoutMinutes == 0 {
		c.IdleTimeoutMinutes = 10
	}
	if c.App.Tenant == "" {
		c.App.Tenant = "feishu"
	}
}

// Validate checks required fields after defaults are applied.
func (c *Config) Validate() error {
	if c.App.ID == "" {
		return errors.New("app.id is required")
	}
	if c.App.Secret == "" {
		return errors.New("app.secret is required")
	}
	if c.App.Tenant != "feishu" && c.App.Tenant != "lark" {
		return fmt.Errorf("app.tenant must be feishu or lark, got %q", c.App.Tenant)
	}
	return nil
}

// HomeDir returns the root state directory, honoring
// LARK_ACP_BRIDGE_HOME or falling back to ~/.lark-acp-bridge.
func HomeDir() string {
	if h := os.Getenv("LARK_ACP_BRIDGE_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".lark-acp-bridge")
}

// Path returns the config file path.
func Path() string { return filepath.Join(HomeDir(), "config.json") }

// Load reads the config from disk, applies defaults, and validates it.
// Returns an error if the file is missing or invalid.
func Load() (*Config, error) {
	p := Path()
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", p, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", p, err)
	}
	defaults(&c)
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save writes the config to disk, creating the directory if needed.
func Save(c *Config) error {
	defaults(c)
	if err := c.Validate(); err != nil {
		return err
	}
	dir := HomeDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), data, 0o600)
}

// EnsureDir creates the home directory tree used by the bridge.
func EnsureDir() error {
	dir := HomeDir()
	for _, sub := range []string{"", "logs"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return err
		}
	}
	return nil
}
