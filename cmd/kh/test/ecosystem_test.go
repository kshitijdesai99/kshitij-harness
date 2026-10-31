package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"kh/internal/ecosystem"
	"kh/internal/session"
)

func TestEcosystemCLIDirectoryPrivacyAndLifecycle(t *testing.T) {
	f := newEcosystemCLI(t)
	ctx := context.Background()
	store, err := ecosystem.Open(ecosystem.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	process := func(pane string) ecosystem.ProcessIdentity {
		t.Helper()
		pid, err := strconv.Atoi(f.runTmux("display-message", "-p", "-t", pane, "#{@kh_peer_input}"))
		if err != nil {
			t.Fatal(err)
		}
		identity, err := ecosystem.ProcessForPID(pid)
		if err != nil {
			t.Fatal(err)
		}
		return identity
	}
	publish := func(a ecosystem.Agent) {
		t.Helper()
		f.runTmux("set-option", "-p", "-t", a.Pane.PaneID, "@kh_ecosystem", a.EcosystemID, ";",
			"set-option", "-p", "-t", a.Pane.PaneID, "@kh_agent_id", a.ID)
	}
	rootPane := f.console("root", "root")
	paneInfo := func(pane string) ecosystem.PaneInfo {
		return ecosystem.PaneInfo{PaneID: pane, SessionID: f.runTmux("display-message", "-p", "-t", pane, "#{session_id}")}
	}
	root, err := store.CreateRoot(ctx, ecosystem.RootOptions{Name: "root", MaxAgents: 4, Process: process(rootPane), Pane: paneInfo(rootPane)})
	if err != nil {
		t.Fatal(err)
	}
	publish(root)
	child := func(parent ecosystem.Agent, name string) ecosystem.Agent {
		t.Helper()
		pane := f.console(name, name)
		reservation, err := store.Reserve(ctx, parent.ID, name, "shared task for "+name)
		if err != nil {
			t.Fatal(err)
		}
		launcher, err := ecosystem.CurrentProcess()
		if err != nil {
			t.Fatal(err)
		}
		if err := store.BeginLaunch(ctx, reservation.Agent.ID, reservation.Token, launcher); err != nil {
			t.Fatal(err)
		}
		a, err := store.Claim(ctx, reservation.Agent.ID, reservation.Token, process(pane), paneInfo(pane))
		if err != nil {
			t.Fatal(err)
		}
		publish(a)
		return a
	}
	branch := child(root, "branch")
	if out, err := f.command(branch.Pane.PaneID, "spawn", "forbidden", "must not run"); err == nil || !strings.Contains(strings.ToLower(out), "manager") {
		t.Fatalf("worker spawn was not rejected before launch: %q: %v", out, err)
	}
	f.mustCommand(branch.Pane.PaneID, "set_role", "manager")
	branch, err = store.Get(ctx, branch.ID)
	if err != nil || branch.Role != "manager" {
		t.Fatalf("promotion did not persist: %+v: %v", branch, err)
	}
	// Exercise the actual recursive launch environment without any provider
	// connection: the fixture selects an unsupported provider. Startup must
	// claim the common identity before failing, and reconciliation frees it.
	f.mustCommand(branch.Pane.PaneID, "spawn", "failed-startup", "must fail locally before a model turn")
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mustCommand(rootPane, "stats") // CLI reconciliation proves exit.
		failed, err := store.Resolve(ctx, root.EcosystemID, "kh:failed-startup")
		if err != nil {
			t.Fatal(err)
		}
		if failed.ParentID != branch.ID || failed.EcosystemID != root.EcosystemID || failed.Role != ecosystem.Worker {
			t.Fatalf("recursive launch lost descendant identity: %+v", failed)
		}
		if !failed.State.Live() {
			if !failed.Process.Valid() {
				t.Fatalf("child never claimed its startup identity: %+v", failed)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed startup retained its capacity slot: %+v", failed)
		}
		time.Sleep(10 * time.Millisecond)
	}
	grandchild := child(branch, "grandchild")
	sibling := child(root, "sibling")
	// A retained interactive worker still consumes a live slot while idle.
	if err := store.Heartbeat(ctx, sibling.ID, sibling.Process, ecosystem.Idle); err != nil {
		t.Fatal(err)
	}
	for _, a := range []ecosystem.Agent{branch, grandchild, sibling} {
		if a.EcosystemID != root.EcosystemID {
			t.Fatalf("descendant escaped common ecosystem: %+v", a)
		}
	}
	if grandchild.Role != "worker" {
		t.Fatalf("manager child did not start as worker: %+v", grandchild)
	}
	// Every participant sees the same directory, even in another tmux window.
	members := []ecosystem.Agent{root, branch, grandchild, sibling}
	for _, caller := range members {
		out := f.mustCommand(caller.Pane.PaneID, "agents")
		for _, target := range members {
			if !strings.Contains(out, target.Address) {
				t.Fatalf("directory from %s omits %s: %s", caller.Address, target.Address, out)
			}
		}
		stats := f.mustCommand(caller.Pane.PaneID, "stats")
		var snapshot struct {
			Self  ecosystem.Agent `json:"self"`
			Stats ecosystem.Stats `json:"stats"`
		}
		if err := json.Unmarshal([]byte(stats), &snapshot); err != nil {
			t.Fatalf("decode stats: %s: %v", stats, err)
		}
		if snapshot.Self.ID != caller.ID || snapshot.Self.EcosystemID != root.EcosystemID || snapshot.Stats.Live != 4 || snapshot.Stats.MaxAgents != 4 || snapshot.Stats.Available != 0 || snapshot.Stats.Managers != 2 || snapshot.Stats.Workers != 2 || snapshot.Stats.States[ecosystem.Idle] != 1 {
			t.Fatalf("incorrect shared counts from %s: %s", caller.Address, stats)
		}
	}
	if out, err := f.command(branch.Pane.PaneID, "spawn", "overflow", "must not run at capacity"); err == nil || !strings.Contains(strings.ToLower(out), "capacity") {
		t.Fatalf("recursive CLI spawn ignored shared capacity: %q: %v", out, err)
	}
	for _, caller := range members {
		for _, target := range members {
			if caller.ID == target.ID {
				continue
			}
			message := "private-console-sentinel-" + caller.Name
			f.mustCommand(caller.Pane.PaneID, "send", target.Address, message)
			f.waitOutput(target.Pane.PaneID, message)
		}
	}
	const conclusion = "shared conclusion: fixture verified"
	if err := store.PublishConclusion(ctx, grandchild.ID, conclusion); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"inspect", "peek"} {
		out := f.mustCommand(sibling.Pane.PaneID, command, grandchild.Address)
		if !strings.Contains(out, conclusion) || strings.Contains(out, "private-console-sentinel-") || strings.Contains(out, "CONSOLE_READY") {
			t.Fatalf("%s must expose shared conclusion, not private pane output: %s", command, out)
		}
	}
	// Removing a manager alone retains and reparents its live descendants.
	f.mustCommand(sibling.Pane.PaneID, "stop", branch.Address)
	grandchild, err = store.Get(ctx, grandchild.ID)
	if err != nil || grandchild.ParentID != root.ID {
		t.Fatalf("single stop did not reparent grandchild: %+v: %v", grandchild, err)
	}
	f.mustCommand(grandchild.Pane.PaneID, "send", sibling.Address, "survived-parent-stop")
	f.waitOutput(sibling.Pane.PaneID, "survived-parent-stop")
	// Reuse the released slot for a branch with a live child, then stop it as a
	// subtree. The independent sibling must remain addressable throughout.
	f.mustCommand(grandchild.Pane.PaneID, "set_role", "manager")
	leaf := child(grandchild, "leaf")
	f.mustCommand(sibling.Pane.PaneID, "stop", grandchild.Address, "subtree")
	for _, id := range []string{grandchild.ID, leaf.ID} {
		a, err := store.Get(ctx, id)
		if err != nil || a.State.Live() {
			t.Fatalf("subtree member was not stopped: %+v: %v", a, err)
		}
	}
	f.mustCommand(rootPane, "send", sibling.Address, "survived-other-subtree")
	f.waitOutput(sibling.Pane.PaneID, "survived-other-subtree")
	// Stopping the root means the whole ecosystem, even with agent scope.
	f.mustCommand(sibling.Pane.PaneID, "stop", root.Address)
	for _, id := range []string{root.ID, sibling.ID} {
		a, err := store.Get(ctx, id)
		if err != nil || a.State.Live() {
			t.Fatalf("root shutdown left member live: %+v: %v", a, err)
		}
	}
}

// This helper deliberately acknowledges but does not exit on SIGTERM. It only
// runs on a private fixture socket, and stop must eventually escalate to KILL.
func TestEcosystemSlowStopProcess(t *testing.T) {
	if os.Getenv("KH_ECOSYSTEM_STOP_HELPER") != "1" {
		return
	}
	terms := make(chan os.Signal, 1)
	signal.Notify(terms, syscall.SIGTERM)
	defer signal.Stop(terms)
	if out, err := exec.Command("tmux", "set-option", "-p", "-t", os.Getenv("TMUX_PANE"), "@kh_peer_input", strconv.Itoa(os.Getpid())).CombinedOutput(); err != nil {
		t.Fatalf("advertise helper: %s: %v", out, err)
	}
	fmt.Println("CONSOLE_READY")
	<-terms
	if err := os.WriteFile(os.Getenv("KH_ECOSYSTEM_STOP_MARKER"), []byte("SIGTERM"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Second)
}

func TestEcosystemStopCancellationFinishesWholeRoot(t *testing.T) {
	f := newEcosystemCLI(t)
	ctx := context.Background()
	store, err := ecosystem.Open(ecosystem.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	process := func(pane string) ecosystem.ProcessIdentity {
		t.Helper()
		pid, err := strconv.Atoi(f.runTmux("display-message", "-p", "-t", pane, "#{@kh_peer_input}"))
		if err != nil {
			t.Fatal(err)
		}
		p, err := ecosystem.ProcessForPID(pid)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	rootPane := f.console("root", "root")
	root, err := store.CreateRoot(ctx, ecosystem.RootOptions{Name: "root", MaxAgents: 3, Process: process(rootPane), Pane: ecosystem.PaneInfo{PaneID: rootPane}})
	if err != nil {
		t.Fatal(err)
	}
	f.runTmux("set-option", "-p", "-t", rootPane, "@kh_ecosystem", root.EcosystemID, ";",
		"set-option", "-p", "-t", rootPane, "@kh_agent_id", root.ID)
	members := []ecosystem.Agent{root}
	markers := []string{}
	for _, name := range []string{"slow-one", "slow-two"} {
		marker := filepath.Join(t.TempDir(), "received-term")
		f.helper = "env KH_ECOSYSTEM_STOP_HELPER=1 'KH_ECOSYSTEM_STOP_MARKER=" + strings.ReplaceAll(marker, "'", "'\\''") + "' '" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "' -test.run=^TestEcosystemSlowStopProcess$"
		pane := f.console(name, name)
		r, err := store.Reserve(ctx, root.ID, name, "slow stop fixture")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.BeginLaunch(ctx, r.Agent.ID, r.Token, root.Process); err != nil {
			t.Fatal(err)
		}
		a, err := store.Claim(ctx, r.Agent.ID, r.Token, process(pane), ecosystem.PaneInfo{PaneID: pane})
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, a)
		markers = append(markers, marker)
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(callCtx, f.binary, "stop", root.Address)
	cmd.Env = append(os.Environ(), "TMUX="+f.tmux, "TMUX_PANE="+rootPane)
	cmd.Stdin = strings.NewReader("")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(markers[0]); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("stop exited before first TERM acknowledgement: %s: %v", output.String(), err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("first slow target never received SIGTERM")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// At least one slow target is still alive after the atomic Stopping
	// transition. Cancel exactly during that wait, not before Stop commits.
	for _, member := range members {
		a, err := store.Get(ctx, member.ID)
		if err != nil || a.State != ecosystem.Stopping {
			t.Fatalf("expected committed stop before cancellation: %+v: %v", a, err)
		}
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("cancelled stop did not finish cleanup: %s: %v", output.String(), err)
	}
	for _, marker := range markers {
		if b, err := os.ReadFile(marker); err != nil || string(b) != "SIGTERM" {
			t.Fatalf("stop skipped signalling a descendant: %q: %v", b, err)
		}
	}
	for _, member := range members {
		alive, err := ecosystem.ProbeProcess(member.Process)
		if err != nil || alive {
			t.Fatalf("cancelled stop left %s alive: %v", member.Name, err)
		}
		a, err := store.Get(ctx, member.ID)
		if err != nil || a.State.Live() {
			t.Fatalf("cancelled stop left %s counted live: %+v: %v", member.Name, a, err)
		}
	}
}

func newEcosystemProviderCLI(t *testing.T, script func(int) *projectCLICall) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	isolateCLIFixture(t)
	home := os.Getenv("HOME")
	projectCLIWrite(t, filepath.Join(home, ".kh", "config.json"), `{"auto":true,"memory_model":"off","memory_hybrid":false,"web_search":false,"map_cap":0}`)
	proxy, _ := projectCLIProxyScript(t, home, script)
	for key, value := range map[string]string{
		"KH_AUTO_REBUILD": "0", "KH_AUTO": "", "KH_PROVIDER": "codex", "KH_MODEL": "", "KH_EFFORT": "",
		"KH_PARENT": "", "KH_AGENT": "", "TMUX": "", "TMUX_PANE": "", "HTTPS_PROXY": proxy, "HTTP_PROXY": proxy,
		"ALL_PROXY": "", "NO_PROXY": "", "SSL_CERT_FILE": filepath.Join(home, "fixture-ca.pem"), "SSL_CERT_DIR": home,
		"GODEBUG": "x509sslcertoverrideplatform=1",
	} {
		t.Setenv(key, value)
	}
	return binary
}

func onlyEcosystemRoot(t *testing.T, store *ecosystem.Store) ecosystem.Agent {
	t.Helper()
	folder := filepath.Join(session.Folder(), "ecosystems")
	entries, err := os.ReadDir(folder)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one conversation sidecar: %v: %v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(folder, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if err := json.Unmarshal(data, &id); err != nil {
		t.Fatal(err)
	}
	a, err := store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestEcosystemStopCancelsRunningBashGroup(t *testing.T) {
	started, marker := filepath.Join(t.TempDir(), "bash-started"), filepath.Join(t.TempDir(), "must-not-appear")
	// The shell and sleep belong to Bash's separate process group. Without
	// cancellation propagation they survive kh and perform the delayed write.
	command := "sleep 4 & child=$!; printf '%s %s' \"$$\" \"$child\" > '" + started + "'; wait \"$child\"; printf orphan > '" + marker + "'"
	input, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	arguments := string(input)
	binary := newEcosystemProviderCLI(t, func(step int) *projectCLICall {
		if step == 0 {
			return &projectCLICall{Name: "bash", Arguments: arguments}
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	app := exec.CommandContext(ctx, binary, "-p", "run the fixture command")
	app.Stdin = strings.NewReader("")
	var output bytes.Buffer
	app.Stdout, app.Stderr = &output, &output
	if err := app.Start(); err != nil {
		t.Fatal(err)
	}
	defer app.Process.Kill()
	done := make(chan error, 1)
	go func() { done <- app.Wait() }()
	var processes []ecosystem.ProcessIdentity
	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, err := os.ReadFile(started); err == nil {
			fields := strings.Fields(string(b))
			if len(fields) == 2 {
				for _, field := range fields {
					pid, err := strconv.Atoi(field)
					if err != nil {
						t.Fatal(err)
					}
					p, err := ecosystem.ProcessForPID(pid)
					if err != nil {
						t.Fatal(err)
					}
					processes = append(processes, p)
				}
				break
			}
		}
		select {
		case err := <-done:
			t.Fatalf("app exited before fake-provider Bash started: %s: %v", output.String(), err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("fake-provider Bash did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	store, err := ecosystem.Open(ecosystem.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	root := onlyEcosystemRoot(t, store)
	if root.Process.PID != app.Process.Pid {
		t.Fatalf("wrong root process: %+v", root)
	}
	stop := exec.CommandContext(ctx, binary, "stop", root.Address)
	stop.Env = append(os.Environ(), "KH_ECOSYSTEM_ID="+root.EcosystemID, "KH_ECOSYSTEM_AGENT_ID="+root.ID)
	stop.Stdin = strings.NewReader("")
	if out, err := stop.CombinedOutput(); err != nil {
		t.Fatalf("stop running application: %s: %v", out, err)
	}
	<-done // A cancelled turn may return a nonzero exit status.
	for _, p := range processes {
		if alive, err := ecosystem.ProbeProcess(p); err != nil || alive {
			t.Fatalf("stop left Bash group member %d alive: %v", p.PID, err)
		}
	}
	// Wait past the command's original delayed write, not merely kh's exit.
	time.Sleep(4 * time.Second)
	if b, err := os.ReadFile(marker); !os.IsNotExist(err) {
		t.Fatalf("stopped Bash performed delayed side effect: %q: %v", b, err)
	}
}

func TestEcosystemPublishesDescendantResultsNotRootReplies(t *testing.T) {
	binary := newEcosystemProviderCLI(t, func(step int) *projectCLICall {
		if step == 1 { // Root completes first; the descendant promotes itself.
			return &projectCLICall{Name: "agents", Arguments: `{"action":"set_role","role":"manager"}`}
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	run := func(env []string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, "-p", "complete fixture task")
		cmd.Stdin = strings.NewReader("")
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "PROJECT-CLI-DONE") {
			t.Fatalf("synthetic successful response: %s: %v", out, err)
		}
		return string(out)
	}
	run(nil)
	store, err := ecosystem.Open(ecosystem.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	root := onlyEcosystemRoot(t, store)
	if root.Conclusion != "" {
		t.Fatalf("ordinary private root reply was published: %q", root.Conclusion)
	}
	// An independent synthetic live manager admits the actual descendant CLI;
	// the successful private user-root conversation above remains unrelated.
	process, err := ecosystem.CurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	manager, err := store.CreateRoot(ctx, ecosystem.RootOptions{Name: "fixture-manager", MaxAgents: 3, Process: process})
	if err != nil {
		t.Fatal(err)
	}
	r, err := store.Reserve(ctx, manager.ID, "result-worker", "shared task")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginLaunch(ctx, r.Agent.ID, r.Token, process); err != nil {
		t.Fatal(err)
	}
	run([]string{"KH_ECOSYSTEM_ID=" + manager.EcosystemID, "KH_ECOSYSTEM_AGENT_ID=" + r.Agent.ID, "KH_ECOSYSTEM_TOKEN=" + r.Token, "KH_PARENT=" + manager.Address})
	worker, err := store.Get(ctx, r.Agent.ID)
	if err != nil || worker.Role != ecosystem.Manager || !strings.Contains(worker.Conclusion, "PROJECT-CLI-DONE") {
		t.Fatalf("promoted descendant did not publish its shared result: %+v: %v", worker, err)
	}
}

// EOF-only startup exercises ecosystem sidecars without asking a provider to
// execute a turn. Legacy provider records remain readable and unchanged.
func TestEcosystemResumeCompatibility(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	isolateCLIFixture(t)
	t.Setenv("KH_AUTO_REBUILD", "0")
	for _, key := range []string{"KH_PROVIDER", "KH_MODEL", "KH_EFFORT", "KH_PARENT", "KH_AGENT", "KH_ECOSYSTEM_ID", "KH_ECOSYSTEM_AGENT_ID", "KH_ECOSYSTEM_TOKEN", "TMUX", "TMUX_PANE"} {
		t.Setenv(key, "")
	}
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".kh"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".kh", "config.json"), []byte(`{"memory_model":"off","memory_hybrid":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := ecosystem.Open(ecosystem.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run := func(t *testing.T, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Stdin = strings.NewReader("")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("EOF startup %v: %s: %v", args, out, err)
		}
	}
	for _, tc := range []struct{ id, saved string }{
		{"legacy", `{"session":"legacy","input":[]}`},
		{"envelope", `{"version":1,"provider":"codex","identity":{"role":"main","name":"saved-name"},"state":{"session":"envelope","input":[]}}`},
	} {
		t.Run(tc.id, func(t *testing.T) {
			if err := session.Save(tc.id, []byte(tc.saved)); err != nil {
				t.Fatal(err)
			}
			var firstID string
			for attempt := 0; attempt < 2; attempt++ {
				limit := 7 + attempt
				config := fmt.Sprintf(`{"memory_model":"off","memory_hybrid":false,"agents":{"max_live":%d}}`, limit)
				if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".kh", "config.json"), []byte(config), 0600); err != nil {
					t.Fatal(err)
				}
				run(t, "-s", tc.id)
				b, err := os.ReadFile(filepath.Join(session.Folder(), "ecosystems", tc.id+".json"))
				if err != nil {
					t.Fatal(err)
				}
				var rootID string
				if err := json.Unmarshal(b, &rootID); err != nil || rootID == "" {
					t.Fatalf("invalid saved ecosystem identity %q: %v", b, err)
				}
				if attempt == 0 {
					firstID = rootID
				} else if rootID != firstID {
					t.Fatalf("resume replaced ecosystem root: %s -> %s", firstID, rootID)
				}
				a, err := store.Get(context.Background(), rootID)
				if err != nil || a.ParentID != "" || a.Role != ecosystem.Manager {
					t.Fatalf("resumed root: %+v: %v", a, err)
				}
				stats, err := store.Stats(context.Background(), rootID)
				if err != nil || stats.MaxAgents != limit {
					t.Fatalf("root resume did not refresh shared limit to %d: %+v: %v", limit, stats, err)
				}
				unchanged, err := session.Load(tc.id)
				if err != nil || string(unchanged) != tc.saved {
					t.Fatalf("EOF startup rewrote private provider record: %s: %v", unchanged, err)
				}
			}
		})
	}
	// A separate new chat must not silently adopt the last resumed root.
	before, err := os.ReadDir(filepath.Join(session.Folder(), "ecosystems"))
	if err != nil {
		t.Fatal(err)
	}
	run(t)
	after, err := os.ReadDir(filepath.Join(session.Folder(), "ecosystems"))
	if err != nil || len(after) != len(before)+1 {
		t.Fatalf("new chat did not create its own ecosystem identity: before=%d after=%d: %v", len(before), len(after), err)
	}
}

// These fixtures run console helpers, never provider turns, on a private tmux
// socket. The real CLI still supplies command parsing, routing and stop logic.
type ecosystemCLI struct {
	t      *testing.T
	binary string
	socket string
	tmux   string
	helper string
}

func newEcosystemCLI(t *testing.T) *ecosystemCLI {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	isolateCLIFixture(t)
	for _, key := range []string{"KH_SOURCE_DIR", "KH_REBUILD_EXEC_PID", "KH_PROVIDER", "KH_MODEL", "KH_EFFORT", "KH_PARENT", "KH_AGENT", "KH_ECOSYSTEM_ID", "KH_ECOSYSTEM_AGENT_ID", "KH_ECOSYSTEM_TOKEN", "TMUX", "TMUX_PANE"} {
		t.Setenv(key, "")
	}
	t.Setenv("KH_AUTO_REBUILD", "0")
	// Even a regression that wrongly launches a worker cannot reach a model.
	t.Setenv("KH_PROVIDER", "ecosystem-fixture-no-provider")
	f := &ecosystemCLI{
		t: t, binary: binary,
		socket: fmt.Sprintf("kh_ecosystem_%d_%d", os.Getpid(), time.Now().UnixNano()),
		helper: "env KH_TMUX_INPUT_HELPER=1 KH_AUTO_REBUILD=0 '" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "' -test.run=^TestTmuxInputProcess$",
	}
	f.runTmux("new-session", "-d", "-s", "eco", "-n", "root", "-x", "240", "-y", "90", f.helper)
	t.Cleanup(func() {
		// The last stop may already have removed the final pane and server.
		_, _ = exec.Command("tmux", "-L", f.socket, "kill-server").CombinedOutput()
	})
	f.tmux = f.runTmux("display-message", "-p", "#{socket_path}") + ",1,0"
	return f
}

func (f *ecosystemCLI) runTmux(args ...string) string {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", append([]string{"-L", f.socket, "-f", "/dev/null"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("private tmux %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func (f *ecosystemCLI) console(window, name string) string {
	f.t.Helper()
	if window != "root" {
		f.runTmux("new-window", "-d", "-t", "eco", "-n", window, f.helper)
	}
	pane := f.runTmux("display-message", "-p", "-t", "eco:"+window+".0", "#{pane_id}")
	f.runTmux("set-option", "-p", "-t", pane, "@kh_name", name)
	f.waitOutput(pane, "CONSOLE_READY")
	return pane
}

func (f *ecosystemCLI) waitOutput(pane, text string) {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if strings.Contains(f.runTmux("capture-pane", "-p", "-t", pane), text) {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("console %s did not show %q", pane, text)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *ecosystemCLI) command(pane string, args ...string) (string, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.binary, args...)
	cmd.Env = append(os.Environ(), "TMUX="+f.tmux, "TMUX_PANE="+pane)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (f *ecosystemCLI) mustCommand(pane string, args ...string) string {
	f.t.Helper()
	out, err := f.command(pane, args...)
	if err != nil {
		f.t.Fatalf("kh %v from %s: %s: %v", args, pane, out, err)
	}
	return out
}
