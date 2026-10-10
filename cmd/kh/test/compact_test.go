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

func TestCompactCommandEmptyHistory(t *testing.T) {
	t.Setenv("KH_AUTO_REBUILD", "0")
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
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
	if err != nil || !strings.Contains(string(out), "compact: nothing to compact") || !strings.Contains(string(out), "/compact   summarize context") {
		t.Fatalf("command: %s: %v", out, err)
	}
	after, err := session.Load("compact-empty")
	if err != nil || string(after) != string(state) {
		t.Fatalf("failed compact changed saved state: %s: %v", after, err)
	}
}
