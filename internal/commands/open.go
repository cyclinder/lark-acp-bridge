package commands

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cognition/lark-acp-bridge/internal/chatbind"
	"github.com/cognition/lark-acp-bridge/internal/workspace"
)

// handleOpen implements /open. From a p2p chat it creates (or reuses) a
// Feishu group bound to a working directory, so the user can run the agent
// in a dedicated multi-user context. The new group inherits the current
// scope's provider and model selections.
//
// Usage:
//
//	/open            use the current scope's cwd (must be set via /cd)
//	/open <path>     resolve the path and bind the new group to it
//
// Only direct (p2p) chats can run /open; in groups the user is asked to
// switch to a 1:1 with the bot. The command never invokes the agent
// subprocess, so it costs zero tokens.
func handleOpen(args string, ctx *Context) error {
	if ctx.ChatMode != "p2p" {
		return ctx.Sender.SendMarkdown(ctx.ChatID,
			"Please use `/open` in a direct message with the bot.",
			ctx.MessageID)
	}
	if ctx.ChatAdmin == nil || ctx.ChatBinds == nil {
		return ctx.Sender.SendMarkdown(ctx.ChatID,
			"Group creation is not configured on this bridge.",
			ctx.MessageID)
	}

	cwd, err := resolveOpenCwd(args, ctx)
	if err != nil {
		return ctx.Sender.SendMarkdown(ctx.ChatID, err.Error(), ctx.MessageID)
	}

	// Reuse: a group is already bound to this cwd — but only if it still
	// exists on Feishu AND the caller can be re-added. If the group was
	// deleted, the bot was removed, or the caller cannot be added back
	// (e.g. missing im:chat:members:write scope, or the user's privacy
	// settings block bot-initiated adds), the stale bind record is cleared
	// and a new group is created instead so the user is not stuck with an
	// invisible group.
	if existingID, b, ok := ctx.ChatBinds.FindByCwd(cwd); ok {
		reuse := true
		if ctx.ChatAdmin.GroupExists(existingID) {
			if err := ctx.ChatAdmin.EnsureMember(existingID, ctx.SenderID); err != nil {
				_ = ctx.Sender.SendMarkdown(ctx.ChatID,
					fmt.Sprintf("Existing group **%s** is still around but I could not add you back: %s. Creating a new group instead.", b.Name, err),
					ctx.MessageID)
				reuse = false
			}
		} else {
			reuse = false
		}
		if reuse {
			if err := ctx.Workspaces.SetCwd(existingID, cwd); err != nil {
				return err
			}
			inheritProviderModel(ctx, existingID)
			_ = ctx.Sender.SendMarkdown(existingID,
				fmt.Sprintf("Welcome back. This group is bound to `%s`.", cwd), "")
			return ctx.Sender.SendMarkdown(ctx.ChatID,
				fmt.Sprintf("Reused existing group **%s** for `%s`.", b.Name, cwd),
				ctx.MessageID)
		}
		// Stale or unusable bind — remove the record so the name can be
		// reused and fall through to create a fresh group.
		_ = ctx.ChatBinds.Clear(existingID)
	}

	// Before creating a new group, search Feishu for an existing group
	// with the target name. If the bot is already in a group named after
	// the cwd basename, reuse it instead of creating a duplicate. This
	// covers the case where the local bind record was lost (e.g. cleared
	// or never recorded) but the group still exists on Feishu.
	name := uniqueGroupName(ctx.ChatBinds, filepath.Base(cwd))
	if foundID, foundName, ok := searchExistingGroup(ctx, name); ok {
		if err := ctx.ChatAdmin.EnsureMember(foundID, ctx.SenderID); err != nil {
			// Group exists but can't add the user — fall through to
			// create so they get a fresh group they can actually join.
			_ = ctx.Sender.SendMarkdown(ctx.ChatID,
				fmt.Sprintf("Found an existing group **%s** on Feishu but could not add you: %s. Creating a new group instead.", foundName, err),
				ctx.MessageID)
		} else {
			// Reuse the found group: record the bind, set cwd, inherit.
			if err := ctx.ChatBinds.Set(foundID, chatbind.Bind{
				Name:      foundName,
				Cwd:       cwd,
				CreatedBy: ctx.SenderID,
				CreatedAt: time.Now().Unix(),
			}); err != nil {
				_ = ctx.Sender.SendMarkdown(ctx.ChatID,
					fmt.Sprintf("Reused existing group **%s** but failed to record the bind: %s", foundName, err),
					ctx.MessageID)
			}
			if err := ctx.Workspaces.SetCwd(foundID, cwd); err != nil {
				return err
			}
			inheritProviderModel(ctx, foundID)
			_ = ctx.Sender.SendMarkdown(foundID,
				fmt.Sprintf("Welcome back. This group is bound to `%s`.", cwd), "")
			return ctx.Sender.SendMarkdown(ctx.ChatID,
				fmt.Sprintf("Reused existing group **%s** for `%s`.", foundName, cwd),
				ctx.MessageID)
		}
	}

	// Create: pick a unique name derived from the cwd basename, then create
	// the group with the caller as an initial member.
	newChatID, createErr := ctx.ChatAdmin.CreateGroup(name, ctx.SenderID)
	if newChatID == "" {
		// Hard failure: the group itself was not created.
		return ctx.Sender.SendMarkdown(ctx.ChatID,
			fmt.Sprintf("Failed to create group: %s. Check the bot has `im:chat:create` permission.", createErr),
			ctx.MessageID)
	}
	if err := ctx.ChatBinds.Set(newChatID, chatbind.Bind{
		Name:      name,
		Cwd:       cwd,
		CreatedBy: ctx.SenderID,
		CreatedAt: time.Now().Unix(),
	}); err != nil {
		// Non-fatal: the group exists; we just could not persist the bind.
		// The user can still use the group; reuse will not work until the
		// bind is recorded.
		_ = ctx.Sender.SendMarkdown(ctx.ChatID,
			fmt.Sprintf("Created group **%s** but failed to record the bind: %s", name, err),
			ctx.MessageID)
	}
	if err := ctx.Workspaces.SetCwd(newChatID, cwd); err != nil {
		return err
	}
	inheritProviderModel(ctx, newChatID)
	_ = ctx.Sender.SendMarkdown(newChatID,
		fmt.Sprintf("This group is bound to `%s`. Send any message (and `@bot` in groups) to start a run.", cwd), "")
	if createErr != nil {
		// Partial success: the group was created but the member could not be
		// added (e.g. missing im:chat:member:bot.add_one scope). Warn the
		// user so they can join the group manually via the group link.
		return ctx.Sender.SendMarkdown(ctx.ChatID,
			fmt.Sprintf("Created group **%s** for `%s`, but could not add you automatically: %s. Please join the group manually.", name, cwd, createErr),
			ctx.MessageID)
	}
	return ctx.Sender.SendMarkdown(ctx.ChatID,
		fmt.Sprintf("Created group **%s** for `%s`.", name, cwd),
		ctx.MessageID)
}

// resolveOpenCwd resolves the working directory for /open. With an argument
// it validates the path via workspace.Resolve; without one it falls back to
// the current scope's cwd, requiring /cd to have been run first.
func resolveOpenCwd(args string, ctx *Context) (string, error) {
	input := strings.TrimSpace(args)
	if input != "" {
		base := ctx.Workspaces.CwdFor(ctx.Scope, ctx.Config.Workspace.Default)
		cwd, err := workspace.ResolveFrom(input, base)
		if err != nil {
			return "", fmt.Errorf("Invalid path: %s", err)
		}
		return cwd, nil
	}
	cwd := ctx.Workspaces.CwdFor(ctx.Scope, ctx.Config.Workspace.Default)
	if cwd == "" {
		return "", fmt.Errorf("No working directory set. Use `/cd <path>` first, or pass a path to `/open <path>`.")
	}
	return cwd, nil
}

// searchExistingGroup searches Feishu for a group whose name exactly matches
// the target name. Returns (chatID, name, true) if a match is found and the
// bot can still access it. The search is best-effort: on any API error it
// returns false so /open falls through to creating a new group.
func searchExistingGroup(ctx *Context, name string) (string, string, bool) {
	results, err := ctx.ChatAdmin.SearchGroups(name)
	if err != nil || len(results) == 0 {
		return "", "", false
	}
	for _, r := range results {
		if r.Name == name && ctx.ChatAdmin.GroupExists(r.ChatID) {
			return r.ChatID, r.Name, true
		}
	}
	return "", "", false
}

// uniqueGroupName returns the first free name matching ^base(-\d+)?$ across
// the locally bound groups. It tries base, then base-2, base-3, ...
func uniqueGroupName(binds *chatbind.Store, base string) string {
	items := binds.List()
	taken := make(map[string]bool, len(items))
	for _, item := range items {
		taken[item.Bind.Name] = true
	}
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !taken[candidate] {
			return candidate
		}
	}
}

// inheritProviderModel copies the current scope's provider and model
// selections onto the new group's scope, so the new chat starts with the
// same effective provider/model as the chat that issued /open. Switching
// provider or model clears the session, so only persistent selections are
// inherited (no session id is copied).
func inheritProviderModel(ctx *Context, newScope string) {
	if ctx.Providers != nil {
		cur := ctx.Providers.Current(ctx.Scope)
		if cur != "" && cur != ctx.Providers.Default() {
			_ = ctx.Providers.Set(newScope, cur)
		}
	}
	if entry, ok := ctx.Sessions.Get(ctx.Scope); ok && entry.Model != "" {
		_ = ctx.Sessions.SetModel(newScope, entry.Model)
	}
}
