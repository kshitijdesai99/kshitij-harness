package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Read-only commands that run without asking.
var safe = map[string]bool{
	"rg": true, "grep": true, "cat": true, "head": true, "tail": true, "ls": true,
	"wc": true, "sed": true, "find": true, "pwd": true, "file": true, "tree": true, "git": true,
}

var askMu sync.Mutex // parallel calls must not ask at the same time

// Bash runs one command per call. yes skips the y/n prompt.
func Bash(yes bool) Tool {
	return Tool{
		Name:        "bash",
		Description: "Run a shell command in the project dir. Use rg to search; read many files in one call (cat a b, sed -n 1,80p f). Output is capped.",
		Params:      map[string]any{"command": map[string]any{"type": "string"}},
		Required:    []string{"command"},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct{ Command string }
			if json.Unmarshal(input, &in) != nil || in.Command == "" {
				return "", fmt.Errorf("need a command")
			}
			fmt.Fprintln(os.Stderr, "$", in.Command)
			if !yes && !isSafe(in.Command) && !ask(in.Command) {
				return "", fmt.Errorf("user said no")
			}
			return run(ctx, in.Command)
		},
	}
}

func run(ctx context.Context, command string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	// No profile/rc: starts in milliseconds instead of loading your shell setup.
	cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-c", command)
	// Own process group, so a timeout kills everything the command started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second // don't hang on pipes held by leftover children
	out, err := cmd.CombinedOutput()

	s := string(out)
	if len(s) > 20000 { // keep head and tail: errors are usually at the end
		s = s[:10000] + "\n...[cut]...\n" + s[len(s)-10000:]
	}
	if err != nil {
		s += "\n" + err.Error()
	}
	return s, nil // a failing command is information for the model, not a tool error
}

// isSafe allows pipes between safe commands; anything that can write or chain does not pass.
func isSafe(c string) bool {
	if strings.ContainsAny(c, ";&><`$\n") {
		return false
	}
	for _, part := range strings.Split(c, "|") {
		f := strings.Fields(part)
		if len(f) == 0 || !safe[f[0]] {
			return false
		}
		switch f[0] {
		case "sed":
			if strings.Contains(part, "-i") {
				return false
			}
		case "find":
			if strings.Contains(part, "-exec") || strings.Contains(part, "-delete") || strings.Contains(part, "-ok") {
				return false
			}
		case "git":
			if len(f) < 2 || !map[string]bool{"status": true, "diff": true, "log": true, "show": true}[f[1]] {
				return false
			}
		}
	}
	return true
}

func ask(c string) bool {
	askMu.Lock()
	defer askMu.Unlock()
	fmt.Fprint(os.Stderr, "  run it? [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(strings.ToLower(line)) == "y"
}
