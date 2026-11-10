package test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"kh/internal/delegation"
	"kh/internal/ecosystem"
	"kh/internal/session"
	"kh/internal/terminal"
)

// warmCLIFixture uses the real native tmux transport and registry. The manager
// console is a harmless test helper; workers execute the actual built CLI.
type warmCLIFixture struct {
	cli             *ecosystemCLI
	store           *ecosystem.Store
	root            ecosystem.Agent
	pane, directory string
}

func newWarmCLIFixture(t *testing.T, idleSeconds int) *warmCLIFixture {
	t.Helper()
	cli := newEcosystemCLI(t)
	projectCLIWrite(t, filepath.Join(os.Getenv("HOME"), ".kh/config.json"), fmt.Sprintf(`{"auto":true,"memory_model":"off","memory_hybrid":false,"web_search":false,"map_cap":0,"agents":{"max_live":5,"idle_timeout_sec":%d}}`, idleSeconds))
	pane := cli.console("root", "main")
	pid, err := strconv.Atoi(cli.runTmux("display-message", "-p", "-t", pane, "#{@kh_peer_input}"))
	if err != nil {
		t.Fatal(err)
	}
	process, err := ecosystem.ProcessForPID(pid)
	if err != nil {
		t.Fatal(err)
	}
	store, err := ecosystem.Open(ecosystem.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	root, err := store.CreateRoot(context.Background(), ecosystem.RootOptions{Name: "warm-manager", MaxAgents: 5, Process: process, Pane: ecosystem.PaneInfo{PaneID: pane, SessionID: cli.runTmux("display-message", "-p", "-t", pane, "#{session_id}")}})
	if err != nil {
		t.Fatal(err)
	}
	cli.runTmux("set-option", "-p", "-t", pane, "@kh_ecosystem", root.EcosystemID, ";", "set-option", "-p", "-t", pane, "@kh_agent_id", root.ID)
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	return &warmCLIFixture{cli, store, root, pane, directory}
}

func warmHandoffJSON(t *testing.T, h delegation.Handoff) string {
	t.Helper()
	if err := h.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (f *warmCLIFixture) waitTask(t *testing.T, address string, generation int64) (ecosystem.Agent, ecosystem.TaskStatus) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last ecosystem.Agent
	var status ecosystem.TaskStatus
	for time.Now().Before(deadline) {
		a, err := f.store.Resolve(context.Background(), f.root.EcosystemID, address)
		if err == nil {
			last = a
			status, err = f.store.TaskStatus(context.Background(), a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if status.Lease.Generation == generation && status.Status == "completed" && a.State == ecosystem.Idle {
				return a, status
			}
			if !a.State.Live() {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	output := ""
	if last.Pane.PaneID != "" {
		output = f.cli.runTmux("capture-pane", "-p", "-S", "-300", "-t", last.Pane.PaneID)
	}
	t.Fatalf("worker never completed generation %d: agent=%+v status=%+v output=%s", generation, last, status, output)
	return last, status
}

func warmGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "GIT_") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func warmPushFixture(t *testing.T, f *warmCLIFixture) (string, delegation.Handoff) {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	warmGit(t, f.directory, "init", "--bare", remote)
	warmGit(t, f.directory, "init", "--initial-branch=main")
	warmGit(t, f.directory, "config", "user.name", "Fixture")
	warmGit(t, f.directory, "config", "user.email", "fixture@example.invalid")
	projectCLIWrite(t, filepath.Join(f.directory, "file"), "initial")
	warmGit(t, f.directory, "add", "file")
	warmGit(t, f.directory, "commit", "-m", "initial")
	warmGit(t, f.directory, "remote", "add", "origin", remote)
	warmGit(t, f.directory, "push", "--set-upstream", "origin", "main")
	projectCLIWrite(t, filepath.Join(f.directory, "file"), "verified")
	warmGit(t, f.directory, "commit", "-am", "verified")
	commit := warmGit(t, f.directory, "rev-parse", "HEAD")
	h := delegation.Handoff{Version: 1, Task: "COLD-VERIFIED-PUSH", Directory: f.directory, Workflow: delegation.WorkflowPushVerified, Verification: delegation.Verification{Commit: commit, Branch: "main", Upstream: "origin/main", References: []string{"fixture: prior verification"}}}
	return remote, h
}

func TestWarmWorkerNativeVerifiedPushAndIdleRetirement(t *testing.T) {
	f := newWarmCLIFixture(t, 3)
	remote, h := warmPushFixture(t, f)
	commit := h.Verification.Commit
	// The inherited provider deliberately does not exist, and no login is
	// installed: success proves the structured path does not invoke a model.
	coldStart := time.Now()
	f.cli.mustCommand(f.pane, "spawn", "--reuse", "--handoff", "push-worker", warmHandoffJSON(t, h))
	first, status := f.waitTask(t, "kh:push-worker", 1)
	coldWall := time.Since(coldStart)
	if !status.Reusable || !strings.Contains(first.Conclusion, commit) || !strings.Contains(first.Conclusion, "origin/main") {
		t.Fatalf("missing verified conclusion: %+v %+v", first, status)
	}
	if got := warmGit(t, f.directory, "--git-dir="+remote, "rev-parse", "refs/heads/main"); got != commit {
		t.Fatalf("remote=%s want=%s", got, commit)
	}
	h.Task = "WARM-VERIFIED-PUSH"
	warmStart := time.Now()
	f.cli.mustCommand(f.pane, "assign", first.Address, warmHandoffJSON(t, h))
	second, next := f.waitTask(t, first.Address, 2)
	warmWall := time.Since(warmStart)
	t.Logf("local no-rebuild driver request→confirmed result: cold=%s warm=%s; cold phases=%+v warm phases=%+v", coldWall, warmWall, status.Timings, next.Timings)
	if second.Process != first.Process || second.ID != first.ID || next.Lease.ID == status.Lease.ID {
		t.Fatalf("assignment did not reuse process with fresh lease: before=%+v/%+v after=%+v/%+v", first, status, second, next)
	}
	var inspection struct {
		TaskStatus ecosystem.TaskStatus `json:"task_status"`
	}
	out := f.cli.mustCommand(f.pane, "inspect", first.Address)
	if err := json.Unmarshal([]byte(out), &inspection); err != nil || inspection.TaskStatus.Lease != next.Lease || inspection.TaskStatus.Timings.ExecutionMS < 0 {
		t.Fatalf("inspect timing/lease output: %s: %v", out, err)
	}
	pane := f.cli.runTmux("capture-pane", "-p", "-S", "-300", "-t", second.Pane.PaneID)
	for _, want := range []string{"COLD-VERIFIED-PUSH", "WARM-VERIFIED-PUSH", "ASSIGNED TASK", "(cold)", "(validated)", "(task timing)"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("missing %q in task banners: %s", want, pane)
		}
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		f.cli.mustCommand(f.pane, "stats") // reconcile physical exit before freed capacity
		a, err := f.store.Get(context.Background(), first.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !a.State.Live() {
			if _, err := f.cli.command(f.pane, "assign", first.Address, warmHandoffJSON(t, h)); err == nil {
				t.Fatal("retired worker accepted assignment")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("idle worker did not retire and release capacity")
}

func TestWarmWorkerDeniedPushAndStructuredKeepRejected(t *testing.T) {
	f := newWarmCLIFixture(t, 30)
	remote, h := warmPushFixture(t, f)
	prior := warmGit(t, f.directory, "--git-dir="+remote, "rev-parse", "refs/heads/main")
	before, err := f.store.List(context.Background(), f.root.EcosystemID)
	if err != nil {
		t.Fatal(err)
	}
	panes := f.cli.runTmux("list-panes", "-a", "-F", "#{pane_id}")
	if out, err := f.cli.command(f.pane, "spawn", "--keep", "--handoff", "invalid-keep", warmHandoffJSON(t, h)); err == nil {
		t.Fatalf("structured keep unexpectedly spawned: %s", out)
	}
	after, err := f.store.List(context.Background(), f.root.EcosystemID)
	if err != nil || len(after) != len(before) || f.cli.runTmux("list-panes", "-a", "-F", "#{pane_id}") != panes {
		t.Fatal("rejected keep allocated a process/pane or registry reservation")
	}
	projectCLIWrite(t, filepath.Join(os.Getenv("HOME"), ".kh/config.json"), `{"auto":false,"memory_model":"off","memory_hybrid":false,"agents":{"max_live":5,"idle_timeout_sec":30}}`)
	h.Task = "DENIED-VERIFIED-PUSH"
	f.cli.mustCommand(f.pane, "--auto=false", "spawn", "--reuse", "--handoff", "denied-worker", warmHandoffJSON(t, h))
	worker, err := f.store.Resolve(context.Background(), f.root.EcosystemID, "kh:denied-worker")
	if err != nil {
		t.Fatal(err)
	}
	f.cli.waitOutput(worker.Pane.PaneID, "run it? [y/N]")
	f.cli.runTmux("send-keys", "-t", worker.Pane.PaneID, "n", "Enter")
	worker, _ = f.waitTask(t, worker.Address, 1)
	if !strings.Contains(strings.ToLower(worker.Conclusion), "verified push declined") {
		t.Fatalf("denial was not reported: %+v", worker)
	}
	if actual := warmGit(t, f.directory, "--git-dir="+remote, "rev-parse", "refs/heads/main"); actual != prior {
		t.Fatalf("denied approval changed remote: %s -> %s", prior, actual)
	}
	f.cli.mustCommand(f.pane, "stop", worker.Address)
}

func TestWarmWorkerFreshHistoryGuidanceAndDelegatedInput(t *testing.T) {
	f := newWarmCLIFixture(t, 30)
	projectCLIWrite(t, filepath.Join(os.Getenv("HOME"), ".kh/config.json"), `{"system":"CUSTOM-SYSTEM","auto":true,"memory_model":"off","memory_hybrid":false,"web_search":false,"map_cap":0,"agents":{"max_live":5,"idle_timeout_sec":30}}`)
	proxy, requests := projectCLIProxyScript(t, os.Getenv("HOME"), func(int) *projectCLICall { return nil })
	for key, value := range map[string]string{"KH_PROVIDER": "codex", "HTTPS_PROXY": proxy, "HTTP_PROXY": proxy, "ALL_PROXY": "", "NO_PROXY": "", "SSL_CERT_FILE": projectCLIProxyCA(os.Getenv("HOME"), proxy), "SSL_CERT_DIR": filepath.Dir(projectCLIProxyCA(os.Getenv("HOME"), proxy)), "GODEBUG": "x509sslcertoverrideplatform=1"} {
		t.Setenv(key, value)
	}
	// The private tmux server predates these variables; publish the synthetic
	// provider environment so its native split-window children inherit it.
	for _, key := range []string{"KH_PROVIDER", "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR", "GODEBUG"} {
		f.cli.runTmux("set-environment", "-g", key, os.Getenv(key))
	}
	guidance := filepath.Join(f.directory, "AGENTS.md")
	projectCLIWrite(t, guidance, "FIRST-FOUNDATION")
	h := delegation.Handoff{Version: 1, Task: "FIRST-PRIVATE-OBJECTIVE", Directory: f.directory, Constraints: []string{"No parent history"}}
	f.cli.mustCommand(f.pane, "spawn", "--reuse", "--handoff", "fresh-worker", warmHandoffJSON(t, h))
	first, status := f.waitTask(t, "kh:fresh-worker", 1)
	receive := func() projectCLIRequest {
		t.Helper()
		select {
		case request := <-requests:
			return request
		case <-time.After(5 * time.Second):
			t.Fatal("provider never received task")
			return projectCLIRequest{}
		}
	}
	request1 := receive()
	assertMemoryPolicy(t, request1)
	if !strings.Contains(request1.Instructions, "FIRST-FOUNDATION") {
		t.Fatal("cold task lacked applicable guidance")
	}
	// Idle text is not an assignment and must not become a later user turn.
	f.cli.runTmux("send-keys", "-l", "-t", first.Pane.PaneID, "STALE-ORDINARY-INPUT")
	f.cli.runTmux("send-keys", "-t", first.Pane.PaneID, "Enter")
	info, err := os.Stat(guidance)
	if err != nil {
		t.Fatal(err)
	}
	projectCLIWrite(t, guidance, "OTHER-FOUNDATION") // same byte count and timestamp
	if err := os.Chtimes(guidance, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	h.Task = "SECOND-PRIVATE-OBJECTIVE"
	f.cli.mustCommand(f.pane, "assign", first.Address, warmHandoffJSON(t, h))
	second, next := f.waitTask(t, first.Address, 2)
	request2 := receive()
	assertMemoryPolicy(t, request2)
	if second.Process != first.Process || next.Lease.ID == status.Lease.ID {
		t.Fatal("warm task did not retain process with fresh task identity")
	}
	if !strings.Contains(request2.Instructions, "OTHER-FOUNDATION") || strings.Contains(request2.Instructions, "FIRST-FOUNDATION") {
		t.Fatal("warm task retained stale binding guidance")
	}
	input, err := json.Marshal(request2.Input)
	if err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{"FIRST-PRIVATE-OBJECTIVE", "PROJECT-CLI-DONE", "STALE-ORDINARY-INPUT"} {
		if strings.Contains(string(input), stale) {
			t.Fatalf("fresh history contains %s: %s", stale, input)
		}
	}
	if !strings.Contains(string(input), "SECOND-PRIVATE-OBJECTIVE") || !strings.Contains(string(input), "not a direct user message") {
		t.Fatalf("missing delegated handoff: %s", input)
	}
	select {
	case unexpected := <-requests:
		t.Fatalf("ordinary/stale input caused extra model request: %+v", unexpected)
	default:
	}
	// Replay an earlier assignment control frame. The database lease, not the
	// transport sender or a generation label alone, must fence the old task.
	payload, _ := json.Marshal(map[string]any{"lease": status.Lease, "handoff": h})
	staleFile, err := os.CreateTemp(filepath.Join(os.Getenv("HOME"), ".kh/handoffs"), "task-"+first.ID+"-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := staleFile.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := staleFile.Close(); err != nil {
		t.Fatal(err)
	}
	sendReceipt := func(name string) {
		t.Helper()
		receipt, _ := json.Marshal(map[string]string{"file": name})
		frame, err := terminal.TaskFrame(terminal.TaskControl{Kind: "assign", Generation: status.Lease.Generation, Payload: receipt})
		if err != nil {
			t.Fatal(err)
		}
		keys := []string{"send-keys", "-H", "-t", first.Pane.PaneID}
		for _, b := range []byte(frame) {
			keys = append(keys, fmt.Sprintf("%02x", b))
		}
		f.cli.runTmux(keys...)
	}
	sendReceipt(filepath.Base(staleFile.Name()))
	f.cli.waitOutput(first.Pane.PaneID, "stale task assignment rejected")
	if _, err := os.Lstat(staleFile.Name()); !os.IsNotExist(err) {
		t.Fatalf("stale task receipt not consumed/deleted: %v", err)
	}
	unchanged, err := f.store.TaskStatus(context.Background(), first.ID)
	if err != nil || unchanged.Lease != next.Lease || unchanged.Status != "completed" {
		t.Fatalf("stale control changed current lease: %+v %v", unchanged, err)
	}
	select {
	case extra := <-requests:
		t.Fatalf("stale control invoked provider: %+v", extra)
	default:
	}
	archives, err := filepath.Glob(filepath.Join(session.Folder(), "*.jsonl"))
	if err != nil || len(archives) != 2 {
		t.Fatalf("tasks did not create separate session archives: %v %v", archives, err)
	}
	for _, path := range archives {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		messages := 0
		err = session.ReadArchive(strings.NewReader(string(data)), func(record session.ArchiveRecord) error {
			if record.Type == "user_message" {
				messages++
				if record.Origin != "delegated" || strings.Contains(record.Text, "STALE-ORDINARY-INPUT") {
					t.Fatalf("task input lost delegated provenance: %+v", record)
				}
			}
			return nil
		})
		if err != nil || messages != 1 {
			t.Fatalf("archive %s messages=%d: %v", path, messages, err)
		}
	}
	// Bad receipt paths and non-regular/oversized/wrong-mode payloads must
	// leave the current completed lease untouched and never block the worker.
	handoffDir := filepath.Join(os.Getenv("HOME"), ".kh/handoffs")
	prefix := "task-" + first.ID + "-"
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(handoffDir, prefix+"symlink.json")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(handoffDir, prefix+"fifo.json"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handoffDir, prefix+"mode.json"), payload, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handoffDir, prefix+"oversize.json"), []byte(strings.Repeat("x", delegation.MaxHandoffBytes*8+1)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{prefix + "symlink.json", prefix + "fifo.json", prefix + "mode.json", prefix + "oversize.json", "../" + prefix + "traversal.json"} {
		sendReceipt(name)
	}
	time.Sleep(100 * time.Millisecond)
	if unchanged, err := f.store.TaskStatus(context.Background(), first.ID); err != nil || unchanged.Lease != next.Lease || unchanged.Status != "completed" {
		t.Fatalf("malicious receipt changed current task: %+v %v", unchanged, err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != string(payload) {
		t.Fatal("receipt followed/deleted outside symlink target")
	}
	// A staging failure must roll back the pending lease instead of stranding
	// this live worker in Running forever. No assignment frame was delivered.
	if err := os.Chmod(handoffDir, 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := f.cli.command(f.pane, "assign", first.Address, warmHandoffJSON(t, h)); err == nil {
		t.Fatalf("insecure staging directory unexpectedly accepted: %s", out)
	}
	cancelled, err := f.store.TaskStatus(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	cancelledAgent, err := f.store.Get(context.Background(), first.ID)
	if err != nil || cancelledAgent.State != ecosystem.Idle || cancelled.Status != "completed" || cancelled.Lease.Generation != next.Lease.Generation+1 {
		t.Fatalf("staging error stranded reservation: %+v %+v %v", cancelledAgent, cancelled, err)
	}
	beforeAgents, err := f.store.List(context.Background(), f.root.EcosystemID)
	if err != nil {
		t.Fatal(err)
	}
	beforeCapacity, err := f.store.Stats(context.Background(), f.root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := f.cli.command(f.pane, "spawn", "--reuse", "--handoff", "must-not-cold-spawn", warmHandoffJSON(t, h)); err == nil {
		t.Fatalf("failed reuse unexpectedly fell back to spawning: %s", out)
	}
	afterAgents, err := f.store.List(context.Background(), f.root.EcosystemID)
	if err != nil {
		t.Fatal(err)
	}
	afterCapacity, err := f.store.Stats(context.Background(), f.root.ID)
	if err != nil || len(afterAgents) != len(beforeAgents) || afterCapacity.Live != beforeCapacity.Live || afterCapacity.Available != beforeCapacity.Available {
		t.Fatalf("failed reuse allocated new worker/capacity: agents %d->%d stats %+v->%+v err=%v", len(beforeAgents), len(afterAgents), beforeCapacity, afterCapacity, err)
	}
	cancelled, err = f.store.TaskStatus(context.Background(), first.ID)
	if err != nil || cancelled.Status != "completed" {
		t.Fatalf("failed reuse left pending task: %+v %v", cancelled, err)
	}
	if err := os.Chmod(handoffDir, 0700); err != nil {
		t.Fatal(err)
	}
	// Assignment and peer-control transport must remain intact when a maximum
	// size objective races independent send commands from the same manager.
	h.Task = "LONG-OBJECTIVE-" + strings.Repeat("x", delegation.MaxTaskBytes-len("LONG-OBJECTIVE--END")) + "-END"
	type commandResult struct {
		out string
		err error
	}
	assigned := make(chan commandResult, 1)
	longJSON := warmHandoffJSON(t, h)
	go func() {
		out, err := f.cli.command(f.pane, "assign", first.Address, longJSON)
		assigned <- commandResult{out, err}
	}()
	for i := 0; i < 8; i++ {
		if out, err := f.cli.command(f.pane, "send", first.Address, fmt.Sprintf("CONCURRENT-PEER-%d", i)); err != nil {
			t.Fatalf("peer send: %s %v", out, err)
		}
	}
	if result := <-assigned; result.err != nil {
		t.Fatalf("long assignment: %s %v", result.out, result.err)
	}
	f.waitTask(t, first.Address, cancelled.Lease.Generation+1)
	foundLong := false
	for {
		select {
		case request := <-requests:
			input, _ := json.Marshal(request.Input)
			foundLong = foundLong || strings.Contains(string(input), h.Task)
		default:
			if !foundLong {
				t.Fatal("long concurrent assignment was lost or corrupted")
			}
			f.cli.mustCommand(f.pane, "stop", first.Address)
			leftovers, err := filepath.Glob(filepath.Join(handoffDir, prefix+"*.json"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("worker retirement left task receipts: %v %v", leftovers, err)
			}
			return
		}
	}
}
