package test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPluginLifecycleCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	home, cwd := t.TempDir(), t.TempDir()
	cwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	projectCLIWrite(t, filepath.Join(home, ".kh", "config.json"), `{"auto":true,"memory_model":"off","memory_hybrid":false,"web_search":false,"map_cap":0}`)
	plugin := filepath.Join(cwd, ".agents", "plugins", "cogit")
	projectCLIWrite(t, filepath.Join(plugin, ".claude-plugin", "plugin.json"), `{"name":"cogit"}`)
	projectCLIWrite(t, filepath.Join(cwd, ".agents", "settings.json"), `{"enabledPlugins":{"cogit":true}}`)
	projectCLIWrite(t, filepath.Join(plugin, "skills", "record", "SKILL.md"), "---\nname: record\ndescription: Fake record skill\n---\nPLUGIN-CONTINUE-CONTEXT Use Write for output. Session data: !`node ${CLAUDE_PLUGIN_ROOT}/bin/cli.js session show --id ${CLAUDE_SESSION_ID}`")
	marker := filepath.Join(cwd, "events.jsonl")
	hooks := map[string]any{}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "Stop", "PreToolUse", "SessionEnd"} {
		script := fmt.Sprintf("cat >> %q; printf '\\n' >> %q; printf '%%s' '{}'", marker, marker)
		if event == "Stop" {
			script = fmt.Sprintf("cat >> %q; printf '\\n' >> %q; printf '%%s' '{\"systemMessage\":\"/cogit:record\"}'", marker, marker)
		}
		hooks[event] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": script}}}}
	}
	data, _ := json.Marshal(map[string]any{"hooks": hooks})
	projectCLIWrite(t, filepath.Join(plugin, "hooks", "hooks.json"), string(data))
	run := func(proxy string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "HOME="+home, "KH_AUTO_REBUILD=0", "KH_AUTO=", "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", "KH_PARENT=", "KH_AGENT=", "TMUX=", "TMUX_PANE=", "HTTPS_PROXY="+proxy, "HTTP_PROXY="+proxy, "ALL_PROXY=", "NO_PROXY=", "SSL_CERT_FILE="+projectCLIProxyCA(home, proxy), "SSL_CERT_DIR="+filepath.Dir(projectCLIProxyCA(home, proxy)), "GODEBUG=x509sslcertoverrideplatform=1")
		cmd.Stdin = strings.NewReader("")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	listed, err := run("", "plugins", "list")
	if err != nil {
		t.Fatalf("list: %s %v", listed, err)
	}
	fields := strings.Fields(listed)
	if len(fields) < 3 || !strings.Contains(listed, "trusted=false") {
		t.Fatalf("listing=%s", listed)
	}
	id := fields[0]
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("discovery executed hooks")
	}
	if _, err := os.Stat(filepath.Join(home, ".kh", "plugin-transcripts")); !os.IsNotExist(err) {
		t.Fatal("discovery created projection")
	}
	if out, err := run("", "plugins", "trust", id); err != nil {
		t.Fatalf("trust: %s %v", out, err)
	}
	proxy, requests := projectCLIProxy(t, home, false)
	if out, err := run(proxy, "-p", "actual human"); err != nil {
		t.Fatalf("turn: %s %v", out, err)
	}
	first, second := <-requests, <-requests
	b1, _ := json.Marshal(first.Input)
	b2, _ := json.Marshal(second.Input)
	if strings.Contains(string(b1), "PLUGIN-CONTINUE-CONTEXT") || !strings.Contains(string(b2), "PLUGIN-CONTINUE-CONTEXT") {
		t.Fatalf("continuation context first=%s second=%s", b1, b2)
	}
	select {
	case extra := <-requests:
		t.Fatalf("unbounded continuation: %+v", extra)
	default:
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	var active []bool
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var in struct {
			Event  string `json:"hook_event_name"`
			Active bool   `json:"stop_hook_active"`
		}
		if err := json.Unmarshal([]byte(line), &in); err != nil {
			t.Fatalf("event %s: %v", line, err)
		}
		events = append(events, in.Event)
		if in.Event == "Stop" {
			active = append(active, in.Active)
		}
	}
	if fmt.Sprint(events) != "[SessionStart UserPromptSubmit Stop Stop SessionEnd]" || fmt.Sprint(active) != "[false true]" {
		t.Fatalf("events=%v active=%v", events, active)
	}
	paths, err := filepath.Glob(filepath.Join(home, ".kh", "plugin-transcripts", "config", "projects", "*", "*.jsonl"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("projection paths=%v %v", paths, err)
	}
	projection, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(projection), `"type":"user"`) != 1 || strings.Count(string(projection), `"type":"assistant"`) != 2 || strings.Contains(string(projection), "PLUGIN-CONTINUE-CONTEXT") {
		t.Fatalf("projection=%s", projection)
	}
	if strings.Contains(string(b2), "!`node") || !strings.Contains(string(b2), "native edit tool") || !strings.Contains(string(b2), "Explicitly invoke the registered bash tool") {
		t.Fatalf("Cogit skill was not adapted inertly: %s", b2)
	}
	// Facade invocation reaches the same approved edit boundary exactly once.
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	editArgs, _ := json.Marshal(map[string]any{"name": "edit", "arguments": map[string]string{"path": filepath.Join(cwd, "facade-created.txt"), "old": "", "new": "created"}})
	editProxy, editRequests := projectCLIProxyScript(t, home, func(step int) *projectCLICall {
		if step == 0 {
			return &projectCLICall{Name: "tool_invoke", Arguments: string(editArgs)}
		}
		return nil
	})
	if out, err := run(editProxy, "-p", "create file through facade"); err != nil {
		t.Fatalf("facade edit: %s %v", out, err)
	}
	<-editRequests
	<-editRequests
	<-editRequests
	created, err := os.ReadFile(filepath.Join(cwd, "facade-created.txt"))
	if err != nil || string(created) != "created" {
		t.Fatalf("facade created=%s %v", created, err)
	}
	raw, err = os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), `"hook_event_name":"PreToolUse"`) != 1 || !strings.Contains(string(raw), `"tool_name":"Write"`) || !strings.Contains(string(raw), "facade-created.txt") {
		t.Fatalf("facade hook mapping=%s", raw)
	}
	// An empty successful continuation must not reuse the prior final reply
	// for a second projection or Stop event.
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	emptyProxy, emptyRequests := projectCLIProxyScript(t, home, func(step int) *projectCLICall {
		if step == 1 {
			return &projectCLICall{Empty: true}
		}
		return nil
	})
	if out, err := run(emptyProxy, "-p", "empty continuation regression"); err != nil {
		t.Fatalf("empty continuation: %s %v", out, err)
	}
	<-emptyRequests
	<-emptyRequests
	raw, err = os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), `"hook_event_name":"Stop"`) != 1 {
		t.Fatalf("empty completion repeated Stop: %s", raw)
	}
	paths, _ = filepath.Glob(filepath.Join(home, ".kh", "plugin-transcripts", "config", "projects", "*", "*.jsonl"))
	found := false
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "empty continuation regression") {
			found = true
			if strings.Count(string(b), `"type":"assistant"`) != 1 {
				t.Fatalf("empty completion duplicated projection: %s", b)
			}
		}
	}
	if !found {
		t.Fatal("empty-continuation projection not found")
	}
	// Invalid pasted images fail before the actual-user callback. Session
	// lifecycle still closes, but there is no user projection or Stop event.
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := run(proxy, "-p", "[[kh:image:00000000000000000000000000000000]]"); err == nil {
		t.Fatalf("invalid image accepted: %s", out)
	}
	raw, err = os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), `"hook_event_name":"SessionStart"`) != 1 || strings.Count(string(raw), `"hook_event_name":"SessionEnd"`) != 1 || strings.Contains(string(raw), "UserPromptSubmit") || strings.Contains(string(raw), `"hook_event_name":"Stop"`) {
		t.Fatalf("invalid image hooks: %s", raw)
	}
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := run(proxy, "-p", "[from kh:fixture] delegated peer instruction"); err != nil {
		t.Fatalf("peer turn: %s %v", out, err)
	}
	raw, err = os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "UserPromptSubmit") || strings.Contains(string(raw), `"hook_event_name":"Stop"`) {
		t.Fatalf("peer message hooks: %s", raw)
	}
	paths, _ = filepath.Glob(filepath.Join(home, ".kh", "plugin-transcripts", "config", "projects", "*", "*.jsonl"))
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "delegated peer instruction") || strings.Contains(string(b), "00000000000000000000000000000000") {
			t.Fatalf("non-user projection: %s", b)
		}
	}
	if out, err := run("", "plugins", "revoke", id); err != nil {
		t.Fatalf("revoke: %s %v", out, err)
	}
	if out, err := run("", "plugins", "list"); err != nil || !strings.Contains(out, "trusted=false") {
		t.Fatalf("revoked list: %s %v", out, err)
	}
}
