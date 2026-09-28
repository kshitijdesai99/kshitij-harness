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

	"kh/internal/config"
)

// Flags that make an otherwise read-only command write. Not configurable on purpose.
var writeFlags = map[string][]string{
	"sed":  {"-i"},
	"find": {"-exec", "-execdir", "-delete", "-ok"},
}

var askMu sync.Mutex // parallel calls must not ask at the same time

// Bash runs one command per call.
func Bash(c config.Config) Tool {
	timeout := time.Duration(c.TimeoutSec) * time.Second
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
			if !c.Yes && !isSafe(in.Command, c.Safe) && !ask() {
				return "", fmt.Errorf("user said no")
			}
			return run(ctx, in.Command, timeout, c.OutputCap)
		},
	}
}

func run(ctx context.Context, command string, timeout time.Duration, outputCap int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// No profile/rc: starts in milliseconds instead of loading your shell setup.
	cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-c", command)
	// Own process group, so a timeout kills everything the command started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second // don't hang on pipes held by leftover children
	out, err := cmd.CombinedOutput()

	s := string(out)
	if half := outputCap / 2; len(s) > outputCap { // keep head and tail: errors are usually at the end
		s = s[:half] + "\n...[cut]...\n" + s[len(s)-half:]
	}
	if err != nil {
		s += "\n" + err.Error()
	}
	return s, nil // a failing command is information for the model, not a tool error
}

// isSafe allows pipes between safe commands; anything that can write or chain does not pass.
// A safe entry like "git diff" matches the leading words of a command.
func isSafe(c string, safe []string) bool {
	if strings.ContainsAny(c, ";&><`$\n") {
		return false
	}
	for _, part := range strings.Split(c, "|") {
		f := strings.Fields(part)
		if len(f) == 0 || !matchesAny(f, safe) {
			return false
		}
		for _, flag := range writeFlags[f[0]] {
			if strings.Contains(part, flag) {
				return false
			}
		}
	}
	return true
}

func matchesAny(f, safe []string) bool {
	for _, s := range safe {
		want := strings.Fields(s)
		if len(want) > 0 && len(f) >= len(want) && strings.Join(f[:len(want)], " ") == strings.Join(want, " ") {
			return true
		}
	}
	return false
}

func ask() bool {
	askMu.Lock()
	defer askMu.Unlock()
	fmt.Fprint(os.Stderr, "  run it? [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(strings.ToLower(line)) == "y"
}
