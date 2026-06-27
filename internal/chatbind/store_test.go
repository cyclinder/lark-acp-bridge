package chatbind

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSetGet(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Set("oc_1", Bind{Name: "myapp", Cwd: "/home/me/myapp", CreatedBy: "ou_1"}); err != nil {
		t.Fatal(err)
	}
	b, ok := s.Get("oc_1")
	if !ok {
		t.Fatal("expected bind for oc_1")
	}
	if b.Name != "myapp" || b.Cwd != "/home/me/myapp" || b.CreatedBy != "ou_1" {
		t.Errorf("unexpected bind: %+v", b)
	}
	if _, ok := s.Get("missing"); ok {
		t.Error("Get should return ok=false for unknown chatID")
	}
}

func TestStoreFindByCwd(t *testing.T) {
	s := New(t.TempDir())
	_ = s.Set("oc_1", Bind{Name: "myapp", Cwd: "/home/me/myapp"})
	_ = s.Set("oc_2", Bind{Name: "other", Cwd: "/home/me/other"})

	id, b, ok := s.FindByCwd("/home/me/myapp")
	if !ok {
		t.Fatal("expected FindByCwd to hit")
	}
	if id != "oc_1" || b.Name != "myapp" {
		t.Errorf("FindByCwd = %q %+v, want oc_1 myapp", id, b)
	}
	if _, _, ok := s.FindByCwd("/nope"); ok {
		t.Error("FindByCwd should miss for unknown cwd")
	}
}

func TestStoreFindByName(t *testing.T) {
	s := New(t.TempDir())
	_ = s.Set("oc_1", Bind{Name: "myapp-2", Cwd: "/x"})
	id, b, ok := s.FindByName("myapp-2")
	if !ok || id != "oc_1" || b.Cwd != "/x" {
		t.Fatalf("FindByName = %q %+v ok=%v", id, b, ok)
	}
	if _, _, ok := s.FindByName("nope"); ok {
		t.Error("FindByName should miss for unknown name")
	}
}

func TestStoreListSorted(t *testing.T) {
	s := New(t.TempDir())
	_ = s.Set("oc_b", Bind{Name: "b", Cwd: "/b"})
	_ = s.Set("oc_a", Bind{Name: "a", Cwd: "/a"})
	items := s.List()
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].ChatID != "oc_a" || items[1].ChatID != "oc_b" {
		t.Errorf("List not sorted by chatID: %+v", items)
	}
}

func TestStoreClear(t *testing.T) {
	s := New(t.TempDir())
	_ = s.Set("oc_1", Bind{Name: "x", Cwd: "/x"})
	if err := s.Clear("oc_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("oc_1"); ok {
		t.Error("Clear should remove the bind")
	}
}

func TestStoreLoadPersists(t *testing.T) {
	dir := t.TempDir()
	s1 := New(dir)
	_ = s1.Set("oc_1", Bind{Name: "myapp", Cwd: "/home/me/myapp", CreatedBy: "ou_1", CreatedAt: 123})
	// File should exist on disk.
	if _, err := os.Stat(filepath.Join(dir, "chatbinds.json")); err != nil {
		t.Fatalf("expected chatbinds.json on disk: %v", err)
	}
	s2 := New(dir)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	b, ok := s2.Get("oc_1")
	if !ok || b.Name != "myapp" || b.CreatedAt != 123 {
		t.Errorf("Load did not restore bind: %+v ok=%v", b, ok)
	}
}

func TestStoreLoadMissingFile(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Load(); err != nil {
		t.Errorf("Load on missing file should be nil, got %v", err)
	}
}
