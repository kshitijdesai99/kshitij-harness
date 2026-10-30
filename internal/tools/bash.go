package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"kh/internal/config"
)

// Approver owns command display, prompting, and steering during approval.
type Approver interface {
	Approve(context.Context, string, bool) (bool, error)
}

// Bash runs one command per call.
func Bash(c config.Config, approval Approver) Tool {
	return BashWithPolicy(c, approval, nil)
}

// BashWithPolicy reads the shared command approval setting for each request.
// A nil policy uses the configuration's initial Auto setting.
func BashWithPolicy(c config.Config, approval Approver, policy *ApprovalPolicy) Tool {
	timeout := time.Duration(c.TimeoutSec) * time.Second
	return Tool{
		Name:        "bash",
		Description: "Run any shell command on the user's machine, with network and filesystem access subject to OS permissions. Starts in the current dir. Commands require approval unless auto is enabled or the command is classified as safe. Use rg to search; read many files in one call (cat a b, sed -n 1,80p f). Output is capped.",
		Params: map[string]any{
			"command": map[string]any{"type": "string"},
		},
		Required: []string{"command"},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct {
				Command string
			}
			if json.Unmarshal(input, &in) != nil || strings.TrimSpace(in.Command) == "" {
				return "", fmt.Errorf("need a command")
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if c.TimeoutSec <= 0 || int64(c.TimeoutSec) > int64((1<<63-1)/time.Second) {
				return "", fmt.Errorf("timeout_sec must be positive and fit a time.Duration")
			}
			if c.OutputCap <= 0 {
				return "", fmt.Errorf("output_cap must be positive")
			}
			auto := c.Auto
			if policy != nil {
				auto = policy.Auto()
			}
			allowed := auto || isSafe(in.Command, c.Safe)
			if approval != nil {
				var err error
				allowed, err = approval.Approve(ctx, in.Command, allowed)
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				if err != nil {
					return "", err
				}
			}
			if !allowed {
				return "", fmt.Errorf("user said no")
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			// No profile/rc: starts without loading the user's shell setup.
			argv := []string{"/bin/bash", "--noprofile", "--norc", "-c", in.Command}
			return run(ctx, argv, timeout, c.OutputCap)
		},
	}
}

func run(ctx context.Context, argv []string, timeout time.Duration, outputCap int) (string, error) {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// Own process group, so a timeout kills everything the command started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second // don't hang on pipes held by leftover children
	out := newOutput(outputCap)
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	s := out.String()
	if err != nil {
		s += "\n" + err.Error()
	}
	if parent.Err() != nil {
		return s, parent.Err()
	}
	// Our timeout, not the user's Ctrl-C: tell the model so it retries smaller.
	if ctx.Err() == context.DeadlineExceeded {
		s += fmt.Sprintf("\n(kh: timed out after %v and was killed. Try a narrower command.)", timeout)
	}
	return s, nil // a failing command is information for the model, not a tool error
}
