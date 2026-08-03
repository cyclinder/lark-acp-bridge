// Copyright 2026 cyclinder kuo
// SPDX-License-Identifier: Apache-2.0

// Package commands implements the bridge's slash command dispatch and
// individual command handlers. All commands are handled locally and never
// invoke the agent subprocess, so they cost zero tokens.
package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/cognition/lark-acp-bridge/internal/agent"
	"github.com/cognition/lark-acp-bridge/internal/card"
	"github.com/cognition/lark-acp-bridge/internal/chatbind"
	"github.com/cognition/lark-acp-bridge/internal/config"
	"github.com/cognition/lark-acp-bridge/internal/provider"
	"github.com/cognition/lark-acp-bridge/internal/run"
	"github.com/cognition/lark-acp-bridge/internal/session"
	"github.com/cognition/lark-acp-bridge/internal/workspace"
)

// Sender is the subset of the Feishu channel needed by command handlers.
type Sender interface {
	SendMarkdown(chatID, markdown, replyTo string) error
	SendCard(chatID string, c card.Card) (messageID string, err error)
}

// AdapterInfo is the subset of agent.AgentAdapter that command handlers
// need. Keeping it as an interface avoids coupling commands to a specific
// adapter implementation.
type AdapterInfo interface {
	ID() string
	DisplayName() string
}

// SessionCloser closes the agent session for a scope (kills the subprocess,
// clears context). Implemented by DevinAdapter.
type SessionCloser interface {
	Close(scope string)
}

// ModelSwitcher switches the model on the scope's active session without
// resetting context. Returns (applied, error): applied=false means no
// active session, caller should stash for next spawn.
type ModelSwitcher interface {
	SetModel(ctx context.Context, scope, model string) (bool, error)
}

// ModelLister enumerates the provider's real, account-specific model list
// on demand (e.g. Copilot via `copilot --acp`). The string is the provider's
// own default model id, used to show the actual "current" when neither the
// session nor the config pins one.
type ModelLister interface {
	ListModels(ctx context.Context) ([]agent.ModelInfo, string, error)
}

// ProviderSessionLister enumerates the provider's own session list on
// demand (e.g. Devin via `devin acp` session/list). Sessions are ordered
// most-recently-updated first, matching the provider's own ordering.
type ProviderSessionLister interface {
	ListSessions(ctx context.Context) ([]agent.SessionInfo, error)
}

// ChatAdmin performs the Feishu group operations that /open needs. It is a
// seam so the commands package stays testable without a live Lark client;
// *lark.Channel implements it.
type ChatAdmin interface {
	// CreateGroup creates a private group named after the project and adds
	// memberOpenID (an open_id) as an initial member. Returns the new
	// chat_id. When the group is created but the member could not be added,
	// a non-empty chatID is returned together with a non-nil error so the
	// caller can record the bind and warn the user to join manually.
	CreateGroup(name, memberOpenID string) (chatID string, err error)
	// EnsureMember best-effort adds a user (open_id) to a group. Adding an
	// existing member is treated as success.
	EnsureMember(chatID, openID string) error
	// GroupExists reports whether the bot can still access a group by its
	// chat_id. Returns false on any error or when the group no longer exists.
	GroupExists(chatID string) bool
	// SearchGroups searches the bot's visible groups by name (server-side
	// fuzzy match) and returns matching ChatInfo entries. Used by /open to
	// discover existing Feishu groups that are not in the local chatbind
	// store (e.g. created manually or whose bind record was lost).
	SearchGroups(name string) ([]ChatInfo, error)
}

// ChatInfo is a minimal projection of a Lark group used by /open for reuse
// and name de-duplication lookups. It mirrors lark.ChatInfo.
type ChatInfo struct {
	ChatID string
	Name   string
}

// ProviderEntry describes one registered provider for the /provider card.
// It mirrors provider.Entry; the alias keeps the commands package's public
// surface stable if provider.Entry grows fields command handlers ignore.
type ProviderEntry = provider.Entry

// ProviderResolver exposes the provider registry to command handlers without
// coupling the commands package to a concrete registry implementation.
type ProviderResolver interface {
	// List returns all registered providers (sorted, with availability).
	List() []ProviderEntry
	// Current returns the effective provider id for a scope (selection or
	// default).
	Current(scope string) string
	// Default returns the default provider id.
	Default() string
	// Set records a provider selection for a scope (persisted).
	Set(scope, id string) error
	// Clear removes a scope's selection, reverting it to the default.
	Clear(scope string) error
}

// Context carries everything a command handler needs.
type Context struct {
	Sender     Sender
	ChatID     string
	Scope      string
	ChatMode   string // "p2p" | "group" | "topic"
	MessageID  string
	// SenderID is the open_id of the user who issued the command, passed
	// through from NormalizedMessage.UserID. /open uses it to add the
	// caller to the newly created group.
	SenderID   string
	Sessions   *session.Store
	Workspaces *workspace.Store
	Active     *run.ActiveRuns
	Config     *config.Config
	Adapter    AdapterInfo
	Executor   *run.Executor
	// Providers, when set, enables the /provider command. Nil in v1-only
	// deployments that do not register multiple providers.
	Providers  ProviderResolver
	// ChatAdmin, when set, enables the /open command. Nil when the bridge
	// runs without group-creation permissions.
	ChatAdmin  ChatAdmin
	// ChatBinds is the persistent chatID -> bind store backing /open reuse.
	ChatBinds  *chatbind.Store
	// ModelList is the cached model options for the current scope, if any.
	ModelList []card.ModelEntry
}

// Handler is one slash command handler.
type Handler func(args string, ctx *Context) error

var handlers = map[string]Handler{
	"/new":      handleNew,
	"/reset":    handleNew,
	"/cd":       handleCd,
	"/ws":       handleWs,
	"/open":     handleOpen,
	"/status":   handleStatus,
	"/pwd":      handlePwd,
	"/stop":     handleStop,
	"/help":     handleHelp,
	"/model":    handleModel,
	"/resume":   handleResume,
	"/sessions": handleSessions,
	"/provider": handleProvider,
}

// TryDispatch checks if a message is a slash command and dispatches it.
// Returns (handled, error). A non-slash message returns (false, nil).
func TryDispatch(text string, ctx *Context) (bool, error) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return false, nil
	}
	parts := strings.Fields(trimmed)
	cmd := parts[0]
	args := strings.TrimSpace(strings.TrimPrefix(trimmed, cmd))
	h, ok := handlers[cmd]
	if !ok {
		return false, nil
	}
	return true, h(args, ctx)
}

func handleNew(_ string, ctx *Context) error {
	ctx.Active.Interrupt(ctx.Scope)
	if c, ok := ctx.Adapter.(SessionCloser); ok {
		c.Close(ctx.Scope)
	}
	_ = ctx.Sessions.Clear(ctx.Scope)
	return ctx.Sender.SendMarkdown(ctx.ChatID, "Session cleared.", ctx.MessageID)
}

func handleCd(args string, ctx *Context) error {
	input := strings.TrimSpace(args)
	if input == "" {
		return ctx.Sender.SendMarkdown(ctx.ChatID, "Usage: `/cd <path>` — absolute, `~/sub`, or relative to the current cwd", ctx.MessageID)
	}
	base := ctx.Workspaces.CwdFor(ctx.Scope, ctx.Config.Workspace.Default)
	cwd, err := workspace.ResolveFrom(input, base)
	if err != nil {
		return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Invalid path: %s", err), ctx.MessageID)
	}
	ctx.Active.Interrupt(ctx.Scope)
	if c, ok := ctx.Adapter.(SessionCloser); ok {
		c.Close(ctx.Scope)
	}
	if err := ctx.Workspaces.SetCwd(ctx.Scope, cwd); err != nil {
		return err
	}
	if err := ctx.Sessions.Clear(ctx.Scope); err != nil {
		return err
	}
	return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Switched cwd to `%s` (session reset).", cwd), ctx.MessageID)
}

func handleStatus(_ string, ctx *Context) error {
	entry, _ := ctx.Sessions.Get(ctx.Scope)
	cwd := ctx.Workspaces.CwdFor(ctx.Scope, ctx.Config.Workspace.Default)
	info := card.StatusInfo{
		Profile:   "default",
		Cwd:       cwd,
		SessionID: entry.SessionID,
		Model:     entry.Model,
		AgentName: ctx.Adapter.DisplayName(),
		ActiveRun: ctx.Active.Get(ctx.Scope) != nil,
		Scope:     ctx.Scope,
		ChatMode:  ctx.ChatMode,
	}
	return sendCard(ctx, card.StatusCard(info))
}

// handlePwd prints the current working directory for the scope. Unlike
// /status it is a lightweight one-liner (no card), useful as a quick check
// after /cd or when resuming a session in a bound group.
func handlePwd(_ string, ctx *Context) error {
	cwd := ctx.Workspaces.CwdFor(ctx.Scope, ctx.Config.Workspace.Default)
	if cwd == "" {
		return ctx.Sender.SendMarkdown(ctx.ChatID, "No working directory set. Use `/cd <path>` first.", ctx.MessageID)
	}
	return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Current directory: `%s`", cwd), ctx.MessageID)
}

func handleStop(_ string, ctx *Context) error {
	h := ctx.Active.Get(ctx.Scope)
	if h == nil {
		return ctx.Sender.SendMarkdown(ctx.ChatID, "No active run to stop.", ctx.MessageID)
	}
	ctx.Active.Interrupt(ctx.Scope)
	return ctx.Sender.SendMarkdown(ctx.ChatID, "Stopped the active run.", ctx.MessageID)
}

func handleHelp(_ string, ctx *Context) error {
	agentCommands := []string{
		"`/model` — list available models (current marked); `/model <N|name>` switches",
		"`/resume` — list saved sessions; `/resume <N>` to reconnect one",
		"`/sessions` — list provider sessions; `/sessions <N>` to continue one",
	}
	c := card.HelpCard(ctx.Adapter.DisplayName(), agentCommands)
	return sendCard(ctx, c)
}

// modelChoices returns the model table for the current scope: the cached
// dynamic list when present, else the provider's own list when it can
// enumerate one, else the built-in fallback table. The second return value
// is the provider's reported default model id ("" when unknown).
func modelChoices(ctx *Context) ([]card.ModelEntry, string) {
	if len(ctx.ModelList) > 0 {
		return ctx.ModelList, ""
	}
	if ml, ok := ctx.Adapter.(ModelLister); ok {
		if listed, currentID, err := ml.ListModels(context.Background()); err == nil && len(listed) > 0 {
			entries := make([]card.ModelEntry, 0, len(listed))
			for _, m := range listed {
				entries = append(entries, card.ModelEntry{Value: m.Value, Name: m.Name})
			}
			return entries, currentID
		}
	}
	return card.FallbackModelsFor(providerID(ctx)), ""
}

// providerID returns the current adapter's provider id, "" when unknown.
func providerID(ctx *Context) string {
	if ctx.Adapter == nil {
		return ""
	}
	return ctx.Adapter.ID()
}

func handleModels(_ string, ctx *Context) error {
	models, providerDefault := modelChoices(ctx)
	current := ""
	if entry, ok := ctx.Sessions.Get(ctx.Scope); ok {
		current = entry.Model
	}
	if current == "" {
		current = ctx.Config.DefaultModelFor(providerID(ctx))
	}
	if current == "" {
		current = providerDefault
	}
	return sendCard(ctx, card.ModelsCard(models, current))
}

func handleModel(args string, ctx *Context) error {
	input := strings.TrimSpace(args)
	if input == "" {
		return handleModels("", ctx)
	}
	models, _ := modelChoices(ctx)
	// Resolve input: either a sequence number or a model value.
	var chosen string
	if n, err := strconv.Atoi(input); err == nil && n >= 1 && n <= len(models) {
		chosen = models[n-1].Value
	} else {
		for _, m := range models {
			if m.Value == input {
				chosen = m.Value
				break
			}
		}
	}
	if chosen == "" {
		return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Unknown model: %s. Use `/model` to list.", input), ctx.MessageID)
	}
	// Try to switch the model on the active session without resetting
	// context. If no session is active, stash the choice for the next spawn.
	if sw, ok := ctx.Adapter.(ModelSwitcher); ok {
		applied, err := sw.SetModel(context.Background(), ctx.Scope, chosen)
		if err != nil {
			return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Model switch failed: %s", err), ctx.MessageID)
		}
		if applied {
			if err := ctx.Sessions.SetModel(ctx.Scope, chosen); err != nil {
				return err
			}
			return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Switched model to `%s` (session preserved).", chosen), ctx.MessageID)
		}
	}
	// No active session: stash the choice for the next spawn and clear the
	// session mapping (context is not portable across models). The entry is
	// replaced rather than Clear()ed so the stashed model survives.
	ctx.Active.Interrupt(ctx.Scope)
	if err := ctx.Sessions.Set(ctx.Scope, session.Entry{Model: chosen}); err != nil {
		return err
	}
	return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Switched model to `%s` (session cleared; will apply on next run).", chosen), ctx.MessageID)
}

func sendCard(ctx *Context, c card.Card) error {
	_, err := ctx.Sender.SendCard(ctx.ChatID, c)
	return err
}

// handleResume implements /resume. With no args it lists all saved sessions.
// With a numeric arg it switches the current scope's session to the selected
// one, allowing the next prompt to resume that session's context.
func handleResume(args string, ctx *Context) error {
	input := strings.TrimSpace(args)
	if input == "" {
		return listSessions(ctx)
	}
	return resumeByIndex(ctx, input)
}

func listSessions(ctx *Context) error {
	items := ctx.Sessions.List()
	rows := make([]card.ResumeRow, 0, len(items))
	for i, item := range items {
		rows = append(rows, card.ResumeRow{
			Index:     i + 1,
			Scope:     item.Scope,
			SessionID: item.SessionID,
			Cwd:       item.Cwd,
			Model:     item.Model,
		})
	}
	return sendCard(ctx, card.ResumeCard(rows, ctx.Scope))
}

func resumeByIndex(ctx *Context, input string) error {
	n, err := strconv.Atoi(input)
	if err != nil {
		return ctx.Sender.SendMarkdown(ctx.ChatID, "Usage: `/resume` to list, or `/resume <N>` to pick a session.", ctx.MessageID)
	}
	items := ctx.Sessions.List()
	if n < 1 || n > len(items) {
		return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Invalid session number %d. Use `/resume` to list (1-%d).", n, len(items)), ctx.MessageID)
	}
	picked := items[n-1]
	// Stop any active run before switching sessions.
	ctx.Active.Interrupt(ctx.Scope)
	// Point the current scope at the picked session, preserving the current
	// cwd and model preferences.
	cur, _ := ctx.Sessions.Get(ctx.Scope)
	cwd := cur.Cwd
	model := cur.Model
	if cwd == "" {
		cwd = picked.Cwd
	}
	if model == "" {
		model = picked.Model
	}
	if err := ctx.Sessions.Set(ctx.Scope, session.Entry{
		SessionID: picked.SessionID,
		Cwd:       cwd,
		Model:     model,
	}); err != nil {
		return err
	}
	sid := picked.SessionID
	if len(sid) > 12 {
		sid = sid[:12] + "..."
	}
	return ctx.Sender.SendMarkdown(ctx.ChatID,
		fmt.Sprintf("Resumed session `%s` (from scope `%s`). Next message will continue that context.", sid, picked.Scope),
		ctx.MessageID)
}

// handleSessions implements /sessions. With no args it lists the current
// provider's own sessions (most recently updated first). With a numeric
// arg it binds the scope to the selected session: the next message loads
// that session and continues its context.
func handleSessions(args string, ctx *Context) error {
	input := strings.TrimSpace(args)
	if input == "" {
		return listProviderSessions(ctx)
	}
	return selectProviderSession(ctx, input)
}

// providerSessionList fetches the provider's session list through the
// ProviderSessionLister seam, or reports that the provider cannot list.
func providerSessionList(ctx *Context) ([]agent.SessionInfo, error) {
	sl, ok := ctx.Adapter.(ProviderSessionLister)
	if !ok {
		return nil, fmt.Errorf("provider `%s` does not support listing sessions", providerID(ctx))
	}
	return sl.ListSessions(context.Background())
}

func listProviderSessions(ctx *Context) error {
	sessions, err := providerSessionList(ctx)
	if err != nil {
		return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Cannot list sessions: %s", err), ctx.MessageID)
	}
	currentID := ""
	if entry, ok := ctx.Sessions.Get(ctx.Scope); ok {
		currentID = entry.SessionID
	}
	// Cap the rendered rows; the card notes the truncation when the
	// provider has more sessions than fit. Selection still resolves
	// against the full list, so visible numbering stays consistent.
	shown := sessions
	if len(shown) > card.MaxSessionRows {
		shown = shown[:card.MaxSessionRows]
	}
	rows := make([]card.SessionRow, 0, len(shown))
	for i, s := range shown {
		rows = append(rows, card.SessionRow{
			Index:     i + 1,
			SessionID: s.ID,
			Title:     s.Title,
			Cwd:       s.Cwd,
			UpdatedAt: s.UpdatedAt,
			Locked:    s.Locked,
			Current:   s.ID == currentID,
		})
	}
	return sendCard(ctx, card.SessionsCard(rows, len(sessions)))
}

func selectProviderSession(ctx *Context, input string) error {
	n, err := strconv.Atoi(input)
	if err != nil {
		return ctx.Sender.SendMarkdown(ctx.ChatID, "Usage: `/sessions` to list, or `/sessions <N>` to pick a session.", ctx.MessageID)
	}
	sessions, err := providerSessionList(ctx)
	if err != nil {
		return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Cannot list sessions: %s", err), ctx.MessageID)
	}
	if n < 1 || n > len(sessions) {
		return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Invalid session number %d. Use `/sessions` to list (1-%d).", n, len(sessions)), ctx.MessageID)
	}
	picked := sessions[n-1]
	if picked.Locked {
		return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Session `%s` is open in another client and cannot be continued here.", picked.ID), ctx.MessageID)
	}
	// Stop any active run and kill the pooled session process so the next
	// message spawns fresh and loads the picked session.
	ctx.Active.Interrupt(ctx.Scope)
	if c, ok := ctx.Adapter.(SessionCloser); ok {
		c.Close(ctx.Scope)
	}
	// Preserve the scope's model preference; the session id and cwd come
	// from the picked session.
	cur, _ := ctx.Sessions.Get(ctx.Scope)
	if err := ctx.Sessions.Set(ctx.Scope, session.Entry{
		SessionID: picked.ID,
		Cwd:       picked.Cwd,
		Model:     cur.Model,
	}); err != nil {
		return err
	}
	// Point the scope's working directory at the session's original cwd;
	// session/load requires the cwd the session was created with.
	if picked.Cwd != "" {
		if err := ctx.Workspaces.SetCwd(ctx.Scope, picked.Cwd); err != nil {
			return err
		}
	}
	title := picked.Title
	if title == "" {
		title = "(untitled)"
	}
	return ctx.Sender.SendMarkdown(ctx.ChatID,
		fmt.Sprintf("Switched to session `%s` (%s). Subsequent messages continue that session; cwd is now `%s`.", picked.ID, title, picked.Cwd),
		ctx.MessageID)
}

// handleProvider implements /provider. With no args it lists registered
// providers. With `<id>` it switches the scope's provider (clearing the
// session, since context is not portable across providers). With `default`
// it clears the override and reverts to the default provider.
func handleProvider(args string, ctx *Context) error {
	if ctx.Providers == nil {
		return ctx.Sender.SendMarkdown(ctx.ChatID, "Provider switching is not configured.", ctx.MessageID)
	}
	input := strings.TrimSpace(args)
	if input == "" {
		return listProviders(ctx)
	}
	if input == "default" {
		return switchProvider(ctx, "", true)
	}
	return switchProvider(ctx, input, false)
}

func listProviders(ctx *Context) error {
	entries := ctx.Providers.List()
	current := ctx.Providers.Current(ctx.Scope)
	def := ctx.Providers.Default()
	rows := make([]card.ProviderRow, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, card.ProviderRow{
			ID:          e.ID,
			DisplayName: e.DisplayName,
			Available:   e.Available,
			Current:     e.ID == current,
			Default:     e.ID == def,
		})
	}
	return sendCard(ctx, card.ProvidersCard(rows))
}

// switchProvider changes the scope's provider selection. Per the product
// spec, switching provider clears the current session (context loss is
// accepted). The old adapter's session is closed if it implements
// SessionCloser.
func switchProvider(ctx *Context, id string, useDefault bool) error {
	if !useDefault {
		found := false
		available := false
		for _, e := range ctx.Providers.List() {
			if e.ID == id {
				found = true
				available = e.Available
				break
			}
		}
		if !found {
			return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Unknown provider: `%s`. Use `/provider` to list.", id), ctx.MessageID)
		}
		if !available {
			return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Provider `%s` is not available (binary missing or not logged in).", id), ctx.MessageID)
		}
	}
	// Stop any active run and close the old adapter's session.
	ctx.Active.Interrupt(ctx.Scope)
	if c, ok := ctx.Adapter.(SessionCloser); ok {
		c.Close(ctx.Scope)
	}
	if err := ctx.Sessions.Clear(ctx.Scope); err != nil {
		return err
	}
	if useDefault {
		if err := ctx.Providers.Clear(ctx.Scope); err != nil {
			return err
		}
		return ctx.Sender.SendMarkdown(ctx.ChatID, "Reverted to the default provider (session cleared).", ctx.MessageID)
	}
	if err := ctx.Providers.Set(ctx.Scope, id); err != nil {
		return err
	}
	return ctx.Sender.SendMarkdown(ctx.ChatID, fmt.Sprintf("Switched provider to `%s` (session cleared).", id), ctx.MessageID)
}
