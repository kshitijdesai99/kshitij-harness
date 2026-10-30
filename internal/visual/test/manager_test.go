package visual_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"kh/internal/visual"
)

// The test executable supplies both guardian and fake uv, with no downloads.
func TestVisualProcess(t *testing.T) {
	switch os.Getenv("KH_VISUAL_TEST_ROLE") {
	case "guardian":
		if err := visual.GuardianMain(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	case "server":
		joined := " " + strings.Join(os.Args, " ") + " "
		if !strings.Contains(joined, " run --no-project --with marimo --with-requirements ") || strings.Contains(joined, " --sandbox ") || !strings.Contains(joined, " --no-sandbox ") || !strings.Contains(joined, " marimo run ") || !strings.Contains(joined, " --watch ") {
			fmt.Fprintln(os.Stderr, "unowned marimo launch:", os.Args)
			os.Exit(7)
		}
		port := ""
		for i, arg := range os.Args {
			if arg == "--port" && i+1 < len(os.Args) {
				port = os.Args[i+1]
			}
		}
		environment := map[string]string{}
		for _, key := range []string{"UV_CACHE_DIR", "TMPDIR", "XDG_CONFIG_HOME", "MPLCONFIGDIR"} {
			environment[key] = os.Getenv(key)
		}
		data, _ := json.Marshal(environment)
		os.WriteFile("environment.json", data, 0600)
		child := exec.Command("/bin/sleep", "600")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		os.WriteFile("child.pid", []byte(strconv.Itoa(child.Process.Pid)), 0600)
		for _, arg := range os.Args {
			if strings.HasSuffix(arg, "/fail.py") {
				os.Exit(23)
			}
			if strings.HasSuffix(arg, "/pending.py") {
				for {
					time.Sleep(time.Hour)
				}
			}
		}
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "fake marimo") })
		if err := http.ListenAndServe("127.0.0.1:"+port, nil); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	case "owner":
		manager, err := visual.New(visual.Options{Home: os.Getenv("KH_VISUAL_TEST_HOME"), UV: os.Getenv("KH_VISUAL_TEST_UV"), Guardian: guardianCommand(), StartupTimeout: 5 * time.Second})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(4)
		}
		if _, err = manager.Create("owned"); err != nil {
			os.Exit(5)
		}
		os.Setenv("KH_VISUAL_TEST_ROLE", "guardian")
		server, err := manager.Start(context.Background(), "owned", 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(6)
		}
		data, _ := json.Marshal(server)
		os.WriteFile(os.Getenv("KH_VISUAL_TEST_RESULT"), data, 0600)
		for {
			if os.Getenv("KH_VISUAL_TEST_EXIT") == "1" {
				if _, err := os.Stat(os.Getenv("KH_VISUAL_TEST_RESULT") + ".exit"); err == nil {
					os.Exit(17)
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}
func guardianCommand() []string { return []string{os.Args[0], "-test.run=^TestVisualProcess$"} }
func fakeUV(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "uv")
	script := "#!/bin/sh\nKH_VISUAL_TEST_ROLE=server exec " + strconv.Quote(os.Args[0]) + " -test.run='^TestVisualProcess$' -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
func manager(t *testing.T, uv string) *visual.Manager {
	t.Helper()
	t.Setenv("KH_VISUAL_TEST_ROLE", "guardian")
	m, err := visual.New(visual.Options{Home: t.TempDir(), UV: uv, Guardian: guardianCommand(), StartupTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}
func eventually(t *testing.T, description string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal(description)
}
func serving(server visual.Server) bool {
	client := http.Client{Timeout: 150 * time.Millisecond, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	response, err := client.Get(server.URL)
	if err != nil {
		return false
	}
	response.Body.Close()
	return response.StatusCode == http.StatusOK
}
func childPID(t *testing.T, root string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "child.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	data, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	// A failed status query is not evidence that the process has exited.
	return err != nil || !strings.Contains(string(data), "Z")
}
func TestAliveWhenPSFails(t *testing.T) {
	for _, mode := range []string{"lookup", "execution"} {
		t.Run(mode, func(t *testing.T) {
			path := t.TempDir()
			if mode == "execution" {
				if err := os.WriteFile(filepath.Join(path, "ps"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", path)
			if err := syscall.Kill(os.Getpid(), 0); err != nil {
				t.Fatal(err)
			}
			if !alive(os.Getpid()) {
				t.Fatal("failed ps query reported a live process as dead")
			}
		})
	}
}
func TestOwnedLifecycleIsolationAndTurnCancellation(t *testing.T) {
	uv := fakeUV(t)
	a, b := manager(t, uv), manager(t, uv)
	if a.Root() == b.Root() {
		t.Fatal("sessions share artifact directory")
	}
	for _, m := range []*visual.Manager{a, b} {
		if _, err := m.Create("demo"); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	first, err := a.Start(ctx, "demo", 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Start(context.Background(), "demo", 0)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(a.Root(), "environment.json"))
	if err != nil {
		t.Fatal(err)
	}
	var environment map[string]string
	if err := json.Unmarshal(data, &environment); err != nil {
		t.Fatal(err)
	}
	if environment["UV_CACHE_DIR"] != filepath.Join(a.Cache(), "uv") || environment["TMPDIR"] != filepath.Join(a.Root(), "tmp") || environment["XDG_CONFIG_HOME"] != filepath.Join(a.Root(), "config") {
		t.Fatal("runtime writes not isolated", environment)
	}
	if _, err := a.Start(context.Background(), "demo", 80); err == nil {
		t.Fatal("running server bypassed port validation")
	}
	cancel()
	time.Sleep(100 * time.Millisecond)
	if !serving(first) || !serving(second) {
		t.Fatal("turn cancellation stopped a server")
	}
	if len(a.List()) != 1 || !a.List()[0].Running {
		t.Fatal(a.List())
	}
	child := childPID(t, a.Root())
	if err := a.Stop("demo"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "owned server still listening", func() bool { return !serving(first) })
	eventually(t, "owned descendant survived stop", func() bool { return !alive(child) })
	if !serving(second) {
		t.Fatal("stop killed sibling session")
	}
	if _, err := os.Stat(first.Notebook); err != nil {
		t.Fatal("artifacts removed", err)
	}
	if err := a.Stop("demo"); err == nil {
		t.Fatal("unknown server stop succeeded")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Create("late"); err == nil {
		t.Fatal("closed manager accepted notebook")
	}
	if !serving(second) {
		t.Fatal("close killed sibling session")
	}
}
func TestStartFailureCleansChildrenAndPendingCancellation(t *testing.T) {
	m := manager(t, fakeUV(t))
	if _, err := m.Create("fail"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), "fail", 0); err == nil || !strings.Contains(err.Error(), "dependency") {
		t.Fatalf("unexpected failure: %v", err)
	}
	child := childPID(t, m.Root())
	eventually(t, "failed start leaked child", func() bool { return !alive(child) })
	if len(m.List()) != 0 {
		t.Fatal(m.List())
	}
	if _, err := m.Create("pending"); err != nil {
		t.Fatal(err)
	}
	pending, cancelPending := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancelPending()
	if _, err := m.Start(pending, "pending", 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pending cancellation lost: %v", err)
	}
	pendingChild := childPID(t, m.Root())
	eventually(t, "pending cancellation leaked descendant", func() bool { return !alive(pendingChild) })
	if len(m.List()) != 0 {
		t.Fatal(m.List())
	}
	if _, err := m.Create("cancel"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Start(ctx, "cancel", 0); err != context.Canceled {
		t.Fatalf("got %v", err)
	}
}
func TestOwnerExitGuardianKillsOwnedGroup(t *testing.T) {
	uv := fakeUV(t)
	sibling := manager(t, uv)
	sibling.Create("sibling")
	other, err := sibling.Start(context.Background(), "sibling", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"os-exit", "sigkill", "sigterm"} {
		t.Run(mode, func(t *testing.T) {
			result := filepath.Join(t.TempDir(), "result.json")
			command := exec.Command(os.Args[0], "-test.run=^TestVisualProcess$")
			command.Env = append(os.Environ(), "KH_VISUAL_TEST_ROLE=owner", "KH_VISUAL_TEST_HOME="+t.TempDir(), "KH_VISUAL_TEST_UV="+uv, "KH_VISUAL_TEST_RESULT="+result, "KH_VISUAL_TEST_EXIT=1")
			command.Stderr = os.Stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer command.Process.Kill()
			var server visual.Server
			eventually(t, "owner failed to start", func() bool {
				data, err := os.ReadFile(result)
				return err == nil && json.Unmarshal(data, &server) == nil
			})
			child := childPID(t, filepath.Dir(server.Notebook))
			switch mode {
			case "os-exit":
				os.WriteFile(result+".exit", nil, 0600)
			case "sigkill":
				command.Process.Kill()
			case "sigterm":
				command.Process.Signal(syscall.SIGTERM)
			}
			command.Wait()
			eventually(t, "owner exit leaked server", func() bool { return !serving(server) })
			eventually(t, "owner exit leaked descendant", func() bool { return !alive(child) })
			if !serving(other) {
				t.Fatal("owner exit killed sibling")
			}
			if _, err := os.Stat(server.Notebook); err != nil {
				t.Fatal("owner exit removed artifacts", err)
			}
		})
	}
}
func TestNamesPathsAndMissingDependency(t *testing.T) {
	m := manager(t, filepath.Join(t.TempDir(), "missing-uv"))
	for _, name := range []string{"../escape", "", "a.py", "/tmp/file", "a/b"} {
		if _, err := m.Create(name); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	server, err := m.Create("demo")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(server.Notebook) != m.Root() {
		t.Fatal(server)
	}
	if _, err := m.Create("demo"); err == nil {
		t.Fatal("overwrote notebook")
	}
	if _, err := m.Start(context.Background(), "demo", 0); err == nil || !strings.Contains(err.Error(), "install uv") {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), "demo", 80); err == nil {
		t.Fatal("accepted privileged port")
	}
	link := filepath.Join(m.Root(), "alias.py")
	if err := os.Symlink(server.Notebook, link); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), "alias", 0); err == nil {
		t.Fatal("accepted notebook symlink")
	}
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".kh", "cache"), 0700)
	os.Symlink(t.TempDir(), filepath.Join(home, ".kh", "cache", "visual"))
	if _, err := visual.New(visual.Options{Home: home}); err == nil {
		t.Fatal("followed cache symlink")
	}
}
