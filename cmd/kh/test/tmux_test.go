package test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A private tmux socket keeps this test away from the user's tmux sessions.
func TestTmuxAgents(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", b, err)
	}
	name := fmt.Sprintf("kh_test_%d", os.Getpid())
	home := t.TempDir() // spawned kh must not write to the user's real ~/.kh
	runTmux := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("tmux", append([]string{"-L", name}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %s: %v", args, b, err)
		}
		return strings.TrimSpace(string(b))
	}
	runTmux("new-session", "-d", "-s", "kh", "-n", "main", "sleep 60")
	defer runTmux("kill-server")
	socket := runTmux("display-message", "-p", "#{socket_path}")
	kh := func(args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Env = append(os.Environ(), "TMUX="+socket+",1,0", "HOME="+home)
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	if s, err := kh("agents"); err != nil || !strings.Contains(s, "kh:main  unknown") {
		t.Fatalf("agents: %q %v", s, err)
	}
	if s, err := kh("send", "kh:main", "hello from test"); err != nil {
		t.Fatalf("send: %s %v", s, err)
	}
	if s, err := kh("peek", "kh:main"); err != nil || !strings.Contains(s, "hello from test") {
		t.Fatalf("peek: %q %v", s, err)
	}
	if s, err := kh("--auto", "spawn", "docs", "hello world"); err != nil {
		t.Fatalf("spawn: %q %v", s, err)
	}
	if command := runTmux("display-message", "-p", "-t", "kh:docs", "#{pane_start_command}"); !strings.Contains(command, "--auto") {
		t.Errorf("spawn did not pass --auto: %q", command)
	}
	if s, err := kh("agents"); err != nil || !strings.Contains(s, "kh:docs") {
		t.Fatalf("agents after spawn: %q %v", s, err)
	}
	if s, err := kh("spawn", "docs", "duplicate"); err == nil {
		t.Fatalf("accepted duplicate: %q", s)
	}
	if s, err := kh("send", "kh:main", "bad\nmessage"); err == nil {
		t.Fatalf("accepted multiline: %q", s)
	}
	// A named pane stays addressable after it is joined into another window.
	runTmux("set-option", "-p", "-t", "kh:main.0", "@kh_name", "main")
	runTmux("join-pane", "-h", "-s", "kh:docs.0", "-t", "kh:main.0")
	if name := runTmux("display-message", "-p", "-t", "kh:main.1", "#{@kh_name}"); name != "docs" {
		t.Fatalf("pane lost name after join: %q", name)
	}
	if s, err := kh("agents"); err != nil || !strings.Contains(s, "kh:docs") || !strings.Contains(s, "kh:main") {
		t.Fatalf("agents after join: %q %v", s, err)
	}
	if s, err := kh("send", "kh:docs", "direct to joined pane"); err != nil {
		t.Fatalf("send after join: %q %v", s, err)
	}
	if s, err := kh("peek", "kh:docs"); err != nil || !strings.Contains(s, "direct to joined pane") {
		t.Fatalf("peek after join: %q %v", s, err)
	}
	if s, err := kh("spawn", "docs", "duplicate after join"); err == nil {
		t.Fatalf("accepted duplicate after join: %q", s)
	}
}
