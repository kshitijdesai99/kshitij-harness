package test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kh/internal/config"
	"kh/internal/tools"
)

func TestSafetyRegression(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, command := range []string{
		"find . -fprint stolen", "find . -fprintf stolen '%p'", "find . -fls stolen", "find . -okdir echo x ;", "find . -execdir sh ;",
		"git diff --output=stolen", "git diff --output stolen", "git diff --ext-diff", "git diff --textconv", "git log --output=stolen",
		"rg --pre=sh x", "rg --hostname-bin=sh x", "rg --pre sh x", "rg --hostname-bin sh x", "rg --future-write-option x", "rg -z x",
		"sed --in-place s/a/b/ a", "sed -i.bak s/a/b/ a", "sed '1e touch stolen' a", "sed 'w stolen' a", "sed 's/a/b/e' a", "sed -f script a", "sed -n '1,2p;w stolen' a",
		"grep --unexpected-write-option x a", "tree -o stolen", "file -C", "file -z a", "tree -R .",
		"cat a > stolen", "cat a\nrm a", "echo $(touch stolen)", "echo `touch stolen`", "echo hi & echo there", "echo hi # ignored", "echo hi |", "echo hi ;; echo there", "echo 'unterminated", "echo \\x", "(echo hi)",
	} {
		t.Run(command, func(t *testing.T) {
			called := false
			cfg := config.Defaults
			cfg.Auto = false
			approval := fakeApprover(func(_ context.Context, got string, safe bool) (bool, error) {
				called = true
				if got != command {
					t.Errorf("command changed: %q", got)
				}
				if safe {
					t.Errorf("dangerous/unsupported command automatically approved: %s", command)
				}
				return false, nil
			})
			input, _ := json.Marshal(map[string]string{"command": command})
			_, err := tools.Bash(cfg, approval).Run(ctx, input)
			if !called || err == nil {
				t.Fatalf("called=%v err=%v", called, err)
			}
		})
	}
}

func TestNilApprover(t *testing.T) {
	cfg := config.Defaults
	for _, tc := range []struct {
		command       string
		auto, allowed bool
	}{
		{"printf hello", false, true}, {"touch denied", false, false}, {"printf hello", true, true},
	} {
		cfg.Auto = tc.auto
		input, _ := json.Marshal(map[string]string{"command": tc.command})
		_, err := tools.Bash(cfg, nil).Run(ctx, input)
		if (err == nil) != tc.allowed {
			t.Fatalf("%q: %v", tc.command, err)
		}
	}
}

func TestExplicitApprovalAndError(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Defaults
	cfg.Auto = false
	input := []byte(`{"command":"touch approved"}`)
	approval := fakeApprover(func(_ context.Context, _ string, safe bool) (bool, error) {
		if safe {
			t.Fatal("write command marked safe")
		}
		return true, nil
	})
	if _, err := tools.Bash(cfg, approval).Run(ctx, input); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("approved"); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("approval failed")
	approval = fakeApprover(func(context.Context, string, bool) (bool, error) { return false, failure })
	if _, err := tools.Bash(cfg, approval).Run(ctx, input); !errors.Is(err, failure) {
		t.Fatalf("got %v", err)
	}
}

func TestApprovalCancellation(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Defaults
	cfg.Auto = false
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	started := make(chan struct{})
	approval := fakeApprover(func(c context.Context, _ string, _ bool) (bool, error) {
		close(started)
		<-c.Done()
		return false, c.Err()
	})
	done := make(chan error, 1)
	go func() {
		_, err := tools.Bash(cfg, approval).Run(c, []byte(`{"command":"touch forbidden"}`))
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("approval ignored cancellation")
	}
	if _, err := os.Stat("forbidden"); !os.IsNotExist(err) {
		t.Fatalf("command ran: %v", err)
	}
}

func TestRunCancellation(t *testing.T) {
	cfg := config.Defaults
	cfg.Auto = true
	c, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := tools.Bash(cfg, fakeApprover(func(_ context.Context, _ string, safe bool) (bool, error) { return safe, nil })).Run(c, []byte(`{"command":"sleep 20 & wait"}`))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("cancel did not terminate process group promptly")
	}
}

func TestCanceledBeforeApproval(t *testing.T) {
	c, cancel := context.WithCancel(ctx)
	cancel()
	approval := fakeApprover(func(context.Context, string, bool) (bool, error) {
		t.Fatal("approval called after cancellation")
		return true, nil
	})
	_, err := tools.Bash(config.Defaults, approval).Run(c, []byte(`{"command":"pwd"}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestEditContainment(t *testing.T) {
	project, outside := t.TempDir(), t.TempDir()
	t.Chdir(project)
	target := filepath.Join(outside, "target")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"escape": outside, "file": target} {
		if err := os.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(path, old, new string) error {
		input, _ := json.Marshal(map[string]string{"path": path, "old": old, "new": new})
		_, err := tools.Edit.Run(ctx, input)
		return err
	}
	crossDirectory := filepath.Join(outside, "cross-directory")
	relative, err := filepath.Rel(project, crossDirectory)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, old, new string }{
		{"escape/new/child", "", "created"}, {"escape/target", "original", "directory link"}, {"file", "directory link", "file link"}, {relative, "", "created"}, {target, "file link", "changed"}, {relative, "created", "changed"},
	} {
		if err := edit(tc.path, tc.old, tc.new); err != nil {
			t.Fatalf("edit %s: %v", tc.path, err)
		}
	}
	for path, want := range map[string]string{
		target: "changed", crossDirectory: "changed", filepath.Join(outside, "new", "child"): "created",
	} {
		if b, err := os.ReadFile(path); err != nil || string(b) != want {
			t.Fatalf("outside file %s: %q %v", path, b, err)
		}
	}
	for _, path := range []string{"..valid/child", filepath.Join(project, "nested", "child")} {
		if err := edit(path, "", "hello long text"); err != nil {
			t.Fatal(err)
		}
		if err := edit(path, "hello long text", "x"); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(path); string(b) != "x" {
			t.Fatalf("not truncated: %q", b)
		}
	}
	if err := os.Symlink("nested", "internal-link"); err != nil {
		t.Fatal(err)
	}
	if err := edit("internal-link/new/child", "", "ok"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile("nested/new/child"); strings.TrimSpace(string(b)) != "ok" {
		t.Fatalf("internal link failed: %q", b)
	}
}
