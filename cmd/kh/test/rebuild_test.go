package test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRebuildCommand(t *testing.T) {
	original := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", original, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	source := filepath.Join(t.TempDir(), "source checkout")
	if err := os.MkdirAll(filepath.Join(source, "cmd", "kh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module kh\n\ngo 1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "cmd", "kh", "main.go"), []byte("package main\nimport \"fmt\"\nfunc main(){fmt.Println(\"REBUILT_FIXTURE\")}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cacheOutput, err := exec.Command("go", "env", "GOPATH", "GOMODCACHE", "GOCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	caches := strings.Split(strings.TrimSpace(string(cacheOutput)), "\n")
	if len(caches) != 3 {
		t.Fatalf("unexpected Go cache paths: %q", cacheOutput)
	}
	fixture := func(t *testing.T, configText string) (string, []string) {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "installed tools")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		binary := filepath.Join(dir, "kh")
		in, err := os.Open(original)
		if err != nil {
			t.Fatal(err)
		}
		out, err := os.OpenFile(binary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o750)
		if err != nil {
			in.Close()
			t.Fatal(err)
		}
		_, err = io.Copy(out, in)
		in.Close()
		closeErr := out.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if err := os.Chmod(binary, 0o750); err != nil {
			t.Fatal(err)
		}
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".kh"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".kh", "config.json"), []byte(configText), 0o600); err != nil {
			t.Fatal(err)
		}
		env := append(os.Environ(), "HOME="+home, "KH_SOURCE_DIR=", "TMUX=", "TMUX_PANE=", "KH_PROVIDER=not-installed",
			"GOPATH="+caches[0], "GOMODCACHE="+caches[1], "GOCACHE="+caches[2])
		return binary, env
	}
	run := func(binary, dir string, env []string, args ...string) (string, error) {
		cmd := exec.Command(binary, args...)
		cmd.Dir = dir
		cmd.Env = env
		cmd.Stdin = strings.NewReader("")
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	digest := func(path string) [32]byte {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(b)
	}
	noTemps := func(t *testing.T, binary string) {
		t.Helper()
		paths, err := filepath.Glob(filepath.Join(filepath.Dir(binary), ".kh-rebuild-*"))
		if err != nil || len(paths) != 0 {
			t.Fatalf("temporary builds leaked: %v %v", paths, err)
		}
	}
	fakeGo := func(t *testing.T, script string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	t.Run("nested-checkout-symlink-and-native-build", func(t *testing.T) {
		binary, env := fixture(t, "invalid JSON")
		link := filepath.Join(t.TempDir(), "kh-link")
		if err := os.Symlink(binary, link); err != nil {
			t.Fatal(err)
		}
		before := digest(binary)
		// A cross-compilation environment must not replace kh with a foreign binary.
		env = append(env, "GOOS=windows", "GOARCH=amd64")
		resolved, err := filepath.EvalSymlinks(binary)
		if err != nil {
			t.Fatal(err)
		}
		out, err := run(link, filepath.Join(source, "cmd"), env, "--rebuild")
		if err != nil || !strings.Contains(out, "Rebuilt "+resolved) || strings.Contains(out, "kh chat") {
			t.Fatalf("rebuild: %s: %v", out, err)
		}
		if digest(binary) == before {
			t.Fatal("executable not replaced")
		}
		info, err := os.Lstat(link)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("symlink replaced: %v %v", info, err)
		}
		info, err = os.Stat(binary)
		if err != nil || info.Mode().Perm() != 0o750 {
			t.Fatalf("permissions changed: %v %v", info, err)
		}
		out, err = run(binary, source, env)
		if err != nil || out != "REBUILT_FIXTURE\n" {
			t.Fatalf("new binary unusable: %q %v", out, err)
		}
		noTemps(t, binary)
	})
	t.Run("explicit-source-from-another-folder", func(t *testing.T) {
		binary, env := fixture(t, "invalid JSON")
		env = append(env, "KH_SOURCE_DIR="+source)
		out, err := run(binary, t.TempDir(), env, "--rebuild")
		if err != nil || !strings.Contains(out, "from "+source) {
			t.Fatalf("override: %s %v", out, err)
		}
		noTemps(t, binary)
	})
	t.Run("automatic-startup-from-another-folder", func(t *testing.T) {
		binary, env := fixture(t, "{}")
		autoSource := t.TempDir()
		if err := os.MkdirAll(filepath.Join(autoSource, "cmd", "kh"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(autoSource, "go.mod"), []byte("module kh\n\ngo 1.24.0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		program := `package main
import ("fmt"; "os")
func main() { cwd, _ := os.Getwd(); fmt.Printf("ARGS=%q CWD=%s ENV=%s\nREBUILT_FIXTURE\n", os.Args[1:], cwd, os.Getenv("KH_FIXTURE_VALUE")) }
`
		if err := os.WriteFile(filepath.Join(autoSource, "cmd", "kh", "main.go"), []byte(program), 0o644); err != nil {
			t.Fatal(err)
		}
		env = append(env, "KH_SOURCE_DIR="+autoSource, "KH_AUTO_REBUILD=1", "KH_FIXTURE_VALUE=preserved")
		before := digest(binary)
		cwd := t.TempDir()
		cmd := exec.Command(binary, "-s", "previous-session")
		cmd.Dir, cmd.Env, cmd.Stdin = cwd, env, strings.NewReader("")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if strings.Contains(stdout.String(), "Rebuilding ") || strings.Contains(stdout.String(), "Starting session with the new build") || !strings.Contains(stderr.String(), "Starting session with the new build") {
			t.Fatalf("build diagnostics must use stderr: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		out := stderr.String() + stdout.String()
		resolvedCwd, resolveErr := filepath.EvalSymlinks(cwd)
		if resolveErr != nil {
			t.Fatal(resolveErr)
		}
		if !strings.Contains(out, `ARGS=["-s" "previous-session"] CWD=`+resolvedCwd+" ENV=preserved") {
			t.Fatalf("startup lost arguments, folder, or environment: %s", out)
		}
		if err != nil || !strings.Contains(out, "Starting session with the new build") || !strings.HasSuffix(out, "REBUILT_FIXTURE\n") {
			t.Fatalf("automatic startup: %s %v", out, err)
		}
		if digest(binary) == before {
			t.Fatal("startup did not replace executable")
		}
		noTemps(t, binary)
	})
	t.Run("automatic-reexec-does-not-loop", func(t *testing.T) {
		binary, env := fixture(t, "{}")
		env = append(env, "KH_AUTO_REBUILD=1", "KH_REBUILD_EXEC_PID=stale-parent-marker")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "-provider", "not-installed", "task")
		cmd.Dir = t.TempDir()
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err == nil || ctx.Err() != nil || strings.Count(string(out), "Starting session with the new build") != 1 || !strings.Contains(string(out), "unsupported provider") {
			t.Fatalf("reexec: %s %v", out, err)
		}
		noTemps(t, binary)
	})
	t.Run("automatic-build-ignores-unrelated-working-checkout", func(t *testing.T) {
		binary, env := fixture(t, "{}")
		env = append(env, "KH_AUTO_REBUILD=1", "KH_SOURCE_DIR=")
		out, err := run(binary, source, env, "--provider", "not-installed", "task")
		if err == nil || !strings.Contains(out, "unsupported provider") || strings.Contains(out, "REBUILT_FIXTURE") {
			t.Fatalf("automatic build selected unrelated cwd checkout: %s %v", out, err)
		}
		noTemps(t, binary)
	})
	for _, mode := range []string{"failed-build", "opt-out", "utility"} {
		t.Run("automatic-"+mode, func(t *testing.T) {
			binary, env := fixture(t, "{}")
			before := digest(binary)
			goDir := fakeGo(t, "echo intentional-compiler-failure >&2\nexit 7")
			env = append(env, "KH_SOURCE_DIR="+source, "KH_AUTO_REBUILD=1", "PATH="+goDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			args := []string{"task"}
			if mode == "opt-out" {
				env = append(env, "KH_AUTO_REBUILD=0")
			}
			if mode == "utility" {
				args = []string{"--sessions"}
			}
			out, err := run(binary, t.TempDir(), env, args...)
			switch mode {
			case "failed-build":
				if err == nil || !strings.Contains(out, "existing binary unchanged") || strings.Contains(out, "unsupported provider") {
					t.Fatalf("startup failure: %s %v", out, err)
				}
			case "opt-out":
				if err == nil || !strings.Contains(out, "unsupported provider") || strings.Contains(out, "Rebuilding") {
					t.Fatalf("opt-out: %s %v", out, err)
				}
			case "utility":
				if err != nil || strings.Contains(out, "Rebuilding") {
					t.Fatalf("utility: %s %v", out, err)
				}
			}
			if digest(binary) != before {
				t.Fatal("executable changed unexpectedly")
			}
			noTemps(t, binary)
		})
	}
	t.Run("terminal-build-failure-is-visible-before-tmux", func(t *testing.T) {
		python, err := exec.LookPath("python3")
		if err != nil {
			t.Skip("python3 not installed")
		}
		binary, env := fixture(t, "{}")
		marker := filepath.Join(t.TempDir(), "tmux-invoked")
		goDir := fakeGo(t, "echo intentional-compiler-failure >&2\nexit 7")
		if err := os.WriteFile(filepath.Join(goDir, "tmux"), []byte("#!/bin/sh\nprintf invoked > \"$KH_TMUX_PROBE\"\nexit 7\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		env = append(env, "KH_AUTO_REBUILD=1", "KH_SOURCE_DIR="+source, "KH_TMUX_PROBE="+marker,
			"PATH="+goDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		cmd := exec.Command(python, "-c", `import os, pty, select, subprocess, sys, time
master, slave = pty.openpty()
process = subprocess.Popen([sys.argv[1]], stdin=slave, stdout=slave, stderr=slave)
os.close(slave)
output = bytearray()
deadline = time.monotonic() + 10
try:
    while time.monotonic() < deadline:
        if select.select([master], [], [], 0.1)[0]:
            try:
                data = os.read(master, 65536)
            except OSError:
                break
            if not data:
                break
            output.extend(data)
    code = process.wait(timeout=1)
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
sys.stdout.buffer.write(output)
sys.exit(code)
`, binary)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "intentional-compiler-failure") || !strings.Contains(string(out), "existing binary unchanged") {
			t.Fatalf("terminal compiler failure hidden: %s %v", out, err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("tmux was invoked before build succeeded: %v", err)
		}
		noTemps(t, binary)
	})
	t.Run("embedded-source-fallback", func(t *testing.T) {
		binary, env := fixture(t, "invalid JSON")
		out, err := run(binary, t.TempDir(), env, "--rebuild")
		if err != nil || !strings.Contains(out, "Existing chats are unchanged") {
			t.Fatalf("fallback: %s %v", out, err)
		}
		out, err = run(binary, t.TempDir(), env, "--help")
		if err != nil || !strings.Contains(out, "-rebuild") {
			t.Fatalf("rebuilt harness: %s %v", out, err)
		}
		noTemps(t, binary)
	})
	for _, exitCode := range []int{7, 0} {
		t.Run(fmt.Sprintf("failed-or-invalid-build-%d", exitCode), func(t *testing.T) {
			binary, env := fixture(t, "invalid JSON")
			before := digest(binary)
			goDir := fakeGo(t, fmt.Sprintf("echo intentional-compiler-failure >&2\nexit %d", exitCode))
			env = append(env, "PATH="+goDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := run(binary, source, env, "--rebuild")
			if err == nil || !strings.Contains(out, "existing binary unchanged") {
				t.Fatalf("failure hidden: %s %v", out, err)
			}
			if digest(binary) != before {
				t.Fatal("failed rebuild changed executable")
			}
			noTemps(t, binary)
		})
	}
	t.Run("bad-source-and-argument-validation", func(t *testing.T) {
		binary, env := fixture(t, "invalid JSON")
		before := digest(binary)
		for _, args := range [][]string{{"--rebuild", "task"}, {"--rebuild", "-r"}, {"--rebuild", "--sessions"}} {
			out, err := run(binary, source, env, args...)
			if err == nil || !strings.Contains(out, "takes no other flags or task") {
				t.Fatalf("args %v: %s %v", args, out, err)
			}
		}
		badSource := t.TempDir()
		out, err := run(binary, source, append(env, "KH_SOURCE_DIR="+badSource), "--rebuild")
		if err == nil || !strings.Contains(out, "KH_SOURCE_DIR is not") {
			t.Fatalf("invalid source: %s %v", out, err)
		}
		out, err = run(binary, source, env, "sessions")
		if err == nil || !strings.Contains(out, "invalid character") {
			t.Fatalf("normal commands ignored broken config: %s %v", out, err)
		}
		if digest(binary) != before {
			t.Fatal("invalid rebuild changed executable")
		}
		noTemps(t, binary)
	})
	t.Run("interrupt-preserves-old-binary", func(t *testing.T) {
		binary, env := fixture(t, "invalid JSON")
		before := digest(binary)
		marker := filepath.Join(t.TempDir(), "started")
		goDir := fakeGo(t, `echo started > "$KH_REBUILD_STARTED"
exec /bin/sleep 20`)
		env = append(env, "PATH="+goDir+string(os.PathListSeparator)+os.Getenv("PATH"), "KH_REBUILD_STARTED="+marker)
		cmd := exec.Command(binary, "--rebuild")
		cmd.Dir = source
		cmd.Env = env
		var output bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		waited := false
		defer func() {
			if !waited {
				cmd.Process.Kill()
				select {
				case <-done:
				case <-time.After(time.Second):
				}
			}
		}()
		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err := os.Stat(marker); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("Go build did not start")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			waited = true
			if err == nil || !strings.Contains(output.String(), "context canceled") {
				t.Fatalf("interrupt: %s %v", output.String(), err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("interrupted rebuild did not exit promptly")
		}
		if digest(binary) != before {
			t.Fatal("interrupted rebuild changed executable")
		}
		noTemps(t, binary)
	})
}
