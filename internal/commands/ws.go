package commands

import (
	"fmt"
	"strings"

	"github.com/cognition/lark-acp-bridge/internal/card"
	"github.com/cognition/lark-acp-bridge/internal/workspace"
)

// handleWs implements /ws, a named-workspace-alias manager.
//
// Usage:
//
//	/ws                list saved aliases (current cwd marked)
//	/ws list           same as above
//	/ws save <name>    save the current cwd as a named alias
//	/ws use <name>     switch to a saved alias (resets session, like /cd)
//	/ws remove <name>  delete a saved alias (alias: /ws rm <name>)
//
// All subcommands are handled locally and never invoke the agent.
func handleWs(args string, ctx *Context) error {
	parts := strings.Fields(args)
	sub := ""
	name := ""
	if len(parts) > 0 {
		sub = parts[0]
	}
	if len(parts) > 1 {
		name = strings.Join(parts[1:], " ")
	}
	switch sub {
	case "", "list":
		return wsList(ctx)
	case "save":
		return wsSave(name, ctx)
	case "use":
		return wsUse(name, ctx)
	case "remove", "rm":
		return wsRemove(name, ctx)
	default:
		return ctx.Sender.SendMarkdown(ctx.ChatID,
			"Usage: `/ws [list|save <name>|use <name>|remove <name>]`", ctx.MessageID)
	}
}

func wsList(ctx *Context) error {
	current := ctx.Workspaces.CwdFor(ctx.Scope, ctx.Config.Workspace.Default)
	named := ctx.Workspaces.ListNamed()
	entries := make([]card.WorkspaceEntry, 0, len(named))
	for _, n := range named {
		entries = append(entries, card.WorkspaceEntry{
			Name:    n.Name,
			Cwd:     n.Cwd,
			Current: n.Cwd == current,
		})
	}
	return sendCard(ctx, card.WorkspacesCard(current, entries))
}

func wsSave(name string, ctx *Context) error {
	if name == "" {
		return ctx.Sender.SendMarkdown(ctx.ChatID, "Usage: `/ws save <name>`", ctx.MessageID)
	}
	cwd := ctx.Workspaces.CwdFor(ctx.Scope, ctx.Config.Workspace.Default)
	if cwd == "" {
		return ctx.Sender.SendMarkdown(ctx.ChatID,
			"No working directory set. Use `/cd <path>` first, then `/ws save <name>`.", ctx.MessageID)
	}
	if err := ctx.Workspaces.SaveNamed(name, cwd); err != nil {
		return err
	}
	return ctx.Sender.SendMarkdown(ctx.ChatID,
		fmt.Sprintf("Saved workspace alias `%s` -> `%s`.", name, cwd), ctx.MessageID)
}

func wsUse(name string, ctx *Context) error {
	if name == "" {
		return ctx.Sender.SendMarkdown(ctx.ChatID, "Usage: `/ws use <name>`", ctx.MessageID)
	}
	cwd := ctx.Workspaces.GetNamed(name)
	if cwd == "" {
		return ctx.Sender.SendMarkdown(ctx.ChatID,
			fmt.Sprintf("No workspace alias named `%s`. Use `/ws` to list.", name), ctx.MessageID)
	}
	resolved, err := workspace.Resolve(cwd)
	if err != nil {
		return ctx.Sender.SendMarkdown(ctx.ChatID,
			fmt.Sprintf("Alias `%s` points to `%s` which is no longer valid: %s", name, cwd, err),
			ctx.MessageID)
	}
	// Switching workspace resets the session, mirroring /cd.
	ctx.Active.Interrupt(ctx.Scope)
	if c, ok := ctx.Adapter.(SessionCloser); ok {
		c.Close(ctx.Scope)
	}
	if err := ctx.Workspaces.SetCwd(ctx.Scope, resolved); err != nil {
		return err
	}
	if err := ctx.Sessions.Clear(ctx.Scope); err != nil {
		return err
	}
	return ctx.Sender.SendMarkdown(ctx.ChatID,
		fmt.Sprintf("Switched to `%s` (`%s`). Session reset.", name, resolved), ctx.MessageID)
}

func wsRemove(name string, ctx *Context) error {
	if name == "" {
		return ctx.Sender.SendMarkdown(ctx.ChatID, "Usage: `/ws remove <name>`", ctx.MessageID)
	}
	if !ctx.Workspaces.RemoveNamed(name) {
		return ctx.Sender.SendMarkdown(ctx.ChatID,
			fmt.Sprintf("No workspace alias named `%s`.", name), ctx.MessageID)
	}
	return ctx.Sender.SendMarkdown(ctx.ChatID,
		fmt.Sprintf("Removed workspace alias `%s`.", name), ctx.MessageID)
}
