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
	App                App       `json:"app"`
	// Owner optionally pins the bridge owner's open_id. When empty, the
	// owner is resolved from the app's collaborator list (the app creator).
	Owner              string    `json:"owner,omitempty"`
	Workspace          Workspace `json:"workspace"`
	Agent              Agent     `json:"agent"`
	Codex              *Codex    `json:"codex,omitempty"`
	Copilot            *Copilot  `json:"copilot,omitempty"`
	NewIssue           *NewIssue `json:"newIssue,omitempty"`
	DefaultProvider    string    `json:"defaultProvider,omitempty"`
	MaxConcurrentRuns  int       `json:"maxConcurrentRuns"`
	DebounceMs         int       `json:"debounceMs"`
	StopGraceMs        int       `json:"stopGraceMs"`
	IdleTimeoutMinutes int       `json:"idleTimeoutMinutes"`
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

// Copilot permission modes map onto Copilot CLI approval flags:
// "allow-all" (--allow-all: tools, paths, URLs), "allow-all-tools"
// (--allow-all-tools; out-of-workspace paths still denied), and "read-only"
// (best-effort: shell/edit/create denied). Copilot CLI has no kernel sandbox,
// so read-only relies on the model honoring tool denials.
const (
	CopilotPermAllowAll      = "allow-all"
	CopilotPermAllowAllTools = "allow-all-tools"
	CopilotPermReadOnly      = "read-only"
)

// Copilot configures the GitHub Copilot provider. When present and the
// copilot binary is available, `/provider copilot` can switch a chat to it.
type Copilot struct {
	Binary       string `json:"binary,omitempty"`
	Permissions  string `json:"permissions,omitempty"`
	DefaultModel string `json:"defaultModel,omitempty"`
}

// NewIssue tunes the /new-issue command. All fields are optional; zero
// values fall back to the accessor defaults below.
type NewIssue struct {
	// MaxMessages caps how many history messages are fetched (default 500).
	MaxMessages int `json:"maxMessages,omitempty"`
	// CharBudget caps the transcript size in characters; oldest messages
	// are dropped first (default 80000).
	CharBudget int `json:"charBudget,omitempty"`
	// MaxImages caps how many images are downloaded for the agent
	// (default 10, newest first).
	MaxImages int `json:"maxImages,omitempty"`
	// PromptTemplate overrides the built-in prompt. Placeholders:
	// {{repo}}, {{count}}, {{extra}}, {{history}}.
	PromptTemplate string `json:"promptTemplate,omitempty"`
}

// NewIssueMaxMessages returns the history fetch cap for /new-issue.
func (c *Config) NewIssueMaxMessages() int {
	if c.NewIssue != nil && c.NewIssue.MaxMessages > 0 {
		return c.NewIssue.MaxMessages
	}
	return 500
}

// NewIssueCharBudget returns the transcript character budget for /new-issue.
func (c *Config) NewIssueCharBudget() int {
	if c.NewIssue != nil && c.NewIssue.CharBudget > 0 {
		return c.NewIssue.CharBudget
	}
	return 80000
}

// NewIssueMaxImages returns the image download cap for /new-issue.
func (c *Config) NewIssueMaxImages() int {
	if c.NewIssue != nil && c.NewIssue.MaxImages > 0 {
		return c.NewIssue.MaxImages
	}
	return 10
}

// NewIssuePromptTemplate returns the configured prompt template override,
// "" meaning the built-in default.
func (c *Config) NewIssuePromptTemplate() string {
	if c.NewIssue != nil {
		return c.NewIssue.PromptTemplate
	}
	return ""
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
	if c.Copilot != nil {
		if c.Copilot.Binary == "" {
			c.Copilot.Binary = "copilot"
		}
		if c.Copilot.Permissions == "" {
			c.Copilot.Permissions = CopilotPermAllowAll
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
	if c.Copilot != nil {
		switch c.Copilot.Permissions {
		case CopilotPermAllowAll, CopilotPermAllowAllTools, CopilotPermReadOnly:
		default:
			return fmt.Errorf("copilot.permissions must be %q, %q, or %q, got %q",
				CopilotPermAllowAll, CopilotPermAllowAllTools, CopilotPermReadOnly, c.Copilot.Permissions)
		}
	}
	return nil
}

// DefaultModelFor returns the configured default model for a provider id
// ("" when unset, meaning the agent CLI picks its own default).
func (c *Config) DefaultModelFor(providerID string) string {
	switch providerID {
	case "codex":
		if c.Codex != nil {
			return c.Codex.DefaultModel
		}
	case "copilot":
		if c.Copilot != nil {
			return c.Copilot.DefaultModel
		}
	}
	return c.Agent.DefaultModel
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
