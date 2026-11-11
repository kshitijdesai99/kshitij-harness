package visual_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"kh/internal/ecosystem"
	"kh/internal/visual"
)

func sharedManager(t *testing.T, home, uv string, owner ecosystem.ProcessIdentity) *visual.Manager {
	t.Helper()
	t.Setenv("KH_VISUAL_TEST_ROLE", "guardian")
	m, err := visual.New(visual.Options{Owner: owner, Home: home, UV: uv, Guardian: guardianCommand(), StartupTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	return m
}
func workerCommand(home, uv, result string, owner ecosystem.ProcessIdentity) *exec.Cmd {
	encoded, _ := json.Marshal(owner)
	command := exec.Command(os.Args[0], "-test.run=^TestVisualProcess$")
	command.Env = append(os.Environ(), "KH_VISUAL_TEST_ROLE=owner", "KH_VISUAL_TEST_HOME="+home, "KH_VISUAL_TEST_UV="+uv, "KH_VISUAL_TEST_RESULT="+result, "KH_VISUAL_TEST_OWNER="+string(encoded), "KH_VISUAL_TEST_EXIT=1")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.Stderr = os.Stderr
	return command
}
func readResult(t *testing.T, result string) visual.Server {
	t.Helper()
	var server visual.Server
	eventually(t, "worker failed to start", func() bool {
		data, err := os.ReadFile(result)
		return err == nil && json.Unmarshal(data, &server) == nil
	})
	return server
}
func TestSharedServersSurviveWorkerExitAndPaneHangup(t *testing.T) {
	owner, err := ecosystem.CurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	uv := fakeUV(t)
	for _, mode := range []string{"close", "os-exit", "sigkill", "sigterm", "pane-sighup"} {
		t.Run(mode, func(t *testing.T) {
			home, result := t.TempDir(), filepath.Join(t.TempDir(), "result.json")
			root := sharedManager(t, home, uv, owner)
			command := workerCommand(home, uv, result, owner)
			if mode == "close" {
				command.Env = append(command.Env, "KH_VISUAL_TEST_CLOSE=1")
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer command.Process.Kill()
			server := readResult(t, result)
			child := childPID(t, root.Root())
			switch mode {
			case "close", "os-exit":
				os.WriteFile(result+".exit", nil, 0600)
			case "sigkill":
				command.Process.Kill()
			case "sigterm":
				command.Process.Signal(syscall.SIGTERM)
			case "pane-sighup":
				syscall.Kill(-command.Process.Pid, syscall.SIGHUP)
			}
			command.Wait()
			// Wait beyond guardian polling/teardown so immediate health cannot
			// conceal a queued owner-EOF or terminal signal cleanup.
			time.Sleep(500 * time.Millisecond)
			if !serving(server) || !alive(child) {
				t.Fatal("worker exit killed root-owned server/descendant")
			}
			listed := root.List()
			if len(listed) != 1 || !listed[0].Running || listed[0].Notebook != server.Notebook {
				t.Fatal(listed)
			}
			if err := root.Stop(server.Name); err != nil {
				t.Fatal(err)
			}
			eventually(t, "explicit root stop leaked listener", func() bool { return !serving(server) })
			eventually(t, "explicit root stop leaked descendant", func() bool { return !alive(child) })
			if _, err := os.Stat(server.Notebook); err != nil {
				t.Fatal("stop removed notebook", err)
			}
		})
	}
}

// A real root process launches a worker, lets that worker exit, then waits for
// graceful close or an external signal. No live worker owns the guardian pipe.
func runSharedRoot() {
	owner, err := ecosystem.CurrentProcess()
	if err != nil {
		os.Exit(10)
	}
	home, uv, result := os.Getenv("KH_VISUAL_TEST_HOME"), os.Getenv("KH_VISUAL_TEST_UV"), os.Getenv("KH_VISUAL_TEST_RESULT")
	m, err := visual.New(visual.Options{Home: home, Owner: owner, UV: uv, Guardian: guardianCommand()})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(11)
	}
	os.WriteFile(result+".root", []byte(m.Root()), 0600)
	worker := workerCommand(home, uv, result, owner)
	worker.Env = append(worker.Env, "KH_VISUAL_TEST_CLOSE=1")
	if err := worker.Start(); err != nil {
		os.Exit(12)
	}
	for {
		if _, err := os.Stat(result); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	os.WriteFile(result+".exit", nil, 0600)
	worker.Wait()
	os.WriteFile(result+".ready", nil, 0600)
	for {
		if _, err := os.Stat(result + ".root-exit"); err == nil {
			if os.Getenv("KH_VISUAL_TEST_CLOSE") == "1" {
				if err := m.Close(); err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(13)
				}
			}
			os.Exit(0)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
func TestSharedRootExitCleansWorkerServers(t *testing.T) {
	uv := fakeUV(t)
	sibling := manager(t, uv)
	sibling.Create("sibling")
	other, err := sibling.Start(context.Background(), "sibling", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"close", "os-exit", "sigterm", "sigkill", "sighup"} {
		t.Run(mode, func(t *testing.T) {
			result := filepath.Join(t.TempDir(), "result.json")
			root := exec.Command(os.Args[0], "-test.run=^TestVisualProcess$")
			root.Env = append(os.Environ(), "KH_VISUAL_TEST_ROLE=root", "KH_VISUAL_TEST_HOME="+t.TempDir(), "KH_VISUAL_TEST_UV="+uv, "KH_VISUAL_TEST_RESULT="+result)
			if mode == "close" {
				root.Env = append(root.Env, "KH_VISUAL_TEST_CLOSE=1")
			}
			root.Stderr = os.Stderr
			if err := root.Start(); err != nil {
				t.Fatal(err)
			}
			defer root.Process.Kill()
			server := readResult(t, result)
			eventually(t, "worker did not exit", func() bool { _, err := os.Stat(result + ".ready"); return err == nil })
			if !serving(server) {
				t.Fatal("worker Close killed root server")
			}
			child := childPID(t, filepath.Dir(server.Notebook))
			switch mode {
			case "close", "os-exit":
				os.WriteFile(result+".root-exit", nil, 0600)
			case "sigterm":
				root.Process.Signal(syscall.SIGTERM)
			case "sigkill":
				root.Process.Kill()
			case "sighup":
				root.Process.Signal(syscall.SIGHUP)
			}
			root.Wait()
			eventually(t, "root exit leaked listener", func() bool { return !serving(server) })
			eventually(t, "root exit leaked descendant", func() bool { return !alive(child) })
			if !serving(other) {
				t.Fatal("root exit killed independent session")
			}
		})
	}
}
func TestSharedPendingStartupCancellationAndRootClose(t *testing.T) {
	owner, err := ecosystem.CurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	uv := fakeUV(t)
	for _, closeClient := range []string{"same", "peer"} {
		t.Run(closeClient, func(t *testing.T) {
			home := t.TempDir()
			first := sharedManager(t, home, uv, owner)
			peer := sharedManager(t, home, uv, owner)
			first.Create("pending")
			started := make(chan error, 1)
			go func() { _, err := first.Start(context.Background(), "pending", 0); started <- err }()
			eventually(t, "pending startup did not launch", func() bool { _, err := os.Stat(filepath.Join(first.Root(), "child.pid")); return err == nil })
			child := childPID(t, first.Root())
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			before := time.Now()
			if _, err := peer.Start(ctx, "pending", 0); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("lock ignored context", err)
			}
			if time.Since(before) > time.Second {
				t.Fatal("canceled lock waited for startup")
			}
			closer := first
			if closeClient == "peer" {
				closer = peer
			}
			before = time.Now()
			if err := closer.Close(); err != nil {
				t.Fatal(err)
			}
			if time.Since(before) > 2*time.Second {
				t.Fatal("root Close blocked behind dependency startup")
			}
			if err := <-started; err == nil {
				t.Fatal("closed root committed startup")
			}
			eventually(t, "root Close leaked pending descendant", func() bool { return !alive(child) })
			if len(first.List()) != 0 {
				t.Fatal("pending startup became discoverable")
			}
		})
	}
	t.Run("context", func(t *testing.T) {
		m := sharedManager(t, t.TempDir(), uv, owner)
		m.Create("pending")
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		if _, err := m.Start(ctx, "pending", 0); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		child := childPID(t, m.Root())
		eventually(t, "startup cancellation leaked descendant", func() bool { return !alive(child) })
		if len(m.List()) != 0 {
			t.Fatal("canceled startup retained manifest")
		}
	})
}
func TestSharedWorkerExitDuringStartupDoesNotTransfer(t *testing.T) {
	owner, err := ecosystem.CurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	uv, home := fakeUV(t), t.TempDir()
	root := sharedManager(t, home, uv, owner)
	command := workerCommand(home, uv, filepath.Join(t.TempDir(), "unused.json"), owner)
	command.Env = append(command.Env, "KH_VISUAL_TEST_NOTEBOOK=pending")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer command.Process.Kill()
	eventually(t, "worker did not begin startup", func() bool { _, err := os.Stat(filepath.Join(root.Root(), "child.pid")); return err == nil })
	child := childPID(t, root.Root())
	command.Process.Kill()
	command.Wait()
	eventually(t, "worker died before commit but leaked descendant", func() bool { return !alive(child) })
	if len(root.List()) != 0 {
		t.Fatal("failed startup transferred ownership")
	}
}
func TestSharedRootExitDuringWorkerStartup(t *testing.T) {
	uv, result := fakeUV(t), filepath.Join(t.TempDir(), "result.json")
	root := exec.Command(os.Args[0], "-test.run=^TestVisualProcess$")
	root.Env = append(os.Environ(), "KH_VISUAL_TEST_ROLE=root", "KH_VISUAL_TEST_HOME="+t.TempDir(), "KH_VISUAL_TEST_UV="+uv, "KH_VISUAL_TEST_RESULT="+result, "KH_VISUAL_TEST_NOTEBOOK=pending")
	root.Stderr = os.Stderr
	if err := root.Start(); err != nil {
		t.Fatal(err)
	}
	defer root.Process.Kill()
	var artifact string
	eventually(t, "root did not begin startup", func() bool {
		data, err := os.ReadFile(result + ".root")
		if err != nil {
			return false
		}
		artifact = string(data)
		_, err = os.Stat(filepath.Join(artifact, "child.pid"))
		return err == nil
	})
	child := childPID(t, artifact)
	root.Process.Kill()
	root.Wait()
	eventually(t, "root exit during startup leaked descendant", func() bool { return !alive(child) })
}
func TestGuardianRejectsReusedOwnerPID(t *testing.T) {
	owner, err := ecosystem.CurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	owner.StartToken += "-reused"
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	command := exec.Command(guardianCommand()[0], guardianCommand()[1:]...)
	command.Env = append(os.Environ(), "KH_VISUAL_TEST_ROLE=guardian")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.ExtraFiles = []*os.File{read}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer command.Process.Kill()
	read.Close()
	root := t.TempDir()
	spec := map[string]any{"Argv": []string{"/bin/sleep", "600"}, "Dir": root, "Env": os.Environ(), "Owner": owner, "Stop": filepath.Join(root, "stop")}
	encoder := json.NewEncoder(write)
	if err := encoder.Encode(spec); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Encode(true); err != nil {
		t.Fatal(err)
	}
	write.Close()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case <-done: // same PID remains alive, but its start identity is not the owner
	case <-time.After(3 * time.Second):
		t.Fatal("guardian trusted reused PID without checking start identity")
	}
}
func TestSharedRootsWithSameHomeStayIsolated(t *testing.T) {
	uv, home := fakeUV(t), t.TempDir()
	roots := make([]*exec.Cmd, 2)
	results := make([]string, 2)
	servers := make([]visual.Server, 2)
	for i := range roots {
		results[i] = filepath.Join(t.TempDir(), "result.json")
		root := exec.Command(os.Args[0], "-test.run=^TestVisualProcess$")
		root.Env = append(os.Environ(), "KH_VISUAL_TEST_ROLE=root", "KH_VISUAL_TEST_HOME="+home, "KH_VISUAL_TEST_UV="+uv, "KH_VISUAL_TEST_RESULT="+results[i], "KH_VISUAL_TEST_CLOSE=1")
		root.Stderr = os.Stderr
		if err := root.Start(); err != nil {
			t.Fatal(err)
		}
		defer root.Process.Kill()
		roots[i] = root
		servers[i] = readResult(t, results[i])
		eventually(t, "root not ready", func() bool { _, err := os.Stat(results[i] + ".ready"); return err == nil })
	}
	if filepath.Dir(servers[0].Notebook) == filepath.Dir(servers[1].Notebook) {
		t.Fatal("independent roots share server namespace")
	}
	for i, root := range roots {
		os.WriteFile(results[i]+".root-exit", nil, 0600)
		if err := root.Wait(); err != nil {
			t.Fatal(err)
		}
		eventually(t, "graceful root exit leaked server", func() bool { return !serving(servers[i]) })
		if i == 0 && !serving(servers[1]) {
			t.Fatal("root close killed independent root server")
		}
	}
}
func TestSharedOwnerIdentityFenceAndClose(t *testing.T) {
	owner, err := ecosystem.CurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	stale := owner
	stale.StartToken += "-stale"
	if _, err := visual.New(visual.Options{Home: t.TempDir(), Owner: stale}); err == nil {
		t.Fatal("accepted reused PID with wrong start identity")
	}
	uv, home := fakeUV(t), t.TempDir()
	first := sharedManager(t, home, uv, owner)
	second := sharedManager(t, home, uv, owner)
	if first.Root() != second.Root() {
		t.Fatal("root clients do not share artifacts")
	}
	first.Create("shared")
	server, err := first.Start(context.Background(), "shared", 0)
	if err != nil {
		t.Fatal(err)
	}
	again, err := second.Start(context.Background(), "shared", 0)
	if err != nil || again.URL != server.URL {
		t.Fatal("duplicate shared launch", err)
	}
	if err := second.Stop("shared"); err != nil {
		t.Fatal(err)
	}
	if serving(server) {
		t.Fatal("sibling stop did not stop server")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Start(context.Background(), "shared", 0); err == nil {
		t.Fatal("worker reopened closed root")
	}
}
