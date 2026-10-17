package test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"kh/internal/config"
	"kh/internal/tools"
)

func TestAutoWriteOptInAndSessionGrants(t *testing.T) {
	t.Chdir(t.TempDir())
	outside, later := t.TempDir(), t.TempDir()
	prompts := 0
	a := newTestAccess(t, config.Config{}, accessApprover(func(context.Context, string) (bool, error) {
		prompts++
		return true, nil
	}))
	if a.AutoWrite() {
		t.Fatal("automatic folder approval enabled by default")
	}
	if err := a.Require(context.Background(), []string{outside}); err != nil || prompts != 1 {
		t.Fatalf("default approval: %v prompts=%d", err, prompts)
	}
	a.SetAutoWrite(true)
	if !a.AutoWrite() {
		t.Fatal("automatic folder approval not enabled")
	}
	if err := a.Require(context.Background(), []string{later}); err != nil || prompts != 1 {
		t.Fatalf("automatic approval: %v prompts=%d", err, prompts)
	}
	a.SetAutoWrite(false)
	if a.AutoWrite() {
		t.Fatal("automatic folder approval not disabled")
	}
	if err := a.Require(context.Background(), []string{outside, later}); err != nil || prompts != 1 {
		t.Fatalf("toggle revoked session grants: %v prompts=%d", err, prompts)
	}
	if err := a.Require(context.Background(), []string{t.TempDir()}); err != nil || prompts != 2 {
		t.Fatalf("new request did not prompt after disabling: %v prompts=%d", err, prompts)
	}
}

func TestAutoWriteOutsideEditAndBash(t *testing.T) {
	t.Chdir(t.TempDir())
	editDir, bashDir := t.TempDir(), t.TempDir()
	cfg := config.Defaults
	cfg.Auto, cfg.AutoWrite, cfg.Sandbox, cfg.Writable = true, true, false, nil
	a := newTestAccess(t, cfg, accessApprover(func(context.Context, string) (bool, error) {
		t.Error("automatic folder approval called approver")
		return false, nil
	}))
	if !a.AutoWrite() {
		t.Fatal("constructor did not copy AutoWrite")
	}
	ctx := context.Background()
	path := filepath.Join(editDir, "new.txt")
	input, _ := json.Marshal(map[string]string{"path": path, "old": "", "new": "hello"})
	if _, err := tools.EditWithAccess(nil, a).Run(ctx, input); err != nil {
		t.Fatal(err)
	}
	requireText(t, path, "hello")
	bashPath := filepath.Join(bashDir, "bash.txt")
	input, _ = json.Marshal(map[string]any{"command": "printf bash > " + shellQuote(bashPath), "write_dirs": []string{bashDir}})
	if _, err := tools.BashWithAccess(cfg, nil, a).Run(ctx, input); err != nil {
		t.Fatal(err)
	}
	requireText(t, bashPath, "bash")
	// Both tool paths must install the same pinned, sandbox-visible session grants.
	dirs, err := a.SandboxDirectories()
	if err != nil || len(dirs) != 3 {
		t.Fatalf("automatic grants: %v %v", dirs, err)
	}
	for _, want := range []string{editDir, bashDir} {
		canonical, err := filepath.EvalSymlinks(want)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, dir := range dirs {
			found = found || dir == canonical
		}
		if !found {
			t.Fatalf("missing sandbox grant %q: %v", canonical, dirs)
		}
	}
	a.SetAutoWrite(false)
	if _, _, err := a.OpenEditRoot(ctx, bashPath); err != nil {
		t.Fatalf("bash grant not shared with edit: %v", err)
	}
}

func TestAutoWriteKeepsBashSandbox(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox only")
	}
	if out, err := exec.Command("/usr/bin/sandbox-exec", "-p", "(version 1)(allow default)", "/usr/bin/true").CombinedOutput(); err != nil {
		t.Skipf("sandbox-exec cannot start in this environment: %v: %s", err, out)
	}
	t.Chdir(t.TempDir())
	outside, unrequested := t.TempDir(), t.TempDir()
	cfg := config.Defaults
	cfg.Auto, cfg.AutoWrite, cfg.Sandbox, cfg.Writable = true, true, true, nil
	a := newTestAccess(t, cfg, accessApprover(func(context.Context, string) (bool, error) { t.Error("unexpected folder prompt"); return false, nil }))
	allowed := filepath.Join(outside, "allowed.txt")
	blocked := filepath.Join(unrequested, "blocked.txt")
	input, _ := json.Marshal(map[string]any{"command": "printf allowed > " + shellQuote(allowed), "write_dirs": []string{outside}})
	if out, err := tools.BashWithAccess(cfg, nil, a).Run(context.Background(), input); err != nil {
		t.Fatalf("declared write: %s %v", out, err)
	}
	requireText(t, allowed, "allowed")
	input, _ = json.Marshal(map[string]string{"command": "printf blocked > " + shellQuote(blocked)})
	_, _ = tools.BashWithAccess(cfg, nil, a).Run(context.Background(), input)
	if _, err := os.Stat(blocked); !os.IsNotExist(err) {
		t.Fatalf("automatic approval disabled sandbox: %v", err)
	}
}

func TestAutoWriteRejectsCanceledAndInvalidRequests(t *testing.T) {
	t.Chdir(t.TempDir())
	outside := t.TempDir()
	a := newTestAccess(t, config.Config{AutoWrite: true}, accessApprover(func(context.Context, string) (bool, error) {
		t.Error("automatic folder approval called approver")
		return false, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Require(ctx, []string{outside}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request: %v", err)
	}
	if _, _, err := a.OpenEditRoot(ctx, filepath.Join(outside, "file")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled edit: %v", err)
	}
	file := filepath.Join(outside, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(outside, "dangling")
	if err := os.Symlink(filepath.Join(outside, "absent"), dangling); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"", " \t", string([]byte{'x', 0}), file, dangling, filepath.Join(outside, "new", "nested")} {
		if err := a.Require(context.Background(), []string{dir}); err == nil {
			t.Errorf("invalid directory accepted: %q", dir)
		}
	}
	dirs, err := a.SandboxDirectories()
	if err != nil || len(dirs) != 1 {
		t.Fatalf("invalid/canceled request broadened grants: %v %v", dirs, err)
	}
}

func TestAutoWriteSymlinkEscapeAndRootReplacement(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	outside := t.TempDir()
	a := newTestAccess(t, config.Config{AutoWrite: true}, nil)
	link := filepath.Join(project, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	root, rel, err := a.OpenEditRoot(context.Background(), filepath.Join(link, "bad"))
	if err == nil {
		f, err := root.Create(rel)
		if err == nil {
			f.Close()
			t.Fatal("automatic approval allowed project symlink escape")
		}
	}
	dirs, err := a.SandboxDirectories()
	if err != nil || len(dirs) != 1 {
		t.Fatalf("symlink implicitly broadened grant: %v %v", dirs, err)
	}
	if err := a.Require(context.Background(), []string{outside}); err != nil {
		t.Fatal(err)
	}
	// Move within a t.TempDir fixture, then try both a symlink and a fresh directory.
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(outside, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(project, outside); err != nil {
		t.Fatal(err)
	}
	assertRejected := func() {
		t.Helper()
		if _, err := a.SandboxDirectories(); err == nil {
			t.Error("replaced sandbox grant accepted")
		}
		if _, _, err := a.OpenEditRoot(context.Background(), filepath.Join(outside, "bad")); err == nil {
			t.Error("replaced edit grant accepted")
		}
	}
	assertRejected()
	if err := os.Remove(outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	assertRejected()
}
