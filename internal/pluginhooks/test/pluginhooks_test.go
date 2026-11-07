package pluginhooks_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kh/internal/pluginhooks"
	"kh/internal/projectconfig"
)

func hook(command string) projectconfig.Hook {
	return projectconfig.Hook{Event: pluginhooks.SessionStart, Command: "/bin/sh", Args: []string{"-c", command}}
}
func input(t *testing.T) pluginhooks.Input {
	t.Helper()
	return pluginhooks.Input{Event: pluginhooks.SessionStart, SessionID: "abc", Cwd: t.TempDir(), Source: "startup"}
}
func TestRunnerJSONEnvironmentAndShell(t *testing.T) {
	in := input(t)
	r := pluginhooks.Runner{ConfigDir: t.TempDir()}
	h := hook(`read line; case "$line" in *'"session_id":"abc"'*) ;; *) exit 10;; esac; test "$CLAUDE_SESSION_ID" = abc && test "$CLAUDE_CODE_SESSION_ID" = abc && test "$CLAUDE_CONFIG_DIR" = "$EXPECTED" && test "$CLAUDE_PROJECT_DIR" = "$EXPECTED_PROJECT" || exit 8; printf '%s' '{"systemMessage":"notice","hookSpecificOutput":{"additionalContext":"context"}}'`)
	h.Env = map[string]string{"EXPECTED": r.ConfigDir, "EXPECTED_PROJECT": in.Cwd}
	h.Matcher = "startup"
	got := r.Run(context.Background(), []projectconfig.Hook{h}, in)
	if len(got.Diagnostics) > 0 || len(got.AdditionalContext) != 1 || got.AdditionalContext[0] != "context" || len(got.SystemMessage) != 1 {
		t.Fatalf("%+v", got)
	}
	env := r.Environment(in, "/plugin")
	if len(env) != 5 || env["CLAUDE_PLUGIN_ROOT"] != "/plugin" {
		t.Fatal(env)
	}
}
func TestRunnerBoundsAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name, cmd, want string
		timeout         time.Duration
		matcher         string
	}{
		{"timeout", "sleep 3", "timed out", 20 * time.Millisecond, ""},
		{"output", "while :; do printf 0123456789; done", "output exceeded", time.Second, ""},
		{"failure", "exit 4", "failed", time.Second, ""},
		{"invalid-json", "printf not-json", "invalid JSON", time.Second, ""},
		{"matcher", "exit 4", "invalid hook matcher", time.Second, "["},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input(t)
			h := hook(tc.cmd)
			h.Timeout = tc.timeout
			h.Matcher = tc.matcher
			start := time.Now()
			got := (pluginhooks.Runner{MaxOutput: 100}).Run(context.Background(), []projectconfig.Hook{h}, in)
			if !strings.Contains(strings.Join(got.Diagnostics, " "), tc.want) {
				t.Fatalf("%+v", got)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("not bounded")
			}
		})
	}
}
func TestCancellationAndMatching(t *testing.T) {
	in := input(t)
	h := hook("exit 9")
	h.Matcher = "resume"
	got := (pluginhooks.Runner{}).Run(context.Background(), []projectconfig.Hook{h}, in)
	if len(got.Diagnostics) > 0 {
		t.Fatal(got)
	}
	h.Matcher = ""
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got = (pluginhooks.Runner{}).Run(ctx, []projectconfig.Hook{h}, in)
	if !strings.Contains(strings.Join(got.Diagnostics, " "), "cancelled") {
		t.Fatal(got)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	h = hook("sleep 3")
	got = (pluginhooks.Runner{}).Run(ctx, []projectconfig.Hook{h}, in)
	if !strings.Contains(strings.Join(got.Diagnostics, " "), "cancelled") {
		t.Fatal(got)
	}
}
func TestNodeDiagnostic(t *testing.T) {
	in := input(t)
	t.Setenv("PATH", "")
	h := hook("exit 8")
	h.RequiresNode = true
	got := (pluginhooks.Runner{}).Run(context.Background(), []projectconfig.Hook{h}, in)
	if !strings.Contains(strings.Join(got.Diagnostics, " "), "requires Node.js") {
		t.Fatal(got)
	}
}

func TestShellPluginRootQuoting(t *testing.T) {
	in := input(t)
	h := hook(`test "$CLAUDE_PLUGIN_ROOT" = "$EXPECTED" || exit 7; printf '%s' '{"additionalContext":"safe"}'`)
	h.PluginRoot = `/a path/with"quotes$(exit 8)`
	h.Env = map[string]string{"EXPECTED": h.PluginRoot}
	got := (pluginhooks.Runner{}).Run(context.Background(), []projectconfig.Hook{h}, in)
	if len(got.Diagnostics) > 0 || len(got.AdditionalContext) != 1 {
		t.Fatal(got)
	}
}

func TestProjectionStablePrivateConcurrent(t *testing.T) {
	config, cwd := t.TempDir(), t.TempDir()
	a, err := pluginhooks.OpenTranscript(config, cwd, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := pluginhooks.OpenTranscript(config, cwd, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	if err = a.AppendUser("user-1", "actual question", at); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(a.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), `"type":"user"`) {
		t.Fatal(string(first))
	}
	if err = b.AppendUser("user-1", "actual question", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(a.Path())
	if string(again) != string(first) {
		t.Fatal("dedup rewrote identity/timestamp")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tr := a
			if i%2 == 0 {
				tr = b
			}
			if err := tr.AppendAssistant(fmt.Sprint(i), fmt.Sprintf("reply %d", i), at); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	f, err := os.Open(a.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	count := 0
	ids := map[string]bool{}
	for scan.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(scan.Bytes(), &rec); err != nil {
			t.Fatal(err)
		}
		id := rec["uuid"].(string)
		if ids[id] {
			t.Fatal("duplicate")
		}
		ids[id] = true
		count++
		if rec["type"] == "assistant" {
			blocks := rec["message"].(map[string]any)["content"].([]any)
			if blocks[0].(map[string]any)["type"] != "text" {
				t.Fatal(rec)
			}
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 21 {
		t.Fatal(count)
	}
	for path, mode := range map[string]os.FileMode{a.Path(): 0600, filepath.Dir(a.Path()): 0700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s mode %v err %v", path, info, err)
		}
	}
	if a.ConfigDir() != config {
		t.Fatal(a.ConfigDir())
	}
}
func TestProjectionRejectsUnsafePaths(t *testing.T) {
	config, cwd := t.TempDir(), t.TempDir()
	if _, err := pluginhooks.OpenTranscript(config, cwd, "../evil"); err == nil {
		t.Fatal("accepted traversal")
	}
	target := t.TempDir()
	link := filepath.Join(config, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginhooks.OpenTranscript(link, cwd, "session"); err == nil {
		t.Fatal("accepted config symlink")
	}
	tr, err := pluginhooks.OpenTranscript(config, cwd, "safe")
	if err != nil {
		t.Fatal(err)
	}
	if err = tr.AppendUser("", "text", time.Now()); err == nil {
		t.Fatal("missing receipt")
	}
	if err = os.Remove(tr.Path()); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(target, "file"), tr.Path()); err != nil {
		t.Fatal(err)
	}
	if err = tr.AppendUser("id", "text", time.Now()); err == nil {
		t.Fatal("accepted file symlink")
	}
}
