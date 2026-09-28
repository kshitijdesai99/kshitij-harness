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
		"internal/tools/bash.go: writeFlags askMu Bash",
		"internal/tools/edit.go: Edit",
		"internal/repomap/test/self_test.go: TestSelf", // untracked files are listed
		"internal/provider/codex.go:",
		"Codex.Step",
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

// Outside git the map walks the dir, and still stops at the cap.
func TestWalkOutsideGit(t *testing.T) {
	d := t.TempDir()
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		os.WriteFile(filepath.Join(d, n), nil, 0o644)
	}
	if m := repomap.Build(d, 1000); m != "a.txt\nb.txt\nc.txt\n" {
		t.Errorf("got %q", m)
	}
	if m := repomap.Build(d, 8); strings.Count(m, ".txt") > 1 {
		t.Errorf("walk ignored cap: %q", m)
	}
}
