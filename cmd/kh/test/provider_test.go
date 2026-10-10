package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kh/internal/config"
	"kh/internal/session"
)

func TestProviderSelectionAndLegacyResume(t *testing.T) {
	t.Setenv("KH_AUTO_REBUILD", "0")
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("KH_AGENT", "")
	t.Setenv("KH_PARENT", "")
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.Dir(), "config.json"), []byte(`{"provider":"not-installed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	state := []byte(`{"session":"legacy","map":"","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"previous question"}]}]}`)
	if err := session.Save("legacy-chat", state); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		ctx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = append(os.Environ(), "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=")
		cmd.Stdin = strings.NewReader("")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run("-s", "legacy-chat")
	if err != nil || !strings.Contains(out, "> previous question") {
		t.Fatalf("resume: %s: %v", out, err)
	}
	out, err = run("-provider", "not-installed", "-s", "legacy-chat")
	if err == nil || !strings.Contains(out, `session uses provider "codex"`) {
		t.Fatalf("mismatch: %s: %v", out, err)
	}
	out, err = run("-provider", "not-installed", "task")
	if err == nil || !strings.Contains(out, "unsupported provider") {
		t.Fatalf("unknown: %s: %v", out, err)
	}
	for _, args := range [][]string{{"sessions"}, {"--sessions"}, {"--sessions", "-i"}} {
		out, err = run(args...)
		if err != nil || !strings.Contains(out, "legacy-chat  [no assistant response yet]") {
			t.Fatalf("listing %v: %s: %v", args, out, err)
		}
	}
}

func TestSessionSaveFailureIsReportedByCLI(t *testing.T) {
	t.Setenv("KH_AUTO_REBUILD", "0")
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("KH_AGENT", "")
	t.Setenv("KH_PARENT", "")
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".kh"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A file in place of the sessions directory makes saving fail reliably,
	// even when the test runs with permissions that would bypass chmod.
	if err := os.WriteFile(filepath.Join(home, ".kh", "sessions"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	cmd := exec.CommandContext(ctx, binary, "-nosandbox", "question")
	cmd.Env = append(os.Environ(), "HOME="+home, "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=")
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "save session") {
		t.Fatalf("save failure hidden: %s: %v", out, err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".kh", "sessions"))
	if err != nil || string(b) != "keep" {
		t.Fatalf("blocked destination changed: %q: %v", b, err)
	}
}
