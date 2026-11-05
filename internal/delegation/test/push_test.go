package delegation_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kh/internal/delegation"
)

func pushGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GIT_") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

type pushFixture struct {
	dir, remote string
	request     delegation.VerifiedPushRequest
}

func newPushFixture(t *testing.T) pushFixture {
	t.Helper()
	// Keep both fixture setup and the public API independent of developer config.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	dir := filepath.Join(root, "work")
	remote := filepath.Join(root, "remote.git")
	pushGit(t, root, "init", "--bare", remote)
	pushGit(t, root, "init", "--initial-branch=main", dir)
	pushGit(t, dir, "config", "user.name", "Test")
	pushGit(t, dir, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}
	pushGit(t, dir, "add", "file")
	pushGit(t, dir, "commit", "-m", "initial")
	pushGit(t, dir, "remote", "add", "origin", remote)
	pushGit(t, dir, "push", "--set-upstream", "origin", "main")
	f := pushFixture{dir: dir, remote: remote}
	f.commit(t, "verified")
	return f
}
func (f *pushFixture) commit(t *testing.T, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "file"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	pushGit(t, f.dir, "add", "file")
	pushGit(t, f.dir, "commit", "-m", text)
	f.request = delegation.VerifiedPushRequest{Directory: f.dir, ExpectedCommit: pushGit(t, f.dir, "rev-parse", "HEAD"), Branch: "main", Upstream: "origin/main", VerificationEvidence: []string{"prior checks: artifact-id"}}
}
func (f pushFixture) remoteHead(t *testing.T) string {
	return pushGit(t, f.dir, "--git-dir="+f.remote, "rev-parse", "refs/heads/main")
}

func TestPushVerifiedSuccessAndNoop(t *testing.T) {
	f := newPushFixture(t)
	// These settings would otherwise expand the push to unrelated refs or tags.
	pushGit(t, f.dir, "tag", "-a", "unwanted", "-m", "unwanted")
	pushGit(t, f.dir, "branch", "other")
	pushGit(t, f.dir, "config", "push.followTags", "true")
	pushGit(t, f.dir, "config", "remote.origin.mirror", "true")
	pushGit(t, f.dir, "config", "remote.origin.push", "refs/heads/*:refs/heads/*")
	result, err := delegation.PushVerified(context.Background(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Commit != f.request.ExpectedCommit || result.Branch != "main" || result.Upstream != "origin/main" || result.AlreadyUpToDate {
		t.Fatalf("result=%+v", result)
	}
	if got := f.remoteHead(t); got != f.request.ExpectedCommit {
		t.Fatalf("remote=%s", got)
	}
	refs := pushGit(t, f.dir, "--git-dir="+f.remote, "for-each-ref", "--format=%(refname)")
	if refs != "refs/heads/main" {
		t.Fatalf("unexpected refs pushed: %s", refs)
	}
	result, err = delegation.PushVerified(context.Background(), f.request)
	if err != nil || !result.AlreadyUpToDate {
		t.Fatalf("noop result=%+v err=%v", result, err)
	}
}

func TestPushVerifiedRejectsUnexpectedState(t *testing.T) {
	for _, name := range []string{"tracked", "untracked", "staged", "commit", "short_commit", "branch", "detached", "upstream", "missing_upstream", "missing_evidence", "multiple_urls", "nonrepo"} {
		t.Run(name, func(t *testing.T) {
			f := newPushFixture(t)
			before := f.remoteHead(t)
			switch name {
			case "tracked":
				if err := os.WriteFile(filepath.Join(f.dir, "file"), []byte("dirty"), 0600); err != nil {
					t.Fatal(err)
				}
			case "untracked":
				if err := os.WriteFile(filepath.Join(f.dir, "extra"), []byte("dirty"), 0600); err != nil {
					t.Fatal(err)
				}
			case "staged":
				if err := os.WriteFile(filepath.Join(f.dir, "file"), []byte("dirty"), 0600); err != nil {
					t.Fatal(err)
				}
				pushGit(t, f.dir, "add", "file")
			case "commit":
				f.request.ExpectedCommit = before
			case "short_commit":
				f.request.ExpectedCommit = f.request.ExpectedCommit[:10]
			case "branch":
				pushGit(t, f.dir, "checkout", "-b", "other")
			case "detached":
				pushGit(t, f.dir, "checkout", "--detach")
			case "upstream":
				f.request.Upstream = "origin/other"
			case "missing_upstream":
				pushGit(t, f.dir, "branch", "--unset-upstream")
			case "missing_evidence":
				f.request.VerificationEvidence = []string{" "}
			case "multiple_urls":
				pushGit(t, f.dir, "config", "--add", "remote.origin.pushurl", f.remote)
				pushGit(t, f.dir, "config", "--add", "remote.origin.pushurl", f.remote+"-second")
			case "nonrepo":
				f.request.Directory = t.TempDir()
			}
			if _, err := delegation.PushVerified(context.Background(), f.request); err == nil {
				t.Fatal("unexpected success")
			}
			if after := f.remoteHead(t); after != before {
				t.Fatalf("remote changed from %s to %s", before, after)
			}
		})
	}
}

func TestPushVerifiedRejectedByRemote(t *testing.T) {
	f := newPushFixture(t)
	before := f.remoteHead(t)
	hook := filepath.Join(f.remote, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho rejected >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := delegation.PushVerified(context.Background(), f.request); err == nil {
		t.Fatal("push unexpectedly accepted")
	}
	if f.remoteHead(t) != before {
		t.Fatal("remote changed")
	}
}

func TestPushVerifiedNeverForcesUnrelatedHistory(t *testing.T) {
	f := newPushFixture(t)
	before := f.remoteHead(t)
	pushGit(t, f.dir, "checkout", "--orphan", "unrelated")
	pushGit(t, f.dir, "add", "file")
	pushGit(t, f.dir, "commit", "-m", "unrelated")
	pushGit(t, f.dir, "branch", "-D", "main")
	pushGit(t, f.dir, "branch", "-m", "main")
	pushGit(t, f.dir, "branch", "--set-upstream-to=origin/main", "main")
	f.request.ExpectedCommit = pushGit(t, f.dir, "rev-parse", "HEAD")
	if _, err := delegation.PushVerified(context.Background(), f.request); err == nil {
		t.Fatal("non-fast-forward accepted")
	}
	if f.remoteHead(t) != before {
		t.Fatal("remote changed")
	}
}

func TestPushVerifiedCancellationAndTimeout(t *testing.T) {
	f := newPushFixture(t)
	before := f.remoteHead(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := delegation.PushVerified(ctx, f.request); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	// A local pre-push hook blocks before any remote update; cancellation must kill
	// the process group rather than waiting for hook descendants to release pipes.
	if err := os.WriteFile(filepath.Join(f.dir, ".git", "hooks", "pre-push"), []byte("#!/bin/sh\nsleep 20\n"), 0700); err != nil {
		t.Fatal(err)
	}
	f.request.Timeout = 200 * time.Millisecond
	start := time.Now()
	if _, err := delegation.PushVerified(context.Background(), f.request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("cancellation did not bound execution")
	}
	if f.remoteHead(t) != before {
		t.Fatal("remote changed")
	}
}

func TestPushVerifiedActiveCancellation(t *testing.T) {
	f := newPushFixture(t)
	before := f.remoteHead(t)
	marker := filepath.Join(t.TempDir(), "hook-started")
	hook := "#!/bin/sh\ntouch '" + marker + "'\nsleep 20\n"
	if err := os.WriteFile(filepath.Join(f.dir, ".git", "hooks", "pre-push"), []byte(hook), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := delegation.PushVerified(ctx, f.request); finished <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("hook did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("active cancellation did not finish")
	}
	if f.remoteHead(t) != before {
		t.Fatal("remote changed")
	}
}

func TestPushVerifiedBoundsOutput(t *testing.T) {
	f := newPushFixture(t)
	before := f.remoteHead(t)
	hook := "#!/bin/sh\nyes output | head -c 100000 >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(f.dir, ".git", "hooks", "pre-push"), []byte(hook), 0700); err != nil {
		t.Fatal(err)
	}
	result, err := delegation.PushVerified(context.Background(), f.request)
	if err == nil || !strings.Contains(err.Error(), "64 KiB") || len(result.Output) > 64*1024 {
		t.Fatalf("len=%d err=%v", len(result.Output), err)
	}
	if f.remoteHead(t) != before {
		t.Fatal("remote changed")
	}
}

func TestPushVerifiedChecksRemoteConfirmation(t *testing.T) {
	f := newPushFixture(t)
	before := f.remoteHead(t)
	// A remote hook moving the branch after accepting the push must not produce a
	// successful verified result. The remote may already have changed on error.
	hook := "#!/bin/sh\ngit update-ref refs/heads/main " + before + "\n"
	if err := os.WriteFile(filepath.Join(f.remote, "hooks", "post-receive"), []byte(hook), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := delegation.PushVerified(context.Background(), f.request); err == nil || !strings.Contains(err.Error(), "remote confirmation") {
		t.Fatalf("err=%v", err)
	}
}

func TestPushVerifiedIgnoresAmbientGitOverrides(t *testing.T) {
	f := newPushFixture(t)
	t.Setenv("GIT_DIR", f.remote)
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "push.followTags")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	if _, err := delegation.PushVerified(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	if f.remoteHead(t) != f.request.ExpectedCommit {
		t.Fatal("wrong remote head")
	}
}

func TestPushVerifiedUsesConfiguredPushURL(t *testing.T) {
	f := newPushFixture(t)
	second := filepath.Join(t.TempDir(), "push.git")
	pushGit(t, f.dir, "init", "--bare", second)
	pushGit(t, f.dir, "config", "remote.origin.pushurl", second)
	old := f.remoteHead(t)
	result, err := delegation.PushVerified(context.Background(), f.request)
	if err != nil || result.Commit != f.request.ExpectedCommit {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if f.remoteHead(t) != old {
		t.Fatal("fetch URL was pushed")
	}
	if got := pushGit(t, f.dir, "--git-dir="+second, "rev-parse", "refs/heads/main"); got != f.request.ExpectedCommit {
		t.Fatalf("push URL head=%s", got)
	}
}
