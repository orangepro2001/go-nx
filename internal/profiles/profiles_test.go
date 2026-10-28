package profiles

import (
	"os"
	"path/filepath"
	"testing"
)

func open(t *testing.T) (*List, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", "connections.json")
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return l, path
}

func TestPersistAndReload(t *testing.T) {
	l, path := open(t)
	if len(l.Items) != 0 {
		t.Fatal("new list not empty")
	}
	l.Upsert(Profile{Name: "Line 1", Addr: "10.0.0.5:7000", Password: "pw"})
	l.Upsert(Profile{Name: "Line 2", Addr: "10.0.0.6"})
	l2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(l2.Items) != 2 || l2.Items[0].Password != "pw" || l2.Items[1].Addr != "10.0.0.6" {
		t.Fatalf("reloaded = %+v", l2.Items)
	}
}

func TestUpsertReplacesSameName(t *testing.T) {
	l, _ := open(t)
	l.Upsert(Profile{Name: "A", Addr: "1"})
	l.Upsert(Profile{Name: "A", Addr: "2"})
	if len(l.Items) != 1 || l.Items[0].Addr != "2" {
		t.Fatalf("items = %+v", l.Items)
	}
}

func TestDelete(t *testing.T) {
	l, path := open(t)
	l.Upsert(Profile{Name: "A"})
	l.Upsert(Profile{Name: "B"})
	l.Delete(0)
	l.Delete(9) // out of range is ignored
	l2, _ := Load(path)
	if len(l2.Items) != 1 || l2.Items[0].Name != "B" {
		t.Fatalf("items = %+v", l2.Items)
	}
}

func TestRenameKeepsNamesUnique(t *testing.T) {
	l, path := open(t)
	l.Upsert(Profile{Name: "A", Addr: "1"})
	l.Upsert(Profile{Name: "B", Addr: "2"})
	if got, _ := l.Rename("A", "B"); got != "B (2)" {
		t.Fatalf("collision renamed to %q", got)
	}
	if got, _ := l.Rename("B (2)", "Press 3"); got != "Press 3" {
		t.Fatalf("renamed to %q", got)
	}
	if got, _ := l.Rename("nope", "X"); got != "nope" {
		t.Fatalf("renaming a missing profile returned %q", got)
	}
	if got, _ := l.Rename("B", ""); got != "B" {
		t.Fatalf("empty name accepted: %q", got)
	}
	l2, _ := Load(path)
	if l2.Items[0].Name != "Press 3" || l2.Items[0].Addr != "1" || l2.Items[1].Name != "B" {
		t.Fatalf("items = %+v", l2.Items)
	}
}

func TestLoadRecoveringSetsCorruptFileAside(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "connections.json")
	if err := os.WriteFile(p, []byte(`[{"name":"a"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("strict Load accepted a corrupt file")
	}
	l, backup, err := LoadRecovering(p)
	if err != nil || backup == "" || len(l.Items) != 0 {
		t.Fatalf("recovering = %d items, backup %q, err %v", len(l.Items), backup, err)
	}
	if err := l.Upsert(Profile{Name: "b", Addr: "1.2.3.4:7000"}); err != nil {
		t.Fatal(err)
	}
	again, backup2, err := LoadRecovering(p)
	if err != nil || backup2 != "" || len(again.Items) != 1 {
		t.Fatalf("reopen = %d items, backup %q, err %v", len(again.Items), backup2, err)
	}
}
