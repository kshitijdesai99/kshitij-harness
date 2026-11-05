package delegation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// VerifiedPushRequest describes previously committed and verified work. Evidence
// is an attestation supplied by the caller, not a command to execute.
type VerifiedPushRequest struct {
	Directory            string
	ExpectedCommit       string
	Branch               string
	Upstream             string // exact short upstream name, for example origin/main
	VerificationEvidence []string
	Timeout              time.Duration // defaults to, and cannot exceed, 30 seconds
}

// VerifiedPushResult is returned only after the remote branch is confirmed.
type VerifiedPushResult struct {
	Commit          string
	Branch          string
	Upstream        string
	Output          string
	AlreadyUpToDate bool
}

var pushCommitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// PushVerified performs no exploration, model calls, commits, or verification
// command reruns. It fails closed on unexpected local state. A push or subsequent
// confirmation error may occur AFTER the remote changed; callers must not retry
// using a less safe workflow. Ordinary Git hooks still run. Concurrent external
// repository changes cannot be locked out; the refspec always pins the supplied
// commit rather than the mutable local branch.
func PushVerified(ctx context.Context, req VerifiedPushRequest) (VerifiedPushResult, error) {
	result := VerifiedPushResult{}
	if req.Directory == "" || !pushCommitPattern.MatchString(req.ExpectedCommit) || strings.TrimSpace(req.Branch) == "" || strings.TrimSpace(req.Upstream) == "" {
		return result, errors.New("verified push requires directory, full lowercase commit ID, branch and upstream")
	}
	evidence := false
	for _, ref := range req.VerificationEvidence {
		evidence = evidence || strings.TrimSpace(ref) != ""
	}
	if !evidence {
		return result, errors.New("verified push requires prior verification evidence")
	}
	timeout := req.Timeout
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	git := func(args ...string) (string, error) { return verifiedPushGit(ctx, req.Directory, args...) }
	fail := func(stage string, err error) (VerifiedPushResult, error) {
		return result, fmt.Errorf("verified push %s: %w", stage, err)
	}
	if _, err := git("rev-parse", "--show-toplevel"); err != nil {
		return fail("repository", err)
	}
	if _, err := git("check-ref-format", "refs/heads/"+req.Branch); err != nil {
		return fail("branch format", err)
	}
	branchRef := "refs/heads/" + req.Branch
	checkState := func() error {
		head, err := git("rev-parse", "--verify", "HEAD")
		if err != nil {
			return err
		}
		if strings.TrimSpace(head) != req.ExpectedCommit {
			return errors.New("HEAD differs from expected commit")
		}
		branch, err := git("symbolic-ref", "--quiet", "HEAD")
		if err != nil {
			return err
		}
		if strings.TrimSpace(branch) != branchRef {
			return errors.New("unexpected branch")
		}
		state, err := git("status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none")
		if err != nil {
			return err
		}
		if state != "" {
			return errors.New("tracked or untracked working tree is dirty")
		}
		return nil
	}
	if err := checkState(); err != nil {
		return fail("state", err)
	}
	tracking, err := git("for-each-ref", "--format=%(upstream:short)%00%(upstream:remotename)%00%(upstream:remoteref)", branchRef)
	if err != nil {
		return fail("upstream", err)
	}
	fields := strings.Split(strings.TrimSuffix(tracking, "\n"), "\x00")
	if len(fields) != 3 || fields[0] != req.Upstream || fields[1] == "" || fields[1] == "." || !strings.HasPrefix(fields[2], "refs/heads/") {
		return fail("upstream", errors.New("configured upstream differs or is not a remote branch"))
	}
	remote, destination := fields[1], fields[2]
	if _, err := git("check-ref-format", destination); err != nil {
		return fail("destination", err)
	}
	urls, err := git("remote", "get-url", "--push", "--all", "--", remote)
	if err != nil {
		return fail("push destination", err)
	}
	url := strings.TrimSuffix(urls, "\n")
	if url == "" || strings.ContainsAny(url, "\r\n\x00") || strings.HasPrefix(url, "-") {
		return fail("push destination", errors.New("expected exactly one configured push URL"))
	}
	if err := checkState(); err != nil {
		return fail("final state", err)
	}
	// Override settings that could expand a push beyond this one branch. A URL
	// rather than the remote name also avoids remote-specific mirror/refspec rules.
	output, err := git("-c", "push.followTags=false", "push", "--porcelain", "--no-force", "--no-follow-tags", "--no-mirror", "--recurse-submodules=no", "--", url, req.ExpectedCommit+":"+destination)
	result.Output = output
	if err != nil {
		return fail("push (remote may have changed)", err)
	}
	confirmed, err := git("ls-remote", "--exit-code", "--refs", "--", url, destination)
	if err != nil {
		return fail("remote confirmation (push completed)", err)
	}
	if strings.TrimSpace(confirmed) != req.ExpectedCommit+"\t"+destination {
		return fail("remote confirmation (push completed)", errors.New("remote branch does not equal expected commit"))
	}
	result.Commit, result.Branch, result.Upstream = req.ExpectedCommit, req.Branch, req.Upstream
	result.AlreadyUpToDate = strings.Contains(output, "=\t")
	return result, nil
}

// pushOutput drains all output without retaining more than a fixed bound.
type pushOutput struct {
	mu       sync.Mutex
	data     []byte
	overflow bool
}

func (b *pushOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := 64*1024 - len(b.data)
	if len(p) > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	b.data = append(b.data, p...)
	return n, nil
}

func verifiedPushGit(ctx context.Context, directory string, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = directory
	// Ambient Git overrides must not silently redirect the repository or inject
	// command-scoped configuration. Normal on-disk Git config and hooks remain.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	output := &pushOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	text := string(output.data)
	if ctx.Err() != nil {
		return text, ctx.Err()
	}
	if output.overflow {
		return text, errors.New("Git output exceeded 64 KiB limit")
	}
	if err != nil {
		return text, fmt.Errorf("%w: %s", err, strings.TrimSpace(text))
	}
	return text, nil
}
