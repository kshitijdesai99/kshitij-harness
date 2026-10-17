package test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutoWriteCLIOptInAndPersistence(t *testing.T) {
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
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"memory_model":"off","effort":"high","custom":"preserve"}`), 0600); err != nil {
		t.Fatal(err)
	}
	inherited := ""
	run := func(input string, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = append(os.Environ(), "HOME="+home, "KH_AUTO_REBUILD=0", "KH_AUTO_WRITE="+inherited, "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", "KH_PARENT=", "KH_AGENT=", "TMUX=", "TMUX_PANE=")
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI %v: %s %v", args, out, err)
		}
		return string(out)
	}
	read := func() map[string]any {
		t.Helper()
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		if err := json.Unmarshal(data, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if out := run("/auto-write\n/auto-write on\n/auto-write\n"); !strings.Contains(out, "auto_write off") || strings.Count(out, "auto_write on") != 2 || strings.Contains(out, "Allow session-only") {
		t.Fatalf("native opt-in: %s", out)
	}
	v := read()
	if v["auto_write"] != true || v["custom"] != "preserve" || v["effort"] != "high" {
		t.Fatalf("config lost keys: %v", v)
	}
	if _, ok := v["auto"]; ok {
		t.Fatal("folder permission changed command approval")
	}
	if _, ok := v["sandbox"]; ok {
		t.Fatal("folder permission changed sandbox")
	}
	if out := run("/auto-write\n"); !strings.Contains(out, "auto_write on") {
		t.Fatalf("policy did not persist: %s", out)
	}
	if out := run("/auto-write\n", "--auto-write=false"); !strings.Contains(out, "auto_write off") {
		t.Fatalf("explicit flag did not override config: %s", out)
	}
	if read()["auto_write"] != true {
		t.Fatal("session flag unexpectedly persisted")
	}
	inherited = "0"
	if out := run("/auto-write\n"); !strings.Contains(out, "auto_write off") {
		t.Fatalf("parent disabled policy not inherited: %s", out)
	}
	if out := run("/auto-write\n", "--auto-write"); !strings.Contains(out, "auto_write on") {
		t.Fatalf("explicit on flag did not override inherited off: %s", out)
	}
	inherited = ""
	if out := run("/auto-write invalid\n"); !strings.Contains(out, "usage: /auto-write") {
		t.Fatalf("invalid syntax: %s", out)
	}
	if read()["auto_write"] != true {
		t.Fatal("invalid command changed policy")
	}
	if out := run("/auto-write off\n"); !strings.Contains(out, "existing session grants remain") {
		t.Fatalf("disable: %s", out)
	}
	if read()["auto_write"] != false {
		t.Fatal("off did not persist")
	}
	inherited = "1"
	if out := run("/auto-write\n"); !strings.Contains(out, "auto_write on") {
		t.Fatalf("parent enabled policy not inherited: %s", out)
	}
	if out := run("/auto-write\n", "--auto-write=false"); !strings.Contains(out, "auto_write off") {
		t.Fatalf("explicit off flag did not override inherited on: %s", out)
	}
}

func TestAutoWriteReloadAfterLocalToggleAndProviderOverride(t *testing.T) {
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
	configPath := filepath.Join(dir, "config.json")
	writeConfig := func() {
		t.Helper()
		if err := os.WriteFile(configPath, []byte(`{"memory_model":"off","provider":"not-installed","auto_write":false}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--provider", "codex")
	cmd.Env = append(os.Environ(), "HOME="+home, "KH_AUTO_REBUILD=0", "KH_AUTO_WRITE=", "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", "KH_PARENT=", "KH_AGENT=", "TMUX=", "TMUX_PANE=")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	log, err := os.CreateTemp(t.TempDir(), "cli-output")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cancel()
			_ = cmd.Wait()
		}
	}()
	waitFor := func(text string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(log.Name())
			if strings.Contains(string(data), text) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		data, _ := os.ReadFile(log.Name())
		t.Fatalf("missing %q: %s", text, data)
	}
	if _, err := stdin.Write([]byte("/auto-write on\n")); err != nil {
		t.Fatal(err)
	}
	waitFor("auto_write on:")
	// Another session disables the persisted policy before this chat's first
	// normal turn. Its active provider differs from the file's provider too.
	writeConfig()
	if _, err := stdin.Write([]byte("try a turn\n")); err != nil {
		t.Fatal(err)
	}
	waitFor("error: not logged in")
	if _, err := stdin.Write([]byte("/auto-write\n")); err != nil {
		t.Fatal(err)
	}
	waitFor("auto_write off:")
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		data, _ := os.ReadFile(log.Name())
		t.Fatalf("exit: %s %v", data, err)
	}
}
