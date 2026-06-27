package commands

import (
	"strings"
	"testing"
)

func TestWsListEmpty(t *testing.T) {
	ctx := newTestContext(t)
	handled, err := TryDispatch("/ws", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /ws = (%v, %v), want (true, nil)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.cards) != 1 {
		t.Fatalf("expected 1 card, got %d", len(fs.cards))
	}
}

func TestWsSaveAndList(t *testing.T) {
	ctx := newTestContext(t)
	dir := t.TempDir()
	if err := ctx.Workspaces.SetCwd(ctx.Scope, dir); err != nil {
		t.Fatal(err)
	}
	// Save the current cwd as alias "main".
	handled, err := TryDispatch("/ws save main", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /ws save main = (%v, %v)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.markdowns) == 0 {
		t.Fatal("expected a save confirmation reply")
	}
	if !strings.Contains(fs.markdowns[len(fs.markdowns)-1].markdown, "main") {
		t.Errorf("save reply should mention alias name, got %q", fs.markdowns[len(fs.markdowns)-1].markdown)
	}
	// List should now show the alias in a card.
	fs.markdowns = nil
	fs.cards = nil
	handled, err = TryDispatch("/ws", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /ws (list) = (%v, %v)", handled, err)
	}
	if len(fs.cards) != 1 {
		t.Fatalf("expected 1 card on list, got %d", len(fs.cards))
	}
}

func TestWsSaveNoCwd(t *testing.T) {
	ctx := newTestContext(t)
	ctx.Config.Workspace.Default = ""
	handled, err := TryDispatch("/ws save main", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /ws save = (%v, %v)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.markdowns) != 1 {
		t.Fatal("expected a reply")
	}
	if !strings.Contains(fs.markdowns[0].markdown, "/cd") {
		t.Errorf("expected /cd hint when no cwd set, got %q", fs.markdowns[0].markdown)
	}
}

func TestWsSaveNoName(t *testing.T) {
	ctx := newTestContext(t)
	handled, err := TryDispatch("/ws save", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /ws save = (%v, %v)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if !strings.Contains(fs.markdowns[0].markdown, "Usage") {
		t.Errorf("expected usage hint, got %q", fs.markdowns[0].markdown)
	}
}

func TestWsUseSwitchesCwd(t *testing.T) {
	ctx := newTestContext(t)
	target := t.TempDir()
	if err := ctx.Workspaces.SaveNamed("proj", target); err != nil {
		t.Fatal(err)
	}
	handled, err := TryDispatch("/ws use proj", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /ws use proj = (%v, %v)", handled, err)
	}
	got := ctx.Workspaces.CwdFor(ctx.Scope, "")
	if got != target {
		t.Errorf("cwd after /ws use = %q, want %q", got, target)
	}
	fs := ctx.Sender.(*fakeSender)
	if !strings.Contains(fs.markdowns[0].markdown, "proj") {
		t.Errorf("use reply should mention alias name, got %q", fs.markdowns[0].markdown)
	}
}

func TestWsUseUnknownAlias(t *testing.T) {
	ctx := newTestContext(t)
	handled, err := TryDispatch("/ws use ghost", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /ws use ghost = (%v, %v)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if !strings.Contains(fs.markdowns[0].markdown, "ghost") {
		t.Errorf("reply should mention the unknown alias name, got %q", fs.markdowns[0].markdown)
	}
}

func TestWsRemove(t *testing.T) {
	ctx := newTestContext(t)
	if err := ctx.Workspaces.SaveNamed("temp", "/tmp"); err != nil {
		t.Fatal(err)
	}
	handled, err := TryDispatch("/ws remove temp", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /ws remove temp = (%v, %v)", handled, err)
	}
	if ctx.Workspaces.GetNamed("temp") != "" {
		t.Error("alias should be removed")
	}
}

func TestWsRemoveAliasRm(t *testing.T) {
	ctx := newTestContext(t)
	if err := ctx.Workspaces.SaveNamed("temp", "/tmp"); err != nil {
		t.Fatal(err)
	}
	handled, err := TryDispatch("/ws rm temp", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /ws rm temp = (%v, %v)", handled, err)
	}
	if ctx.Workspaces.GetNamed("temp") != "" {
		t.Error("alias should be removed via rm alias")
	}
}

func TestWsRemoveUnknown(t *testing.T) {
	ctx := newTestContext(t)
	handled, err := TryDispatch("/ws remove ghost", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /ws remove ghost = (%v, %v)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if !strings.Contains(fs.markdowns[0].markdown, "ghost") {
		t.Errorf("reply should mention the unknown alias name, got %q", fs.markdowns[0].markdown)
	}
}

func TestWsInvalidSubcommand(t *testing.T) {
	ctx := newTestContext(t)
	handled, err := TryDispatch("/ws frobnicate", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /ws frobnicate = (%v, %v)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if !strings.Contains(fs.markdowns[0].markdown, "Usage") {
		t.Errorf("expected usage hint for bad subcommand, got %q", fs.markdowns[0].markdown)
	}
}
