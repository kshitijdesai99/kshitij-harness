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

func TestAutoCLIOptInAndPersistence(t *testing.T) {
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
	isolateCLIFixture(t)
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"memory_model":"off","effort":"high","custom":"preserve","auto":false,"auto_write":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{"--nosandbox", "--auto-write=false"} {
		cmd := exec.Command(binary, retired)
		cmd.Stdin = strings.NewReader("")
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "flag provided but not defined") {
			t.Fatalf("retired flag %s accepted: %s %v", retired, out, err)
		}
	}
	inherited := ""
	run := func(input string, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = append(os.Environ(), "HOME="+home, "KH_AUTO_REBUILD=0", "KH_AUTO="+inherited, "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", "KH_PARENT=", "KH_AGENT=", "TMUX=", "TMUX_PANE=")
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
	if out := run("/auto\n/auto on\n/auto\n"); !strings.Contains(out, "auto off") || strings.Count(out, "auto on") != 2 || strings.Contains(out, "Allow session-only") {
		t.Fatalf("native opt-in: %s", out)
	}
	v := read()
	if v["auto"] != true || v["custom"] != "preserve" || v["effort"] != "high" {
		t.Fatalf("config lost keys: %v", v)
	}
	if _, ok := v["auto_write"]; ok {
		t.Fatal("/auto persistence retained legacy auto_write")
	}
	if _, ok := v["sandbox"]; ok {
		t.Fatal("retired sandbox key persisted")
	}
	if out := run("/auto\n"); !strings.Contains(out, "auto on") {
		t.Fatalf("policy did not persist: %s", out)
	}
	if out := run("/auto\n", "--auto=false"); !strings.Contains(out, "auto off") {
		t.Fatalf("explicit flag did not override config: %s", out)
	}
	if read()["auto"] != true {
		t.Fatal("session flag unexpectedly persisted")
	}
	inherited = "0"
	if out := run("/auto\n"); !strings.Contains(out, "auto off") {
		t.Fatalf("parent disabled policy not inherited: %s", out)
	}
	if out := run("/auto\n", "--auto"); !strings.Contains(out, "auto on") {
		t.Fatalf("explicit on flag did not override inherited off: %s", out)
	}
	inherited = ""
	if out := run("/auto invalid\n"); !strings.Contains(out, "usage: /auto") {
		t.Fatalf("invalid syntax: %s", out)
	}
	if read()["auto"] != true {
		t.Fatal("invalid command changed policy")
	}
	if out := run("/auto off\n"); !strings.Contains(out, "commands require approval") {
		t.Fatalf("disable: %s", out)
	}
	if read()["auto"] != false {
		t.Fatal("off did not persist")
	}
	inherited = "1"
	if out := run("/auto\n"); !strings.Contains(out, "auto on") {
		t.Fatalf("parent enabled policy not inherited: %s", out)
	}
	if out := run("/auto\n", "--auto=false"); !strings.Contains(out, "auto off") {
		t.Fatalf("explicit off flag did not override inherited on: %s", out)
	}
}

func TestAutoReloadAfterLocalToggleAndProviderOverride(t *testing.T) {
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
	isolateCLIFixture(t)
	configPath := filepath.Join(dir, "config.json")
	writeConfig := func() {
		t.Helper()
		if err := os.WriteFile(configPath, []byte(`{"memory_model":"off","provider":"not-installed","auto":false}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--provider", "codex")
	cmd.Env = append(os.Environ(), "HOME="+home, "KH_AUTO_REBUILD=0", "KH_AUTO=", "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", "KH_PARENT=", "KH_AGENT=", "TMUX=", "TMUX_PANE=")
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
	if _, err := stdin.Write([]byte("/auto on\n")); err != nil {
		t.Fatal(err)
	}
	waitFor("auto on:")
	// Another session disables the persisted policy before this chat's first
	// normal turn. Its active provider differs from the file's provider too.
	writeConfig()
	if _, err := stdin.Write([]byte("try a turn\n")); err != nil {
		t.Fatal(err)
	}
	waitFor("error: not logged in")
	if _, err := stdin.Write([]byte("/auto\n")); err != nil {
		t.Fatal(err)
	}
	waitFor("auto off:")
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		data, _ := os.ReadFile(log.Name())
		t.Fatalf("exit: %s %v", data, err)
	}
}
