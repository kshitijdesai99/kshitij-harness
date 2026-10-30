package test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"kh/internal/session"
)

// /fork copies the chat into a new session and splits a pane to the right
// of the current one, selecting the new pane without creating a window.
func TestForkOpensNewPaneToTheRight(t *testing.T) {
	for _, source := range []string{"", filepath.Join(t.TempDir(), "selected source 'quoted' \"double\"")} {
		name := "empty-source"
		if source != "" {
			name = "selected-source"
		}
		t.Run(name, func(t *testing.T) { testForkOpensNewPaneToTheRight(t, source) })
	}
}

func testForkOpensNewPaneToTheRight(t *testing.T, source string) {
	t.Setenv("KH_SOURCE_DIR", source)
	t.Setenv("KH_REBUILD_EXEC_PID", "inherited-marker")
	t.Setenv("KH_AUTO_REBUILD", "0")
	auto := source == ""
	autoEnv := "0"
	staleAutoEnv := "1"
	if auto {
		autoEnv, staleAutoEnv = "1", "0"
	}
	t.Setenv("KH_AUTO", autoEnv)
	assertAuto := func(command string) {
		t.Helper()
		if decoded, err := strconv.Unquote(command); err == nil {
			command = decoded
		}
		if !strings.Contains(command, "'KH_AUTO="+autoEnv+"'") || !strings.Contains(command, "'--auto="+strconv.FormatBool(auto)+"'") {
			t.Fatalf("child lost active auto policy: %s", command)
		}
	}
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
	home := t.TempDir()
	t.Setenv("HOME", home)
	state, err := session.Encode("codex", []byte(`{"session":"s","map":"test repo map","input":[
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
		cmd := exec.Command("tmux", append([]string{"-L", name, "-f", "/dev/null"}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %s: %v", args, b, err)
		}
		return strings.TrimSpace(string(b))
	}
	runTmux("new-session", "-d", "-s", "kh", "-n", "main", "-x", "200", "-y", "50", "-c", cwd,
		"env KH_PROVIDER= KH_MODEL= KH_EFFORT= KH_AGENT=kh:worker KH_PARENT=kh:upstream "+binary+" -s original")
	defer runTmux("kill-server")
	runTmux("new-window", "-d", "-t", "kh:1", "-n", "other", "sleep 60")
	originalPane := runTmux("display-message", "-p", "-t", "kh:main", "#{pane_id}")
	waitFor(t, func() bool {
		return strings.Contains(runTmux("capture-pane", "-p", "-t", "kh:main"), "There are 3 files.")
	})

	// Change the already-running server, not the initiating chat's environment.
	// A fork must not inherit these stale startup settings from the server.
	for key, value := range map[string]string{
		"KH_SOURCE_DIR":       "/stale/server/source",
		"KH_AUTO_REBUILD":     "1",
		"KH_AUTO":             staleAutoEnv,
		"KH_PARENT":           "kh:stale-parent",
		"KH_REBUILD_EXEC_PID": "stale-server-marker",
	} {
		runTmux("set-environment", "-g", key, value)
	}
	// Queue both before either child starts: addresses must be reserved by the
	// parent rather than relying on the fork's startup to register its name.
	runTmux("send-keys", "-t", originalPane, "/fork", "Enter", "/fork", "Enter")
	waitForFork := func(name string) string {
		t.Helper()
		var id string
		waitFor(t, func() bool {
			for _, pane := range strings.Split(runTmux("list-panes", "-t", "kh:main", "-F", "#{pane_id} #{@kh_name}"), "\n") {
				if fields := strings.Fields(pane); len(fields) == 2 && fields[1] == name {
					id = fields[0]
					return true
				}
			}
			return false
		})
		return id
	}
	forkPane := waitForFork("fork")
	secondPane := waitForFork("fork-2")

	if windows := runTmux("list-windows", "-t", "kh", "-F", "#{window_name}"); windows != "main\nother" {
		t.Fatalf("fork must not create or rename windows: %q", windows)
	}
	if panes := runTmux("list-panes", "-t", "kh:main", "-F", "#{pane_id}"); len(strings.Fields(panes)) != 3 {
		t.Fatalf("expected exactly three panes: %q", panes)
	}
	if active := runTmux("display-message", "-p", "-t", "kh:main", "#{pane_id}"); active != secondPane {
		t.Fatalf("latest fork pane not selected: %q", active)
	}
	// Horizontal splitting places panes on the same row, with the fork's left
	// edge immediately after the original pane and its separator.
	originalGeometry := runTmux("display-message", "-p", "-t", originalPane, "#{pane_top}:#{pane_right}:#{pane_height}")
	forkGeometry := runTmux("display-message", "-p", "-t", secondPane, "#{pane_top}:#{pane_left}:#{pane_height}")
	var originalTop, originalRight, originalHeight, forkTop, forkLeft, forkHeight int
	if _, err := fmt.Sscanf(originalGeometry, "%d:%d:%d", &originalTop, &originalRight, &originalHeight); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Sscanf(forkGeometry, "%d:%d:%d", &forkTop, &forkLeft, &forkHeight); err != nil {
		t.Fatal(err)
	}
	if forkTop != originalTop || forkHeight != originalHeight || forkLeft != originalRight+2 {
		t.Fatalf("fork not immediately to the right: original %s, fork %s", originalGeometry, forkGeometry)
	}
	command := runTmux("display-message", "-p", "-t", forkPane, "#{pane_start_command}")
	assertTmuxStartupEnvironment(t, command, source)
	assertAuto(command)
	id := command[strings.LastIndex(command, "'-s' '")+6:]
	id = strings.TrimSuffix(strings.TrimSuffix(id, `"`), "'")
	copied, err := session.Load(id)
	if err != nil {
		t.Fatalf("load fork session %q: %v", id, err)
	}
	// Provider Save normalizes JSON key ordering; compare the full state by
	// value while checking the original file remains byte-for-byte unchanged.
	forkRecord, err := session.Decode(copied)
	if err != nil || forkRecord.Identity == nil || forkRecord.Identity.Role != "main" || forkRecord.Identity.Name != "fork" || forkRecord.Identity.Parent != "" {
		t.Fatalf("fork identity: %+v: %v", forkRecord, err)
	}
	originalRecord, err := session.Decode(state)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(forkRecord.State, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(originalRecord.State, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("session %q not copied: %s", id, copied)
	}
	if original, err := session.Load("original"); err != nil || string(original) != string(state) {
		t.Fatalf("fork changed original session: %s: %v", original, err)
	}
	// The fork replays the chat and ends at its last reply, waiting for input.
	waitFor(t, func() bool {
		screen := runTmux("capture-pane", "-p", "-t", forkPane)
		return strings.Contains(screen, "There are 3 files.") && strings.Contains(screen, "> count files")
	})
	if name := runTmux("display-message", "-p", "-t", forkPane, "#{@kh_name}"); name != "fork" {
		t.Fatalf("fork address: %q", name)
	}

	// Forking again from the original must allocate an independent session
	// and address, without replacing the first fork or steering the original.
	if secondPane == forkPane || secondPane == originalPane {
		t.Fatalf("repeated fork reused pane %s", secondPane)
	}
	secondCommand := runTmux("display-message", "-p", "-t", secondPane, "#{pane_start_command}")
	assertTmuxStartupEnvironment(t, secondCommand, source)
	assertAuto(secondCommand)
	secondID := secondCommand[strings.LastIndex(secondCommand, "'-s' '")+6:]
	secondID = strings.TrimSuffix(strings.TrimSuffix(secondID, `"`), "'")
	if secondID == id {
		t.Fatalf("repeated fork reused session %q", id)
	}
	second, err := session.Load(secondID)
	if err != nil {
		t.Fatal(err)
	}
	secondRecord, err := session.Decode(second)
	if err != nil || secondRecord.Identity == nil || secondRecord.Identity.Role != "main" || secondRecord.Identity.Name != "fork-2" || string(secondRecord.State) != string(forkRecord.State) {
		t.Fatalf("repeated fork changed checkpoint/identity: %s: %v", second, err)
	}
	if panes := runTmux("list-panes", "-t", "kh:main", "-F", "#{pane_id}"); len(strings.Fields(panes)) != 3 {
		t.Fatalf("expected three panes after repeated fork: %q", panes)
	}
	if original, err := session.Load("original"); err != nil || string(original) != string(state) {
		t.Fatalf("repeated fork changed original session: %s: %v", original, err)
	}
	waitFor(t, func() bool {
		return runTmux("display-message", "-p", "-t", originalPane, "#{@kh_state}") == "waiting"
	})

	// /new uses the same non-steering pane path but must start without any
	// conversation or resume flags, with independent identities even when queued.
	runTmux("send-keys", "-t", forkPane, "/new", "Enter", "/new", "Enter")
	newIDs := map[string]bool{id: true, secondID: true}
	for _, name := range []string{"new", "new-2"} {
		pane := waitForFork(name)
		command := runTmux("display-message", "-p", "-t", pane, "#{pane_start_command}")
		if decoded, err := strconv.Unquote(command); err == nil {
			command = decoded
		}
		assertTmuxStartupEnvironment(t, command, source)
		assertAuto(command)
		if strings.Contains(command, "'-s'") || strings.Contains(command, "'-r'") {
			t.Fatalf("new session unexpectedly resumes history: %s", command)
		}
		var screen string
		waitFor(t, func() bool {
			screen = runTmux("capture-pane", "-p", "-J", "-t", pane)
			return strings.Contains(screen, "kh chat, session")
		})
		match := regexp.MustCompile(`kh chat, session ([^ ]+)`).FindStringSubmatch(screen)
		if len(match) != 2 || newIDs[match[1]] || strings.Contains(screen, "There are 3 files.") || strings.Contains(screen, "> count files") {
			t.Fatalf("new session not fresh and independent: %s", screen)
		}
		newIDs[match[1]] = true
		// No credentials are installed. A failed turn still saves the new
		// identity, proving the child cleared the inherited worker parent.
		runTmux("send-keys", "-t", pane, "question", "Enter")
		waitFor(t, func() bool {
			b, err := session.Load(match[1])
			if err != nil {
				return false
			}
			r, err := session.Decode(b)
			return err == nil && r.Identity != nil && r.Identity.Role == "main" && r.Identity.Name == name && r.Identity.Parent == ""
		})
	}
	if original, err := session.Load("original"); err != nil || string(original) != string(state) {
		t.Fatalf("/new changed original session: %s: %v", original, err)
	}

	// A fresh chat has no completed conversation to copy.
	runTmux("new-window", "-t", "kh", "-n", "empty", "-c", cwd,
		"env KH_PROVIDER= KH_MODEL= KH_EFFORT= KH_AGENT= KH_PARENT= KH_AUTO_REBUILD=0 "+binary)
	emptyPane := runTmux("display-message", "-p", "-t", "kh:empty", "#{pane_id}")
	waitFor(t, func() bool {
		return strings.Contains(runTmux("capture-pane", "-p", "-t", emptyPane), "kh chat, session")
	})
	runTmux("send-keys", "-t", emptyPane, "/fork", "Enter")
	waitFor(t, func() bool {
		return strings.Contains(runTmux("capture-pane", "-p", "-t", emptyPane), "nothing to fork yet")
	})
	if panes := runTmux("list-panes", "-t", "kh:empty", "-F", "#{pane_id}"); len(strings.Fields(panes)) != 1 {
		t.Fatalf("empty chat unexpectedly forked: %q", panes)
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
