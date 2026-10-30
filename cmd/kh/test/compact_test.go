package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kh/internal/session"
)

func TestCompactCommandProviderFailureReportsUnchanged(t *testing.T) {
	t.Setenv("KH_AUTO_REBUILD", "0")
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	isolateCLIFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".kh"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".kh", "config.json"), []byte(`{"memory_model":"off"}`), 0600); err != nil {
		t.Fatal(err)
	}
	state, err := session.Encode("codex", []byte(`{"session":"failed","map":"","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Keep the previous conversation."}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Save("compact-failure", state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-s", "compact-failure")
	cmd.Env = append(os.Environ(), "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", "TMUX=", "TMUX_PANE=")
	cmd.Stdin = strings.NewReader("/compact\n/help\n")
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil || !strings.Contains(text, "(compacting context... Ctrl-C to cancel)") || !strings.Contains(text, "compact: failed; conversation unchanged: not logged in") || strings.Contains(text, "context compacted and session saved") || !strings.Contains(text, "/compact   summarize context") {
		t.Fatalf("failure feedback: %s %v", text, err)
	}
	if strings.Index(text, "(compacting context...") > strings.Index(text, "compact: failed;") {
		t.Fatalf("start not shown before failure: %s", text)
	}
	after, err := session.Load("compact-failure")
	if err != nil || string(after) != string(state) {
		t.Fatalf("failed command changed saved history: %s %v", after, err)
	}
}

func TestCompactCommandEmptyHistory(t *testing.T) {
	t.Setenv("KH_AUTO_REBUILD", "0")
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	isolateCLIFixture(t)
	t.Setenv("HOME", t.TempDir())
	state, err := session.Encode("codex", []byte(`{"session":"empty","map":"","input":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Save("compact-empty", state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-s", "compact-empty")
	cmd.Env = append(os.Environ(), "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", "TMUX=", "TMUX_PANE=")
	cmd.Stdin = strings.NewReader("/compact\n/help\n")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "compacting context...") || !strings.Contains(string(out), "compact: failed; conversation unchanged: nothing to compact") || !strings.Contains(string(out), "/compact   summarize context") {
		t.Fatalf("command: %s: %v", out, err)
	}
	after, err := session.Load("compact-empty")
	if err != nil || string(after) != string(state) {
		t.Fatalf("failed compact changed saved state: %s: %v", after, err)
	}
}
