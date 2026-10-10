package test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A private tmux socket keeps this test away from the user's tmux sessions.
func TestTmuxAgents(t *testing.T) {
	for _, source := range []string{"", filepath.Join(t.TempDir(), "selected source 'quoted' \"double\"")} {
		name := "empty-source"
		if source != "" {
			name = "selected-source"
		}
		t.Run(name, func(t *testing.T) { testTmuxAgents(t, source) })
	}
}

func testTmuxAgents(t *testing.T, source string) {
	t.Setenv("KH_SOURCE_DIR", source)
	t.Setenv("KH_REBUILD_EXEC_PID", "inherited-marker")
	t.Setenv("KH_AUTO_REBUILD", "0")
	t.Setenv("KH_PROVIDER", "")
	t.Setenv("KH_MODEL", "")
	t.Setenv("KH_EFFORT", "")
	t.Setenv("KH_AGENT", "")
	t.Setenv("KH_PARENT", "")
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
		cmd := exec.Command("tmux", append([]string{"-L", name, "-f", "/dev/null"}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %s: %v", args, b, err)
		}
		return strings.TrimSpace(string(b))
	}
	runTmux("new-session", "-d", "-s", "kh", "-n", "main", "-x", "240", "-y", "90", "sleep 60")
	defer runTmux("kill-server")
	// Simulate a server left running since another checkout/configuration.
	// Subsequent kh invocations still have the original process environment.
	for key, value := range map[string]string{
		"KH_SOURCE_DIR":       "/stale/server/source",
		"KH_AUTO_REBUILD":     "1",
		"KH_REBUILD_EXEC_PID": "stale-server-marker",
	} {
		runTmux("set-environment", "-g", key, value)
	}
	socket := runTmux("display-message", "-p", "#{socket_path}")
	mainPane := runTmux("display-message", "-p", "-t", "kh:main.0", "#{pane_id}")
	runTmux("set-option", "-p", "-t", mainPane, "@kh_name", "main")
	khFrom := func(pane string, args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Env = append(os.Environ(), "TMUX="+socket+",1,0", "TMUX_PANE="+pane, "HOME="+home, "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=")
		cmd.Stdin = strings.NewReader("")
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	kh := func(args ...string) (string, error) { return khFrom(mainPane, args...) }
	if s, err := kh("agents"); err != nil || !strings.Contains(s, "kh:main  unknown") {
		t.Fatalf("agents: %q %v", s, err)
	}
	if s, err := kh("send", "kh:main", "hello from test"); err != nil {
		t.Fatalf("send: %s %v", s, err)
	}
	if s, err := kh("peek", "kh:main"); err != nil || !strings.Contains(s, "hello from test") {
		t.Fatalf("peek: %q %v", s, err)
	}
	start := time.Now()
	if s, err := kh("--auto", "-provider", "codex", "-model", "future-model", "-effort", "low", "spawn", "docs", "hello world"); err != nil {
		t.Fatalf("spawn: %q %v", s, err)
	}
	elapsed := time.Since(start)
	t.Logf("spawn returned in %v (does not wait for model)", elapsed)
	if elapsed > 3*time.Second {
		t.Errorf("spawn waited too long: %v", elapsed)
	}
	docsPane := runTmux("display-message", "-p", "-t", "kh:main.1", "#{pane_id}")
	command := runTmux("display-message", "-p", "-t", docsPane, "#{pane_start_command}")
	assertTmuxStartupEnvironment(t, command, source)
	if windows := runTmux("list-windows", "-t", "kh", "-F", "#{window_name}"); windows != "main" {
		t.Fatalf("spawn opened another tab: %s", windows)
	}
	if active := runTmux("display-message", "-p", "-t", "kh:main", "#{pane_id}"); active != mainPane {
		t.Fatalf("spawn stole focus: %s", active)
	}
	if left := runTmux("display-message", "-p", "-t", docsPane, "#{pane_left}"); left == "0" {
		t.Fatal("worker not on the right")
	}
	if title := runTmux("display-message", "-p", "-t", docsPane, "#{pane_title}"); title != "docs" {
		t.Fatalf("worker title: %q", title)
	}
	if strings.Contains(command, "'-i'") {
		t.Errorf("spawned agent should exit after its task: %q", command)
	}
	for _, value := range []string{"--auto", "-provider", "codex", "-model", "future-model", "-effort", "low"} {
		if !strings.Contains(command, value) {
			t.Errorf("spawn did not pass %q: %q", value, command)
		}
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
	// The invoker's identity must not depend on which pane the user focuses.
	runTmux("select-pane", "-t", docsPane)
	if s, err := kh("spawn", "tests", "run isolated tests"); err != nil {
		t.Fatalf("second spawn: %q %v", s, err)
	}
	if active := runTmux("display-message", "-p", "-t", "kh:main", "#{pane_id}"); active != docsPane {
		t.Fatalf("second spawn changed focus: %s", active)
	}
	testsPane := runTmux("display-message", "-p", "-t", "kh:main.2", "#{pane_id}")
	if count := runTmux("display-message", "-p", "-t", "kh:main", "#{window_panes}"); count != "3" {
		t.Fatalf("want three panes: %s", count)
	}
	if left := runTmux("display-message", "-p", "-t", testsPane, "#{pane_left}"); left != runTmux("display-message", "-p", "-t", docsPane, "#{pane_left}") {
		t.Fatal("workers not stacked in the same right column")
	}
	if s, err := khFrom(docsPane, "send", "kh:tests", "peer coordination"); err != nil {
		t.Fatalf("peer send: %q %v", s, err)
	}
	if s, err := kh("peek", "kh:tests"); err != nil || !strings.Contains(s, "[from kh:docs] peer coordination") {
		t.Fatalf("peer message missing: %q %v", s, err)
	}
	if s, err := khFrom(testsPane, "send", "kh:main", "yes"); err != nil {
		t.Fatalf("reply: %q %v", s, err)
	}
	if s, err := kh("peek", "kh:main"); err != nil || !strings.Contains(s, "[from kh:tests] yes") {
		t.Fatalf("reply lacks sender attribution: %q %v", s, err)
	}
	// Pane identity, not a window name/index, survives a move and rejoin.
	runTmux("break-pane", "-d", "-s", docsPane, "-n", "moved")
	runTmux("join-pane", "-v", "-d", "-s", docsPane, "-t", testsPane)
	if name := runTmux("display-message", "-p", "-t", docsPane, "#{@kh_name}"); name != "docs" {
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
	for _, message := range []string{"literal ;", "--leading-option", "quotes ' and \" and $() and `code`", "UTF-8: café 日本語", "yes"} {
		if s, err := khFrom(docsPane, "send", "kh:main", message); err != nil {
			t.Fatalf("literal send: %q %v", s, err)
		}
		if s, err := kh("peek", "kh:main"); err != nil || !strings.Contains(s, "[from kh:docs] "+message) {
			t.Fatalf("message altered: %q %v", s, err)
		}
	}
	if s, err := kh("send", "kh:main", "interrupt\x03"); err == nil {
		t.Fatalf("accepted terminal control input: %q", s)
	}
	// Logical addresses also work in an existing, non-kh tmux session.
	runTmux("new-session", "-d", "-s", "work", "-n", "chat", "-x", "240", "-y", "90", "sleep 60")
	workMain := runTmux("display-message", "-p", "-t", "work:chat.0", "#{pane_id}")
	runTmux("set-option", "-p", "-t", workMain, "@kh_name", "main")
	if s, err := khFrom(workMain, "spawn", "docs", "independent session worker"); err != nil {
		t.Fatalf("existing session spawn: %q %v", s, err)
	}
	workDocs := runTmux("display-message", "-p", "-t", "work:chat.1", "#{pane_id}")
	if s, err := khFrom(workDocs, "send", "kh:main", "session-local reply"); err != nil {
		t.Fatalf("existing session send: %q %v", s, err)
	}
	if s, err := khFrom(workMain, "peek", "kh:main"); err != nil || !strings.Contains(s, "session-local reply") {
		t.Fatalf("reply misrouted: %q %v", s, err)
	}
	if s, err := kh("peek", "kh:main"); err != nil || strings.Contains(s, "session-local reply") {
		t.Fatalf("message leaked across sessions: %q %v", s, err)
	}
	// --keep leaves the worker in chat for follow-up messages.
	if s, err := khFrom(workMain, "spawn", "--keep", "keeper", "stay for follow-ups"); err != nil {
		t.Fatalf("keep spawn: %q %v", s, err)
	}
	keeper := runTmux("display-message", "-p", "-t", "work:chat.2", "#{pane_start_command}")
	assertTmuxStartupEnvironment(t, keeper, source)
	if !strings.Contains(keeper, "'-i'") {
		t.Fatalf("--keep did not keep the agent in chat: %q", keeper)
	}
}

// Check the public tmux launch command, including shell quoting for paths with
// spaces and quotes. Explicit empty assignments must override server values.
func assertTmuxStartupEnvironment(t *testing.T, command, source string) {
	t.Helper()
	// tmux quotes and escapes pane_start_command when it contains quotes.
	if strings.HasPrefix(command, "\"") {
		decoded, err := strconv.Unquote(command)
		if err != nil {
			t.Fatalf("decode tmux launch command %q: %v", command, err)
		}
		command = decoded
	}
	for _, assignment := range []string{"KH_SOURCE_DIR=" + source, "KH_AUTO_REBUILD=0", "KH_REBUILD_EXEC_PID="} {
		quoted := "'" + strings.ReplaceAll(assignment, "'", "'\\''") + "'"
		if !strings.Contains(command, quoted) {
			t.Errorf("launch did not explicitly forward %q: %q", assignment, command)
		}
	}
	for _, stale := range []string{"/stale/server/source", "stale-server-marker", "inherited-marker"} {
		if strings.Contains(command, stale) {
			t.Errorf("launch retained stale environment %q: %q", stale, command)
		}
	}
}
