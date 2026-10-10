package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
	timeout := time.Duration(c.TimeoutSec) * time.Second
	// No profile/rc: starts in milliseconds instead of loading your shell setup.
	argv := []string{"/bin/bash", "--noprofile", "--norc", "-c"}
	if c.Sandbox && runtime.GOOS == "darwin" {
		argv = append(sandbox(append([]string{"."}, c.Writable...)), argv...)
	}
	// Cap = len, so parallel calls appending their command each get a new array.
	argv = argv[:len(argv):len(argv)]
	return Tool{
		Name:        "bash",
		Description: "Run any shell command on the user's machine, with network and system access. Starts in the current dir. Use rg to search; read many files in one call (cat a b, sed -n 1,80p f). Output is capped.",
		Params:      map[string]any{"command": map[string]any{"type": "string"}},
		Required:    []string{"command"},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct{ Command string }
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
			allowed := c.Auto || isSafe(in.Command, c.Safe)
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
			out, err := run(ctx, append(argv, in.Command), timeout, c.OutputCap)
			// The error is the same for our sandbox and for macOS privacy, so name both.
			if c.Sandbox && strings.Contains(out, "Operation not permitted") {
				out += "\n(kh: if this was a write outside the project, kh's sandbox blocked it; the user can rerun with --nosandbox or add the dir to writable in ~/.kh/config.json. " +
					"If it was a read, macOS privacy settings are blocking this folder for the user's terminal app.)"
			}
			return out, err
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

// sandbox returns a sandbox-exec prefix that allows reads and network everywhere
// but writes only under dirs (and /dev, for /dev/null and the tty).
func sandbox(dirs []string) []string {
	home, _ := os.UserHomeDir()
	p := `(version 1)(allow default)(deny file-write*)(allow file-write* (subpath "/dev")`
	for _, d := range dirs {
		if strings.HasPrefix(d, "~/") {
			d = filepath.Join(home, d[2:])
		}
		d, _ = filepath.Abs(d)
		if r, err := filepath.EvalSymlinks(d); err == nil {
			d = r // the sandbox matches real paths, e.g. /tmp is /private/tmp
		}
		p += " (subpath " + strconv.Quote(d) + ")"
	}
	// Memory is harness-owned state, not a project output. Always permit its
	// exact SQLite files, even when Writable is explicitly empty. Do not grant
	// the entire .kh directory: it also contains config and login credentials.
	if home != "" {
		if resolved, err := filepath.EvalSymlinks(home); err == nil {
			home = resolved
		}
		dir := filepath.Join(home, ".kh")
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
		p += ") (allow file-write-create (literal " + strconv.Quote(dir) + ")) (allow file-write*"
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			p += " (literal " + strconv.Quote(filepath.Join(dir, "memory.db")+suffix) + ")"
		}
	}
	return []string{"/usr/bin/sandbox-exec", "-p", p + ")"}
}
