package test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"kh/internal/ecosystem"
	"kh/internal/terminal"
)

// Exercise the real console transport rather than a sleeping pane's input echo.
func TestTmuxInputProcess(t *testing.T) {
	if os.Getenv("KH_TMUX_INPUT_HELPER") != "1" {
		return
	}
	console := terminal.NewConsole(os.Stdin, os.Stdout, os.Stderr)
	defer console.Close()
	console.Start()
	// The integration fixture advertises the same live-receiver capability.
	if out, err := exec.Command("tmux", "set-option", "-p", "-t", os.Getenv("TMUX_PANE"), "@kh_peer_input", strconv.Itoa(os.Getpid())).CombinedOutput(); err != nil {
		t.Fatalf("advertise input: %s: %v", out, err)
	}
	fmt.Println("CONSOLE_READY")
	for line := range console.Lines {
		fmt.Println(line)
	}
}

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
	isolateCLIFixture(t)
	name := fmt.Sprintf("kh_test_%d", os.Getpid())
	home := t.TempDir() // spawned kh must not write to the user's real ~/.kh
	t.Setenv("HOME", home)
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
	helper := "env KH_TMUX_INPUT_HELPER=1 KH_AUTO_REBUILD=0 " + "'" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "' -test.run=^TestTmuxInputProcess$"
	runTmux("new-session", "-d", "-s", "kh", "-n", "main", "-x", "240", "-y", "90", helper)
	defer runTmux("kill-server")
	waitConsole := func(pane string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for runTmux("display-message", "-p", "-t", pane, "#{@kh_peer_input}") == "" || !strings.Contains(runTmux("capture-pane", "-p", "-t", pane), "CONSOLE_READY") {
			if time.Now().After(deadline) {
				t.Fatal("console did not become ready")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
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
	waitConsole(mainPane)
	ctx := context.Background()
	store, err := ecosystem.Open(ecosystem.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registerConsole := func(pane, name string, parent *ecosystem.Agent) ecosystem.Agent {
		t.Helper()
		pid, err := strconv.Atoi(runTmux("display-message", "-p", "-t", pane, "#{@kh_peer_input}"))
		if err != nil {
			t.Fatal(err)
		}
		process, err := ecosystem.ProcessForPID(pid)
		if err != nil {
			t.Fatal(err)
		}
		info := ecosystem.PaneInfo{PaneID: pane, SessionID: runTmux("display-message", "-p", "-t", pane, "#{session_id}")}
		var a ecosystem.Agent
		if parent == nil {
			a, err = store.CreateRoot(ctx, ecosystem.RootOptions{Name: name, MaxAgents: 20, Process: process, Pane: info})
		} else {
			// Respawning a fixture console creates a new process incarnation;
			// never retain the failed kh process's identity on its reused pane.
			deadline := time.Now().Add(3 * time.Second)
			var reservation ecosystem.Reservation
			for {
				if err := store.Reconcile(ctx, parent.EcosystemID, ecosystem.ProbeProcess); err != nil {
					t.Fatal(err)
				}
				reservation, err = store.Reserve(ctx, parent.ID, name, "console transport fixture")
				if err == nil || time.Now().After(deadline) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err == nil {
				err = store.BeginLaunch(ctx, reservation.Agent.ID, reservation.Token, parent.Process)
			}
			if err == nil {
				a, err = store.Claim(ctx, reservation.Agent.ID, reservation.Token, process, info)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		runTmux("set-option", "-p", "-t", pane, "@kh_ecosystem", a.EcosystemID, ";", "set-option", "-p", "-t", pane, "@kh_agent_id", a.ID)
		return a
	}
	mainAgent := registerConsole(mainPane, "main", nil)
	// Pane capture is only a local assertion on this fixture's own console;
	// public inspect/peek must never return these private transport contents.
	assertConsole := func(pane, text string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for !strings.Contains(runTmux("capture-pane", "-p", "-t", pane), text) {
			if time.Now().After(deadline) {
				t.Fatalf("console %s did not receive %q", pane, text)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	khFrom := func(pane string, args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Env = append(os.Environ(), "TMUX="+socket+",1,0", "TMUX_PANE="+pane, "HOME="+home, "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=")
		cmd.Stdin = strings.NewReader("")
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	kh := func(args ...string) (string, error) { return khFrom(mainPane, args...) }
	if s, err := kh("agents"); err != nil || !strings.Contains(s, "kh:main  running  manager") {
		t.Fatalf("agents: %q %v", s, err)
	}
	// A reused legacy pane must reject delivery instead of corrupting a draft.
	runTmux("set-option", "-p", "-t", mainPane, "@kh_peer_input", "")
	if s, err := kh("send", "kh:main", "must not inject"); err == nil || !strings.Contains(s, "not ready for isolated peer input") {
		t.Fatalf("legacy receiver accepted: %q %v", s, err)
	}
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	runTmux("set-option", "-p", "-t", mainPane, "@kh_peer_input", strconv.Itoa(dead.Process.Pid))
	if s, err := kh("send", "kh:main", "must not inject after crash"); err == nil || !strings.Contains(s, "not ready for isolated peer input") {
		t.Fatalf("dead receiver accepted: %q %v", s, err)
	}
	runTmux("set-option", "-p", "-t", mainPane, "@kh_peer_input", runTmux("display-message", "-p", "-t", mainPane, "#{pane_pid}"))
	owner := runTmux("display-message", "-p", "-t", mainPane, "#{@kh_peer_input}")
	for _, args := range [][]string{{"-provider", "codex"}, {"-p", "-provider", "codex", "nested task"}} {
		// Both EOF chat and failed one-shot turns inherit the pane environment,
		// but redirected input/output never own its interactive receiver.
		if out, err := kh(args...); err == nil || !strings.Contains(out, "pane already belongs to a live ecosystem") {
			t.Fatalf("redirected startup bypassed live-pane admission: %q: %v", out, err)
		}
		if got := runTmux("display-message", "-p", "-t", mainPane, "#{@kh_peer_input}"); got != owner {
			t.Fatalf("redirected child changed receiver: got=%q want=%q", got, owner)
		}
		if got := runTmux("display-message", "-p", "-t", mainPane, "#{@kh_agent_id}"); got != mainAgent.ID {
			t.Fatalf("redirected child changed ecosystem owner: got=%q want=%q", got, mainAgent.ID)
		}
	}
	if s, err := kh("send", "kh:main", "hello from test"); err != nil {
		t.Fatalf("send: %s %v", s, err)
	}
	assertConsole(mainPane, "hello from test")
	if s, err := kh("peek", "kh:main"); err != nil || !strings.Contains(s, mainAgent.ID) || strings.Contains(s, "hello from test") {
		t.Fatalf("peek must expose metadata, not pane output: %q %v", s, err)
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
	if strings.Contains(command, "'-i'") || !strings.Contains(command, "'-p'") {
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
	// An unauthenticated one-shot failure may leave a visible dead pane, but
	// must not keep its kh process (and ecosystem capacity slot) alive.
	deadline := time.Now().Add(5 * time.Second)
	for {
		a, err := store.Resolve(ctx, mainAgent.EcosystemID, "kh:docs")
		if err != nil {
			t.Fatal(err)
		}
		if a.Process.Valid() {
			alive, err := ecosystem.ProbeProcess(a.Process)
			if err != nil {
				t.Fatal(err)
			}
			if !alive && runTmux("display-message", "-p", "-t", docsPane, "#{pane_dead}") == "1" {
				if err := store.Reconcile(ctx, mainAgent.EcosystemID, ecosystem.ProbeProcess); err != nil {
					t.Fatal(err)
				}
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed one-shot retained a live process: %+v", a)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s, err := kh("send", "kh:main", "bad\nmessage"); err == nil {
		t.Fatalf("accepted multiline: %q", s)
	}
	// Startup routing is checked above; use a real console as the receiver so
	// these transport checks need neither provider credentials nor input echo.
	runTmux("set-option", "-p", "-t", docsPane, "@kh_peer_input", "")
	runTmux("respawn-pane", "-k", "-t", docsPane, helper)
	waitConsole(docsPane)
	docsAgent := registerConsole(docsPane, "docs", &mainAgent)
	if s, err := kh("spawn", "docs", "duplicate"); err == nil {
		t.Fatalf("accepted live duplicate: %q", s)
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
	runTmux("set-option", "-p", "-t", testsPane, "@kh_peer_input", "")
	runTmux("respawn-pane", "-k", "-t", testsPane, helper)
	waitConsole(testsPane)
	testsAgent := registerConsole(testsPane, "tests", &mainAgent)
	if count := runTmux("display-message", "-p", "-t", "kh:main", "#{window_panes}"); count != "3" {
		t.Fatalf("want three panes: %s", count)
	}
	if left := runTmux("display-message", "-p", "-t", testsPane, "#{pane_left}"); left != runTmux("display-message", "-p", "-t", docsPane, "#{pane_left}") {
		t.Fatal("workers not stacked in the same right column")
	}
	if s, err := khFrom(docsPane, "send", "kh:tests", "peer coordination"); err != nil {
		t.Fatalf("peer send: %q %v", s, err)
	}
	assertConsole(testsPane, "[from "+docsAgent.Address+"] peer coordination")
	if s, err := khFrom(testsPane, "send", "kh:main", "yes"); err != nil {
		t.Fatalf("reply: %q %v", s, err)
	}
	assertConsole(mainPane, "[from "+testsAgent.Address+"] yes")
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
	assertConsole(docsPane, "direct to joined pane")
	if s, err := kh("spawn", "docs", "duplicate after join"); err == nil {
		t.Fatalf("accepted duplicate after join: %q", s)
	}
	for _, message := range []string{"literal ;", "--leading-option", "quotes ' and \" and $() and `code`", "UTF-8: café 日本語", "yes"} {
		if s, err := khFrom(docsPane, "send", "kh:main", message); err != nil {
			t.Fatalf("literal send: %q %v", s, err)
		}
		assertConsole(mainPane, "[from "+docsAgent.Address+"] "+message)
	}
	if s, err := kh("send", "kh:main", "interrupt\x03"); err == nil {
		t.Fatalf("accepted terminal control input: %q", s)
	}
	// Logical addresses also work in an existing, non-kh tmux session.
	runTmux("new-session", "-d", "-s", "work", "-n", "chat", "-x", "240", "-y", "90", helper)
	workMain := runTmux("display-message", "-p", "-t", "work:chat.0", "#{pane_id}")
	runTmux("set-option", "-p", "-t", workMain, "@kh_name", "main")
	waitConsole(workMain)
	workAgent := registerConsole(workMain, "main", nil)
	if s, err := khFrom(workMain, "spawn", "docs", "independent session worker"); err != nil {
		t.Fatalf("existing session spawn: %q %v", s, err)
	}
	workDocs := runTmux("display-message", "-p", "-t", "work:chat.1", "#{pane_id}")
	runTmux("set-option", "-p", "-t", workDocs, "@kh_peer_input", "")
	runTmux("respawn-pane", "-k", "-t", workDocs, helper)
	waitConsole(workDocs)
	registerConsole(workDocs, "docs", &workAgent)
	if s, err := khFrom(workDocs, "send", "kh:main", "session-local reply"); err != nil {
		t.Fatalf("existing session send: %q %v", s, err)
	}
	assertConsole(workMain, "session-local reply")
	if s := runTmux("capture-pane", "-p", "-t", mainPane); strings.Contains(s, "session-local reply") {
		t.Fatalf("message leaked across ecosystems: %q", s)
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
		// tmux uses shell-style dollar escapes, which Go string literals do
		// not recognise; remove only that quoting layer before decoding.
		decoded, err := strconv.Unquote(strings.ReplaceAll(command, `\$`, `$`))
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
