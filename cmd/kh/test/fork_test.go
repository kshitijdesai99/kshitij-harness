package test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kh/internal/session"
)

// /fork copies the chat into a new session and opens it in a new window
// directly to the right of the current one, which becomes active.
func TestForkOpensNewWindowToTheRight(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", b, err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	state, err := session.Encode("codex", []byte(`{"session":"s","map":"","input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"count files"}]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"There are 3 files."}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Save("original", state); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	name := fmt.Sprintf("kh_fork_%d", os.Getpid())
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
	runTmux("new-session", "-d", "-s", "kh", "-n", "main", "-x", "200", "-y", "50", "-c", cwd,
		"env KH_PROVIDER= KH_MODEL= KH_EFFORT= KH_AGENT= KH_PARENT= "+binary+" -s original")
	defer runTmux("kill-server")
	runTmux("new-window", "-d", "-t", "kh:1", "-n", "other", "sleep 60")
	waitFor(t, func() bool {
		return strings.Contains(runTmux("capture-pane", "-p", "-t", "kh:main"), "There are 3 files.")
	})

	runTmux("send-keys", "-t", "kh:main", "/fork", "Enter")
	waitFor(t, func() bool {
		return strings.Contains(runTmux("list-windows", "-t", "kh", "-F", "#{window_name}"), "fork")
	})

	if windows := runTmux("list-windows", "-t", "kh", "-F", "#{window_name}"); windows != "main\nfork\nother" {
		t.Fatalf("fork should sit right after the current window: %q", windows)
	}
	if active := runTmux("display-message", "-p", "-t", "kh", "#{window_name}"); active != "fork" {
		t.Fatalf("fork window not selected: %q", active)
	}
	command := runTmux("display-message", "-p", "-t", "kh:fork", "#{pane_start_command}")
	id := command[strings.LastIndex(command, "'-s' '")+6:]
	id = strings.TrimSuffix(strings.TrimSuffix(id, `"`), "'")
	copied, err := session.Load(id)
	original, _ := session.Load("original") // re-saved by /fork just before copying
	if err != nil || string(copied) != string(original) || !strings.Contains(string(copied), "There are 3 files.") {
		t.Fatalf("session %q not copied: %v", id, err)
	}
	// The fork replays the chat and ends at its last reply, waiting for input.
	waitFor(t, func() bool {
		screen := runTmux("capture-pane", "-p", "-t", "kh:fork")
		return strings.Contains(screen, "There are 3 files.") && strings.Contains(screen, "> count files")
	})
	if name := runTmux("display-message", "-p", "-t", "kh:fork", "#{@kh_name}"); name != "fork" {
		t.Fatalf("fork address: %q", name)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timed out")
}
