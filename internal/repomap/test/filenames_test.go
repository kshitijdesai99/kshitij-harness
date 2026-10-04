package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"kh/internal/repomap"
)

func TestMapPreservesGitFilenames(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "core.quotepath", "true")
	names := []struct{ name, display string }{
		{"ordinary.go", "ordinary.go"},
		{"two words.go", `"two words.go"`},
		{"tab\tname.go", `"tab\tname.go"`},
		{"line\nbreak.go", `"line\nbreak.go"`},
		{"quote\"name.go", `"quote\"name.go"`},
		{"single'quote.go", `"single'quote.go"`},
		{"back\\slash.go", `"back\\slash.go"`},
		{"日本語.go", "日本語.go"},
		{"emoji-😀.go", "emoji-😀.go"},
		{" leading.go", `" leading.go"`},
		{"trailing .go", `"trailing .go"`},
		{"nested dir/雪 file.go", `"nested dir/雪 file.go"`},
		{"-option.go", "-option.go"},
		{"notes with spaces.txt", `"notes with spaces.txt"`},
		{"carriage\rreturn.go", `"carriage\rreturn.go"`},
		{"escape\x1b.go", `"escape\x1b.go"`},
		{"unicode\u00a0space.go", `"unicode\u00a0space.go"`},
		{"unicode\u2028separator.go", `"unicode\u2028separator.go"`},
	}
	wantLen := 0
	for i, tc := range names {
		name := tc.name
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package example\ntype Widget struct{}\nfunc (*Widget) Step() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			git("add", "--", name)
		}
		line := tc.display + "\n"
		if strings.HasSuffix(name, ".go") {
			line = tc.display + ": Widget Widget.Step\n"
		}
		wantLen += len(line)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignored.go"), []byte("package ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantLen += len(".gitignore\n")
	m := repomap.Build(dir, 10000)
	for _, tc := range names {
		want := tc.display + "\n"
		if strings.HasSuffix(tc.name, ".go") {
			want = tc.display + ": Widget Widget.Step\n"
		}
		if !strings.Contains(m, want) {
			t.Errorf("map missing displayed filename and symbols %q: %q", want, m)
		}
	}
	if lines := strings.Count(m, "\n"); lines != len(names)+1 {
		t.Errorf("map has %d lines, want one per file (%d): %q", lines, len(names)+1, m)
	}
	if len(m) != wantLen || strings.Contains(m, "ignored.go") {
		t.Errorf("unexpected entries in map (got %d bytes, want %d): %q", len(m), wantLen, m)
	}
}
