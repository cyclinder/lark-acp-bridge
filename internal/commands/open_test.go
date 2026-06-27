package commands

import (
	"strings"
	"testing"

	"github.com/cognition/lark-acp-bridge/internal/chatbind"
	"github.com/cognition/lark-acp-bridge/internal/config"
	"github.com/cognition/lark-acp-bridge/internal/session"
	"github.com/cognition/lark-acp-bridge/internal/workspace"
)

// fakeChatAdmin records calls and returns deterministic chat ids.
type fakeChatAdmin struct {
	created        []fakeCreate
	ensureCalls    []fakeEnsure
	createErr      error
	memberAddErr   error // non-nil: CreateGroup returns (chatID, err) — partial success
	ensureErr      error // non-nil: EnsureMember returns this error
	nextID         string
	groupExistsMap map[string]bool // nil means "all exist" (default true)
	searchResults  []ChatInfo      // groups returned by SearchGroups
	searchErr      error
}

type fakeCreate struct {
	name     string
	memberID string
}

type fakeEnsure struct {
	chatID string
	openID string
}

func (f *fakeChatAdmin) CreateGroup(name, memberOpenID string) (string, error) {
	f.created = append(f.created, fakeCreate{name: name, memberID: memberOpenID})
	if f.createErr != nil {
		return "", f.createErr
	}
	id := "oc_new"
	if f.nextID != "" {
		id = f.nextID
	}
	return id, f.memberAddErr
}

func (f *fakeChatAdmin) EnsureMember(chatID, openID string) error {
	f.ensureCalls = append(f.ensureCalls, fakeEnsure{chatID: chatID, openID: openID})
	return f.ensureErr
}

func (f *fakeChatAdmin) GroupExists(chatID string) bool {
	if f.groupExistsMap == nil {
		return true
	}
	return f.groupExistsMap[chatID]
}

func (f *fakeChatAdmin) SearchGroups(name string) ([]ChatInfo, error) {
	return f.searchResults, f.searchErr
}

func TestOpenRejectsGroupChat(t *testing.T) {
	ctx := newOpenContextWithMode(t, "group")
	handled, err := TryDispatch("/open", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /open = (%v, %v)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if len(fs.markdowns) != 1 || !strings.Contains(fs.markdowns[0].markdown, "direct message") {
		t.Fatalf("expected p2p-only reply, got %+v", fs.markdowns)
	}
}

func TestOpenNotConfigured(t *testing.T) {
	ctx := newOpenContextWithMode(t, "p2p")
	ctx.ChatAdmin = nil
	ctx.ChatBinds = nil
	handled, err := TryDispatch("/open", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /open = (%v, %v)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if !strings.Contains(fs.markdowns[0].markdown, "not configured") {
		t.Fatalf("expected not-configured reply, got %q", fs.markdowns[0].markdown)
	}
}

func TestOpenCreatesGroupAndBindsCwd(t *testing.T) {
	ctx, admin := newOpenContextWithModeAndAdmin(t, "p2p")
	handled, err := TryDispatch("/open", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /open = (%v, %v)", handled, err)
	}
	// Group created with the cwd basename and the sender as a member.
	if len(admin.created) != 1 {
		t.Fatalf("expected 1 CreateGroup call, got %d", len(admin.created))
	}
	cwd := ctx.Workspaces.CwdFor(ctx.Scope, "")
	base := baseName(cwd)
	if admin.created[0].name != base {
		t.Errorf("group name = %q, want %q", admin.created[0].name, base)
	}
	if admin.created[0].memberID != "ou_sender" {
		t.Errorf("member = %q, want ou_sender", admin.created[0].memberID)
	}
	// Bind recorded for the new chat id.
	b, ok := ctx.ChatBinds.Get("oc_new")
	if !ok {
		t.Fatal("expected bind for oc_new")
	}
	if b.Cwd != cwd || b.Name != base || b.CreatedBy != "ou_sender" {
		t.Errorf("bind = %+v, want cwd=%s name=%s createdBy=ou_sender", b, cwd, base)
	}
	// New scope's cwd is set to the same cwd.
	if got := ctx.Workspaces.CwdFor("oc_new", ""); got != cwd {
		t.Errorf("new scope cwd = %q, want %q", got, cwd)
	}
	// Welcome message sent to the new group, plus a confirmation in the DM.
	fs := ctx.Sender.(*fakeSender)
	var groupMsg, dmMsg string
	for _, m := range fs.markdowns {
		switch m.chatID {
		case "oc_new":
			groupMsg = m.markdown
		case "dm_1":
			dmMsg = m.markdown
		}
	}
	if groupMsg == "" {
		t.Error("expected a welcome message in the new group")
	}
	if !strings.Contains(dmMsg, "Created group") {
		t.Errorf("expected Created-group reply in DM, got %q", dmMsg)
	}
}

func TestOpenReusesExistingBind(t *testing.T) {
	ctx, admin := newOpenContextWithModeAndAdmin(t, "p2p")
	cwd := ctx.Workspaces.CwdFor(ctx.Scope, "")
	// Pre-seed a bind for the same cwd pointing at an existing group.
	if err := ctx.ChatBinds.Set("oc_existing", chatbind.Bind{Name: "myapp", Cwd: cwd}); err != nil {
		t.Fatal(err)
	}
	handled, err := TryDispatch("/open", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /open = (%v, %v)", handled, err)
	}
	// No new group created.
	if len(admin.created) != 0 {
		t.Fatalf("expected no CreateGroup on reuse, got %d", len(admin.created))
	}
	// EnsureMember called to re-add the sender.
	found := false
	for _, c := range admin.ensureCalls {
		if c.chatID == "oc_existing" && c.openID == "ou_sender" {
			found = true
		}
	}
	if !found {
		t.Error("expected EnsureMember(oc_existing, ou_sender) on reuse")
	}
	// Existing scope's cwd re-set (idempotent).
	if got := ctx.Workspaces.CwdFor("oc_existing", ""); got != cwd {
		t.Errorf("reused scope cwd = %q, want %q", got, cwd)
	}
	fs := ctx.Sender.(*fakeSender)
	if !strings.Contains(fs.markdowns[len(fs.markdowns)-1].markdown, "Reused existing group") {
		t.Errorf("expected Reused reply, got %+v", fs.markdowns)
	}
}

func TestOpenStaleBindCreatesNewGroup(t *testing.T) {
	ctx, admin := newOpenContextWithModeAndAdmin(t, "p2p")
	cwd := ctx.Workspaces.CwdFor(ctx.Scope, "")
	// Pre-seed a bind whose group no longer exists on Feishu.
	if err := ctx.ChatBinds.Set("oc_gone", chatbind.Bind{Name: "myapp", Cwd: cwd}); err != nil {
		t.Fatal(err)
	}
	admin.groupExistsMap = map[string]bool{"oc_gone": false}
	handled, err := TryDispatch("/open", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /open = (%v, %v)", handled, err)
	}
	// Stale bind should have been removed.
	if _, ok := ctx.ChatBinds.Get("oc_gone"); ok {
		t.Error("expected stale bind to be cleared")
	}
	// A new group should have been created.
	if len(admin.created) != 1 {
		t.Fatalf("expected 1 CreateGroup call, got %d", len(admin.created))
	}
	if _, ok := ctx.ChatBinds.Get("oc_new"); !ok {
		t.Error("expected new bind recorded")
	}
	fs := ctx.Sender.(*fakeSender)
	var dmMsg string
	for _, m := range fs.markdowns {
		if m.chatID == "dm_1" {
			dmMsg = m.markdown
		}
	}
	if !strings.Contains(dmMsg, "Created group") {
		t.Errorf("expected Created-group reply in DM, got %q", dmMsg)
	}
}

// TestOpenReuseEnsureMemberFailsCreatesNewGroup covers the case where the
// bound group still exists on Feishu but the caller cannot be re-added
// (e.g. missing im:chat:members:write scope). The stale bind must be
// cleared and a fresh group created so the user is not stuck with an
// invisible group.
func TestOpenReuseEnsureMemberFailsCreatesNewGroup(t *testing.T) {
	ctx, admin := newOpenContextWithModeAndAdmin(t, "p2p")
	cwd := ctx.Workspaces.CwdFor(ctx.Scope, "")
	if err := ctx.ChatBinds.Set("oc_existing", chatbind.Bind{Name: "myapp", Cwd: cwd}); err != nil {
		t.Fatal(err)
	}
	admin.ensureErr = errFakeType{}
	handled, err := TryDispatch("/open", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /open = (%v, %v)", handled, err)
	}
	// Stale bind cleared.
	if _, ok := ctx.ChatBinds.Get("oc_existing"); ok {
		t.Error("expected stale bind to be cleared when EnsureMember failed")
	}
	// A new group was created.
	if len(admin.created) != 1 {
		t.Fatalf("expected 1 CreateGroup call after ensure failure, got %d", len(admin.created))
	}
	if _, ok := ctx.ChatBinds.Get("oc_new"); !ok {
		t.Error("expected new bind recorded for oc_new")
	}
	// DM explains the fallback: first a could-not-add-back notice, then a
	// Created-group confirmation. Both are sent to the DM chat.
	fs := ctx.Sender.(*fakeSender)
	var dmMsgs []string
	for _, m := range fs.markdowns {
		if m.chatID == "dm_1" {
			dmMsgs = append(dmMsgs, m.markdown)
		}
	}
	var sawNotice, sawCreated bool
	for _, msg := range dmMsgs {
		if strings.Contains(msg, "could not add you back") {
			sawNotice = true
		}
		if strings.Contains(msg, "Created group") {
			sawCreated = true
		}
	}
	if !sawNotice {
		t.Errorf("expected could-not-add-back notice in DM, got %v", dmMsgs)
	}
	if !sawCreated {
		t.Errorf("expected Created-group reply after fallback, got %v", dmMsgs)
	}
}

// TestOpenSearchReusesExistingFeishuGroup covers the case where no local
// bind exists but a group with the target name already exists on Feishu
// (e.g. the bind record was lost or the group was created manually). The
// search-and-reuse path should find it, add the user, record the bind,
// and skip creating a new group.
func TestOpenSearchReusesExistingFeishuGroup(t *testing.T) {
	ctx, admin := newOpenContextWithModeAndAdmin(t, "p2p")
	cwd := ctx.Workspaces.CwdFor(ctx.Scope, "")
	base := baseName(cwd)
	// No local bind — but Feishu has a group with the same name.
	admin.searchResults = []ChatInfo{
		{ChatID: "oc_found", Name: base},
	}
	handled, err := TryDispatch("/open", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /open = (%v, %v)", handled, err)
	}
	// No new group created.
	if len(admin.created) != 0 {
		t.Fatalf("expected no CreateGroup call when search finds a match, got %d", len(admin.created))
	}
	// Bind recorded for the found group.
	b, ok := ctx.ChatBinds.Get("oc_found")
	if !ok {
		t.Fatal("expected bind recorded for oc_found")
	}
	if b.Cwd != cwd || b.Name != base {
		t.Errorf("bind = %+v, want cwd=%s name=%s", b, cwd, base)
	}
	// EnsureMember called to add the sender to the found group.
	found := false
	for _, c := range admin.ensureCalls {
		if c.chatID == "oc_found" && c.openID == "ou_sender" {
			found = true
		}
	}
	if !found {
		t.Error("expected EnsureMember(oc_found, ou_sender) on search reuse")
	}
	// DM says "Reused existing group".
	fs := ctx.Sender.(*fakeSender)
	var dmMsg string
	for _, m := range fs.markdowns {
		if m.chatID == "dm_1" {
			dmMsg = m.markdown
		}
	}
	if !strings.Contains(dmMsg, "Reused existing group") {
		t.Errorf("expected Reused reply, got %q", dmMsg)
	}
}

// TestOpenSearchNoMatchCreatesNew covers the case where the Feishu search
// returns no matching group — /open should fall through to creating a new
// one.
func TestOpenSearchNoMatchCreatesNew(t *testing.T) {
	ctx, admin := newOpenContextWithModeAndAdmin(t, "p2p")
	// Search returns nothing.
	admin.searchResults = nil
	handled, err := TryDispatch("/open", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /open = (%v, %v)", handled, err)
	}
	if len(admin.created) != 1 {
		t.Fatalf("expected 1 CreateGroup call when search finds no match, got %d", len(admin.created))
	}
}

func TestOpenNameDedup(t *testing.T) {
	ctx, admin := newOpenContextWithModeAndAdmin(t, "p2p")
	cwd := ctx.Workspaces.CwdFor(ctx.Scope, "")
	base := baseName(cwd)
	// Pre-seed a bind with the same base name but a different cwd, so the
	// new group must take base-2.
	otherCwd := t.TempDir()
	if err := ctx.ChatBinds.Set("oc_old", chatbind.Bind{Name: base, Cwd: otherCwd}); err != nil {
		t.Fatal(err)
	}
	handled, err := TryDispatch("/open", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /open = (%v, %v)", handled, err)
	}
	want := base + "-2"
	if admin.created[0].name != want {
		t.Errorf("deduped name = %q, want %q", admin.created[0].name, want)
	}
}

func TestOpenCreateFailureReportsPermissionHint(t *testing.T) {
	ctx, admin := newOpenContextWithModeAndAdmin(t, "p2p")
	admin.createErr = errFakeType{}
	handled, err := TryDispatch("/open", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /open = (%v, %v)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if !strings.Contains(fs.markdowns[0].markdown, "im:chat:create") {
		t.Errorf("expected permission hint, got %q", fs.markdowns[0].markdown)
	}
}

func TestOpenMemberAddFailureWarnsUser(t *testing.T) {
	ctx, admin := newOpenContextWithModeAndAdmin(t, "p2p")
	admin.memberAddErr = errFakeType{}
	handled, err := TryDispatch("/open", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /open = (%v, %v)", handled, err)
	}
	// The group was still created and the bind recorded.
	if len(admin.created) != 1 {
		t.Fatalf("expected 1 CreateGroup call, got %d", len(admin.created))
	}
	if _, ok := ctx.ChatBinds.Get("oc_new"); !ok {
		t.Fatal("expected bind recorded even when member-add failed")
	}
	// The DM reply contains a warning, not a plain "Created group" success.
	fs := ctx.Sender.(*fakeSender)
	var dmMsg string
	for _, m := range fs.markdowns {
		if m.chatID == "dm_1" {
			dmMsg = m.markdown
		}
	}
	if !strings.Contains(dmMsg, "could not add you") {
		t.Errorf("expected member-add-failure warning in DM, got %q", dmMsg)
	}
	if strings.Contains(dmMsg, "im:chat:create") {
		t.Errorf("expected member-add warning, not create-permission hint, got %q", dmMsg)
	}
}

func TestOpenNoCwdPromptsCd(t *testing.T) {
	ctx, _ := newOpenContextWithModeAndAdmin(t, "p2p")
	// Clear the preset cwd and leave no default.
	if err := ctx.Workspaces.Clear(ctx.Scope); err != nil {
		t.Fatal(err)
	}
	ctx.Config.Workspace.Default = ""
	handled, err := TryDispatch("/open", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /open = (%v, %v)", handled, err)
	}
	fs := ctx.Sender.(*fakeSender)
	if !strings.Contains(fs.markdowns[0].markdown, "/cd") {
		t.Errorf("expected /cd hint, got %q", fs.markdowns[0].markdown)
	}
}

func TestOpenInheritsProviderAndModel(t *testing.T) {
	ctx, _ := newOpenContextWithModeAndAdmin(t, "p2p")
	pr := newFakeProviderResolver()
	pr.current = "codex" // non-default
	ctx.Providers = pr
	if err := ctx.Sessions.SetModel(ctx.Scope, "gpt-5"); err != nil {
		t.Fatal(err)
	}
	handled, err := TryDispatch("/open", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /open = (%v, %v)", handled, err)
	}
	if pr.setCalls["oc_new"] != "codex" {
		t.Errorf("expected provider inherited to oc_new, got %+v", pr.setCalls)
	}
	entry, ok := ctx.Sessions.Get("oc_new")
	if !ok || entry.Model != "gpt-5" {
		t.Errorf("expected model inherited to oc_new, got %+v ok=%v", entry, ok)
	}
}

func TestOpenDoesNotInheritDefaultProvider(t *testing.T) {
	ctx, _ := newOpenContextWithModeAndAdmin(t, "p2p")
	pr := newFakeProviderResolver()
	pr.current = pr.def // default; no override should be written
	ctx.Providers = pr
	handled, err := TryDispatch("/open", ctx)
	if !handled || err != nil {
		t.Fatalf("TryDispatch /open = (%v, %v)", handled, err)
	}
	if _, ok := pr.setCalls["oc_new"]; ok {
		t.Errorf("default provider should not be written as an override: %+v", pr.setCalls)
	}
}

// --- helpers --------------------------------------------------------------

type errFakeType struct{}

func (errFakeType) Error() string { return "boom" }

// newOpenContextWithMode builds a p2p/group context with a preset cwd but
// without a ChatAdmin (used by the reject/not-configured tests).
func newOpenContextWithMode(t *testing.T, mode string) *Context {
	ctx, _ := newOpenContextWithModeAndAdmin(t, mode)
	return ctx
}

// newOpenContextWithModeAndAdmin builds a context with mode and a fake admin.
func newOpenContextWithModeAndAdmin(t *testing.T, mode string) (*Context, *fakeChatAdmin) {
	t.Helper()
	dir := t.TempDir()
	cwd := t.TempDir()
	admin := &fakeChatAdmin{}
	ctx := &Context{
		Sender:     &fakeSender{},
		ChatID:     "dm_1",
		Scope:      "dm_1",
		ChatMode:   mode,
		MessageID:  "msg_1",
		SenderID:   "ou_sender",
		Sessions:   session.New(dir),
		Workspaces: workspace.New(dir),
		ChatBinds:  chatbind.New(dir),
		ChatAdmin:  admin,
		Config: &config.Config{
			App:   config.App{ID: "x", Secret: "s"},
			Agent: config.Agent{Binary: "devin"},
		},
		Adapter: &fakeAdapter{name: "Devin"},
	}
	if err := ctx.Workspaces.SetCwd(ctx.Scope, cwd); err != nil {
		t.Fatal(err)
	}
	return ctx, admin
}

// baseName returns the last path component, mirroring filepath.Base without
// pulling filepath into the test file's imports.
func baseName(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
