package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHelpCLIBypassesChatAndSessions(t *testing.T) {
	t.Setenv("KH_AUTO_REBUILD", "0")
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	home := t.TempDir()
	dir := filepath.Join(home, ".kh")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfg, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = append(os.Environ(), "HOME="+home, "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", "TMUX=must-not-use-tmux", "KH_PARENT=", "KH_AGENT=")
		cmd.Stdin = strings.NewReader("")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for _, flag := range []string{"--help", "-h"} {
		out, err := run(flag)
		if err != nil || !strings.Contains(out, "one-off question") {
			t.Fatalf("usage %s: %s %v", flag, out, err)
		}
	}
	if err := os.WriteFile(cfg, []byte(`{"provider":"not-installed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := run("--help", "sessions")
	if err == nil || !strings.Contains(out, "unsupported provider") {
		t.Fatalf("request treated as subcommand: %s %v", out, err)
	}
	for _, args := range [][]string{{"--help", "-r", "question"}, {"--help", "-s", "old", "question"}, {"--help", "-i", "question"}, {"--help", "--sessions", "question"}, {"--help", "--rebuild", "question"}} {
		out, err := run(args...)
		if err == nil || !strings.Contains(out, "cannot be combined") && !strings.Contains(out, "takes no other flags") {
			t.Fatalf("conflict %v: %s %v", args, out, err)
		}
	}
	out, err = run("--help", " ")
	if err == nil || !strings.Contains(out, "non-empty request") {
		t.Fatalf("blank: %s %v", out, err)
	}
	if err := os.WriteFile(cfg, []byte(`{"provider":"codex"}`), 0600); err != nil {
		t.Fatal(err)
	}
	out, err = run("--help", "explain rebase")
	if err == nil || !strings.Contains(out, "not logged in") || strings.Contains(out, "kh chat,") || strings.Contains(out, "kh -r to continue") {
		t.Fatalf("unauthenticated request: %s %v", out, err)
	}
	for _, name := range []string{"sessions", "memory.db"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("help created %s: %v", name, err)
		}
	}
}
