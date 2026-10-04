package test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"kh/internal/config"
	"kh/internal/session"
)

// This intentionally reproduces only the historical layout to create fixtures.
// New locations are always obtained from the public Folder accessor.
func legacyFolder(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	wd, err = filepath.EvalSymlinks(wd)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(config.Dir(), "sessions", strings.ReplaceAll(wd, string(filepath.Separator), "-"))
}

func writeChat(t *testing.T, folder, id, text string, saved time.Time) string {
	t.Helper()
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(folder, id+".json")
	if err := os.WriteFile(name, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(name, saved, saved); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestCollidingLegacyFolderKeysHaveIndependentNewSaves(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	a := filepath.Join(root, "a-b")
	b := filepath.Join(root, "a", "b")
	for _, folder := range []string{a, b} {
		if err := os.MkdirAll(folder, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(a)
	oldFolder := legacyFolder(t)
	newA := session.Folder()
	legacy := writeChat(t, oldFolder, "legacy", "ambiguous old chat", time.Unix(100, 0))
	if err := session.Save("same", []byte("A")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(b)
	if legacyFolder(t) != oldFolder {
		t.Fatal("fixture does not collide under the historical key")
	}
	if session.Folder() == newA {
		t.Fatal("different absolute folders share the new namespace")
	}
	if _, err := session.Load("same"); !os.IsNotExist(err) {
		t.Fatalf("folder B saw folder A's new save: %v", err)
	}
	if err := session.Save("same", []byte("B")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ folder, want string }{{a, "A"}, {b, "B"}} {
		t.Chdir(tc.folder)
		got, err := session.Load("same")
		if err != nil || string(got) != tc.want {
			t.Fatalf("load in %s = %q, %v", tc.folder, got, err)
		}
		// The legacy key's original ownership cannot be reconstructed: both
		// folders retain fallback access rather than moving or deleting it.
		got, err = session.Load("legacy")
		if err != nil || string(got) != "ambiguous old chat" {
			t.Fatalf("legacy fallback = %q, %v", got, err)
		}
	}
	if got, err := os.ReadFile(legacy); err != nil || string(got) != "ambiguous old chat" {
		t.Fatalf("legacy changed: %q, %v", got, err)
	}
}

func TestSymlinkFolderUsesCanonicalNamespace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	real := filepath.Join(root, "real")
	alias := filepath.Join(root, "alias")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Chdir(real)
	folder := session.Folder()
	oldFolder := legacyFolder(t)
	writeChat(t, oldFolder, "old", "legacy", time.Unix(100, 0))
	if err := session.Save("chat", []byte("canonical")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(alias)
	t.Setenv("PWD", alias)
	if got := session.Folder(); got != folder {
		t.Fatalf("symlink namespace = %s, want %s", got, folder)
	}
	for id, want := range map[string]string{"chat": "canonical", "old": "legacy"} {
		got, err := session.Load(id)
		if err != nil || string(got) != want {
			t.Fatalf("symlink load %s = %q, %v", id, got, err)
		}
	}
	if err := session.Save("chat", []byte("through alias")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(real)
	if got, err := session.Load("chat"); err != nil || string(got) != "through alias" {
		t.Fatalf("real load = %q, %v", got, err)
	}
}

func TestLegacyListRecentLatestFallbackAndResave(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	oldFolder := legacyFolder(t)
	older, middle, newer := time.Unix(100, 0), time.Unix(200, 0), time.Unix(300, 0)
	legacyA := writeChat(t, oldFolder, "a", "legacy A", newer)
	legacyM := writeChat(t, oldFolder, "m", "legacy M", newer.Add(time.Hour))
	writeChat(t, oldFolder, "z", "legacy Z", middle)
	if got, err := session.Latest(); err != nil || got != "z" {
		t.Fatalf("legacy latest = %q, %v", got, err)
	}
	if got := session.Recent(); len(got) != 3 || got[0].ID != "m" || !got[0].SavedAt.Equal(newer.Add(time.Hour)) {
		t.Fatalf("legacy recent = %v", got)
	}
	writeChat(t, session.Folder(), "m", "new M", older)
	if err := os.WriteFile(filepath.Join(oldFolder, ".save-abandoned"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(oldFolder, "ignored.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := session.List(); !reflect.DeepEqual(got, []string{"a", "m", "z"}) {
		t.Fatalf("merged list = %v", got)
	}
	if got, err := session.Load("m"); err != nil || string(got) != "new M" {
		t.Fatalf("duplicate load = %q, %v", got, err)
	}
	wantRecent := []session.Entry{{ID: "a", SavedAt: newer}, {ID: "z", SavedAt: middle}, {ID: "m", SavedAt: older}}
	if got := session.Recent(); !reflect.DeepEqual(got, wantRecent) {
		t.Fatalf("merged recent = %v, want %v", got, wantRecent)
	}
	if got, err := session.Latest(); err != nil || got != "z" {
		t.Fatalf("merged latest = %q, %v", got, err)
	}
	if err := session.Save("a", []byte("resaved A")); err != nil {
		t.Fatal(err)
	}
	if got, err := session.Load("a"); err != nil || string(got) != "resaved A" {
		t.Fatalf("resave load = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(session.Folder(), "a.json")); err != nil || string(got) != "resaved A" {
		t.Fatalf("resave not in new namespace: %q, %v", got, err)
	}
	for name, want := range map[string]string{legacyA: "legacy A", legacyM: "legacy M"} {
		if got, err := os.ReadFile(name); err != nil || string(got) != want {
			t.Fatalf("legacy modified: %q, %v", got, err)
		}
	}
	if info, err := os.Stat(legacyA); err != nil || !info.ModTime().Equal(newer) {
		t.Fatalf("legacy modification time changed: %v, %v", info, err)
	}
}

func TestLegacyFallbackDoesNotMaskNewReadErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	writeChat(t, legacyFolder(t), "chat", "legacy", time.Unix(100, 0))
	if err := os.MkdirAll(filepath.Join(session.Folder(), "chat.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Load("chat"); err == nil {
		t.Fatal("new namespace read error masked by legacy fallback")
	}
}
