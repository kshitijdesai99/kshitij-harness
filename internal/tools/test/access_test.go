package test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"kh/internal/config"
	"kh/internal/tools"
)

type accessApprover func(context.Context, string) (bool, error)

func (f accessApprover) ApproveWrite(ctx context.Context, dir string) (bool, error) {
	return f(ctx, dir)
}

func newTestAccess(t *testing.T, cfg config.Config, approve tools.WriteApprover) *tools.WriteAccess {
	t.Helper()
	a, err := tools.NewWriteAccess(cfg, approve)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func TestWriteAccessCanonicalGrantsAndBoundaries(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	outside := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}
	var prompts []string
	a := newTestAccess(t, config.Config{Writable: []string{outside}}, accessApprover(func(_ context.Context, dir string) (bool, error) {
		prompts = append(prompts, dir)
		return true, nil
	}))
	ctx := context.Background()
	if err := a.Require(ctx, []string{".", alias, outside}); err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 {
		t.Fatalf("alias/configured Writable prompts = %v", prompts)
	}
	root, rel, err := a.OpenEditRoot(ctx, filepath.Join(outside, "new", "nested", "file"))
	if err != nil || rel != filepath.Join("new", "nested", "file") {
		t.Fatalf("new nested root: %q, %v", rel, err)
	}
	if _, err := root.Stat("."); err != nil {
		t.Fatal(err)
	}
	// A lexical-prefix sibling is not implicitly granted.
	sibling := project + "-sibling"
	if err := os.Mkdir(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sibling) })
	if err := a.Require(ctx, []string{sibling}); err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 {
		t.Fatalf("sibling did not prompt: %v", prompts)
	}
	dirs, err := a.SandboxDirectories()
	if err != nil {
		t.Fatal(err)
	}
	dirs[0] = "changed"
	again, err := a.SandboxDirectories()
	if err != nil || again[0] == "changed" {
		t.Fatalf("sandbox directories expose mutable storage: %v", err)
	}
}

func TestWriteAccessOutsideNewFileAndNoImplicitWritable(t *testing.T) {
	t.Chdir(t.TempDir())
	outside := t.TempDir()
	var prompted string
	a := newTestAccess(t, config.Config{Auto: false, Writable: []string{outside}}, accessApprover(func(_ context.Context, dir string) (bool, error) {
		prompted = dir
		return true, nil
	}))
	root, rel, err := a.OpenEditRoot(context.Background(), filepath.Join(outside, "new", "file"))
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(outside)
	if prompted != canonical || rel != filepath.Join("new", "file") {
		t.Fatalf("prompt %q rel %q", prompted, rel)
	}
	if err := root.Mkdir("new", 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := root.Create(rel)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Stat("."); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("root survives Close: %v", err)
	}
	if err := a.Require(context.Background(), []string{outside}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Require after Close: %v", err)
	}
}

func TestWriteAccessDeduplicatesConcurrentAndCancelsWaiter(t *testing.T) {
	t.Chdir(t.TempDir())
	outside := t.TempDir()
	started, release := make(chan struct{}), make(chan struct{})
	var count atomic.Int32
	a := newTestAccess(t, config.Config{}, accessApprover(func(ctx context.Context, _ string) (bool, error) {
		if count.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return true, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}))
	leader := make(chan error, 1)
	go func() { leader <- a.Require(context.Background(), []string{outside}) }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Require(ctx, []string{outside}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.Require(context.Background(), []string{outside}); err != nil {
				t.Error(err)
			}
		}()
	}
	close(release)
	wg.Wait()
	if err := <-leader; err != nil || count.Load() != 1 {
		t.Fatalf("leader %v prompts %d", err, count.Load())
	}
}

func TestWriteAccessCanceledApprovalDoesNotGrant(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	outside := t.TempDir()
	a := newTestAccess(t, config.Config{Auto: false}, accessApprover(func(context.Context, string) (bool, error) {
		cancel()
		return true, nil
	}))
	if err := a.Require(ctx, []string{outside}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	dirs, err := a.SandboxDirectories()
	if err != nil || len(dirs) != 1 {
		t.Fatalf("canceled approval granted access: %q %v", dirs, err)
	}
}

func TestWriteAccessSymlinkEscapeAndRootReplacement(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	outside := t.TempDir()
	a := newTestAccess(t, config.Config{}, accessApprover(func(context.Context, string) (bool, error) { return true, nil }))
	link := filepath.Join(project, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	root, rel, err := a.OpenEditRoot(context.Background(), filepath.Join(link, "bad"))
	if err == nil {
		f, writeErr := root.Create(rel)
		if writeErr == nil {
			f.Close()
			t.Fatal("symlink escaped project root")
		}
	}
	if err := a.Require(context.Background(), []string{outside}); err != nil {
		t.Fatal(err)
	}
	moved := outside + "-moved"
	if err := os.Rename(outside, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(moved) })
	if err := os.Symlink(project, outside); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SandboxDirectories(); err == nil {
		t.Fatal("rebound sandbox grant accepted")
	}
	if _, _, err := a.OpenEditRoot(context.Background(), filepath.Join(outside, "bad")); err == nil {
		t.Fatal("rebound edit grant accepted")
	}
}

func TestBashConfiguredWritableNeedsNoRedundantApproval(t *testing.T) {
	t.Chdir(t.TempDir())
	outside := t.TempDir()
	cfg := config.Defaults
	cfg.Auto, cfg.Sandbox, cfg.Writable = false, false, []string{outside}
	prompts := 0
	a := newTestAccess(t, cfg, accessApprover(func(context.Context, string) (bool, error) {
		prompts++
		return false, nil
	}))
	path := filepath.Join(outside, "existing.txt")
	input, _ := json.Marshal(map[string]any{"command": "printf old > " + shellQuote(path), "write_dirs": []string{outside}})
	commandApproval := fakeApprover(func(context.Context, string, bool) (bool, error) { return true, nil })
	if _, err := tools.BashWithAccess(cfg, commandApproval, a).Run(context.Background(), input); err != nil || prompts != 0 {
		t.Fatalf("configured bash directory prompted: %v prompts=%d", err, prompts)
	}
	edit, _ := json.Marshal(map[string]string{"path": path, "old": "old", "new": "new"})
	if _, err := tools.EditWithAccess(nil, a).Run(context.Background(), edit); err == nil || prompts != 1 {
		t.Fatalf("configured bash permission became an implicit edit grant: %v prompts=%d", err, prompts)
	}
	requireText(t, path, "old")
}

func TestBashWriteDirsApprovalBeforeExecution(t *testing.T) {
	t.Chdir(t.TempDir())
	outside := t.TempDir()
	for _, mode := range []string{"nil", "deny", "error", "cancel", "allow"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var approve tools.WriteApprover
			var prompts int
			if mode != "nil" {
				approve = accessApprover(func(context.Context, string) (bool, error) {
					prompts++
					switch mode {
					case "error":
						return false, errors.New("approval failed")
					case "cancel":
						cancel()
						return true, nil
					}
					return mode == "allow", nil
				})
			}
			cfg := config.Defaults
			cfg.Auto, cfg.Sandbox, cfg.Writable = false, false, nil
			cfg.Safe = []string{"touch"}
			a := newTestAccess(t, cfg, approve)
			file := filepath.Join(outside, mode)
			in, _ := json.Marshal(map[string]any{"command": "touch " + shellQuote(file), "write_dirs": []string{outside}})
			_, err := tools.BashWithAccess(cfg, nil, a).Run(ctx, in)
			_, statErr := os.Stat(file)
			if mode == "allow" {
				if err != nil || statErr != nil || prompts != 1 {
					t.Fatalf("allowed: %v stat %v prompts %d", err, statErr, prompts)
				}
				if _, _, err := a.OpenEditRoot(ctx, file); err != nil || prompts != 1 {
					t.Fatalf("bash/edit grant not shared: %v prompts %d", err, prompts)
				}
			} else if err == nil || !os.IsNotExist(statErr) {
				t.Fatalf("unapproved command executed: %v, stat %v", err, statErr)
			}
		})
	}
}

func TestBashSessionSandboxUpdates(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox is macOS only")
	}
	if out, err := exec.Command("/usr/bin/sandbox-exec", "-p", "(version 1)(allow default)", "/usr/bin/true").CombinedOutput(); err != nil {
		t.Skipf("sandbox-exec cannot start in this environment: %v: %s", err, out)
	}
	t.Chdir(t.TempDir())
	outside, configured := t.TempDir(), t.TempDir()
	read := filepath.Join(outside, "read")
	if err := os.WriteFile(read, []byte("outside-readable"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults
	cfg.Auto, cfg.Sandbox, cfg.Writable = false, true, []string{configured}
	cfg.Safe = []string{"cat", "touch"}
	var prompts int
	a := newTestAccess(t, cfg, accessApprover(func(context.Context, string) (bool, error) {
		prompts++
		return true, nil
	}))
	tool := tools.BashWithAccess(cfg, nil, a)
	call := func(command string, dirs ...string) string {
		t.Helper()
		in, _ := json.Marshal(map[string]any{"command": command, "write_dirs": dirs})
		out, err := tool.Run(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := call("cat " + shellQuote(read)); !strings.Contains(out, "outside-readable") || prompts != 0 {
		t.Fatalf("outside read: %q prompts %d", out, prompts)
	}
	call("touch " + shellQuote(filepath.Join(configured, "cache")))
	if _, err := os.Stat(filepath.Join(configured, "cache")); err != nil {
		t.Fatal("configured Writable lost:", err)
	}
	blocked := filepath.Join(outside, "blocked")
	if out := call("touch " + shellQuote(blocked)); !strings.Contains(out, "write_dirs") {
		t.Fatalf("missing permission hint: %s", out)
	}
	if _, err := os.Stat(blocked); !os.IsNotExist(err) {
		t.Fatal("undeclared outside write permitted")
	}
	// The tool already exists: each call must rebuild its sandbox from grants.
	call("touch "+shellQuote(blocked), outside)
	if _, err := os.Stat(blocked); err != nil || prompts != 1 {
		t.Fatalf("grant did not update sandbox: %v prompts %d", err, prompts)
	}
	call("touch " + shellQuote(filepath.Join(outside, "later")))
	if _, err := os.Stat(filepath.Join(outside, "later")); err != nil || prompts != 1 {
		t.Fatalf("grant not remembered: %v prompts %d", err, prompts)
	}
}

func shellQuote(path string) string { return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'" }
