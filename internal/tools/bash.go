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

// termMu keeps parallel calls from printing over a pending y/n question.
var termMu sync.Mutex

// Bash runs one command per call.
func Bash(c config.Config) Tool {
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
			if json.Unmarshal(input, &in) != nil || in.Command == "" {
				return "", fmt.Errorf("need a command")
			}
			StartInput()
			if !show(in.Command, c.Auto || isSafe(in.Command, c.Safe)) {
				return "", fmt.Errorf("user said no")
			}
			out, err := run(ctx, append(argv, in.Command), timeout, c.OutputCap)
			// The error is the same for our sandbox and for macOS privacy, so name both.
			if c.Sandbox && strings.Contains(out, "Operation not permitted") {
				out += "\n(kh: if this was a write outside the project, kh's sandbox blocked it; the user can rerun with -nosandbox or add the dir to writable in ~/.kh/config.json. " +
					"If it was a read, macOS privacy settings are blocking this folder for the user's terminal app.)"
			}
			return out, err
		},
	}
}

func run(ctx context.Context, argv []string, timeout time.Duration, outputCap int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
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
	return []string{"/usr/bin/sandbox-exec", "-p", p + ")"}
}

// isSafe allows safe commands joined by | ; && ||. Redirects, backgrounding (&),
// substitution ($, `) and newlines never pass. A safe entry like "git diff"
// matches the leading words of a command.
func isSafe(c string, safe []string) bool {
	c = strings.NewReplacer("&&", "|", "||", "|", ";", "|").Replace(c)
	if strings.ContainsAny(c, "&><`$\n") {
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

// show prints the command and, unless it is ok to run, asks the user.
// The question sits right under its own command.
func show(c string, ok bool) bool {
	termMu.Lock()
	defer termMu.Unlock()
	fmt.Fprintln(os.Stderr, "$", c)
	if ok {
		return true
	}
	for {
		fmt.Fprint(os.Stderr, "  run it? [y/N] ")
		switch line := <-Lines; strings.ToLower(line) { // "" once closed = no
		case "y", "yes":
			return true
		case "", "n", "no":
			return false
		default: // typed to steer, not to answer: keep it for the model
			held = append(held, line)
			fmt.Fprintln(os.Stderr, "  (noted for the model)")
		}
	}
}

var held []string // steering lines typed while a y/n question was open

// Held returns and clears steering lines caught by a y/n question.
func Held() []string {
	termMu.Lock()
	defer termMu.Unlock()
	h := held
	held = nil
	return h
}
