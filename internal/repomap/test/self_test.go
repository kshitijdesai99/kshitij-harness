package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kh/internal/repomap"
)

// Maps this repo and checks a few known symbols show up in the right place.
func TestSelf(t *testing.T) {
	m := repomap.Build("../../..", 20000)
	for _, want := range []string{
		"internal/tools/bash.go: ", "isSafe",
		"internal/tools/edit.go: Edit",
		"internal/repomap/test/self_test.go: TestSelf", // untracked files are listed
		"internal/provider/codex/client.go:",
		"Client.Step",
		"README.md\n",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("map missing %q:\n%s", want, m)
		}
	}
	if len(repomap.Build("../../..", 50)) > 70 {
		t.Error("cap not applied")
	}
	if repomap.Build("../../..", 0) != "" {
		t.Error("map_cap 0 should turn the map off")
	}
}

// Outside a git project there is no map.
func TestNoMapOutsideGit(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "a.go"), []byte("package a\nfunc A() {}\n"), 0o644)
	if m := repomap.Build(d, 1000); m != "" {
		t.Errorf("got %q", m)
	}
}
