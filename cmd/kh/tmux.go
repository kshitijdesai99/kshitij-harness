// Package main implements the kh command line.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"kh/internal/image"
)

var agentName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func tmux(args ...string) (string, error) {
	return tmuxContext(context.Background(), args...)
}

func tmuxContext(ctx context.Context, args ...string) (string, error) {
	b, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("tmux %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(b)), err)
	}
	return strings.TrimSpace(string(b)), nil
}

// quote a single shell argument: tmux runs its window command through a shell.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func shellCommand(args ...string) string {
	for i, s := range args {
		args[i] = shellQuote(s)
	}
	return strings.Join(args, " ")
}

func currentPane(ctx context.Context) (string, error) {
	if os.Getenv("TMUX") == "" {
		return "", fmt.Errorf("not inside tmux")
	}
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		return pane, nil
	}
	return tmuxContext(ctx, "display-message", "-p", "#{pane_id}")
}

func address() (string, error) { return agentAddress(context.Background()) }

func agentAddress(ctx context.Context) (string, error) {
	pane, err := currentPane(ctx)
	if err != nil {
		return "", err
	}
	s, err := tmuxContext(ctx, "display-message", "-p", "-t", pane, "#{@kh_name}|#{window_name}")
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(s, "|", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid tmux address: %q", s)
	}
	name := parts[0]
	if name == "" {
		name = parts[1]
	}
	if !agentName.MatchString(name) {
		return "", fmt.Errorf("this pane needs an agent name")
	}
	return "kh:" + name, nil
}

func validAddress(s string) bool {
	name, ok := strings.CutPrefix(s, "kh:")
	return ok && agentName.MatchString(name)
}

// Agent identities belong to panes, not windows: an agent keeps its name when
// its pane is moved or joined to a different window. kh:<name> is a logical
// address scoped to the caller's tmux session, not a hard-coded session name.
type namedPane struct {
	id, name, state, window string
}

func agentPanesContext(ctx context.Context) ([]namedPane, error) {
	target, err := currentPane(ctx)
	if err != nil {
		return nil, err
	}
	s, err := tmuxContext(ctx, "list-panes", "-s", "-t", target, "-F", "#{pane_id}|#{@kh_name}|#{window_name}|#{window_panes}|#{@kh_state}|#{window_id}")
	if err != nil {
		return nil, err
	}
	var panes []namedPane
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Split(line, "|")
		if len(fields) != 6 {
			continue
		}
		name := fields[1]
		if name == "" && fields[3] == "1" { // older, one-agent-per-window sessions
			name = fields[2]
		}
		if name != "" {
			panes = append(panes, namedPane{fields[0], name, fields[4], fields[5]})
		}
	}
	return panes, nil
}

func setAgentState(state string) {
	if os.Getenv("TMUX") != "" {
		pane, err := currentPane(context.Background())
		if err == nil {
			_, _ = tmux("set-option", "-p", "-t", pane, "@kh_state", state, ";",
				"set-option", "-w", "-t", pane, "@kh_state", state)
		}
	}
}

// clipboardPaste runs at the Ctrl-V keystroke. A failed read leaves the
// user's input unchanged and shows the pasteboard diagnosis immediately.
func clipboardPaste(pane string) error {
	if !regexp.MustCompile(`^%[0-9]+$`).MatchString(pane) {
		return fmt.Errorf("invalid tmux pane")
	}
	active, err := tmux("display-message", "-p", "-t", pane, "#{@kh_image_paste}")
	if err != nil || active != "1" {
		return fmt.Errorf("not an interactive kh pane")
	}
	marker, err := image.Snapshot()
	if err != nil {
		_, _ = tmux("display-message", "-t", pane, "-d", "7000", "Image paste: "+err.Error())
		return err
	}
	if _, err := tmux("send-keys", "-t", pane, "-l", "--", marker); err != nil {
		_, _ = image.Take(strings.TrimSuffix(strings.TrimPrefix(marker, "[[kh:image:"), "]]"))
		return err
	}
	return nil
}

// Ctrl-V keeps its usual behavior in shells, other apps, and other sessions.
func installImagePasteBinding() {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" { // A detached tmux command cannot reliably identify its own pane.
		return
	}
	if _, err := tmux("set-option", "-p", "-t", pane, "@kh_image_paste", "1"); err != nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	// tmux expands this pane option when the key is pressed; quoting protects
	// executable paths containing shell metacharacters or spaces.
	if _, err := tmux("set-option", "-p", "-t", pane, "@kh_image_command", shellCommand(exe, "clipboard-paste")); err != nil {
		return
	}
	keys, err := tmux("list-keys", "-T", "root")
	if err != nil {
		return
	}
	for _, line := range strings.Split(keys, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "bind-key" {
			continue
		}
		rootCtrlV := false
		for i := 1; i+2 < len(fields); i++ {
			if fields[i] == "-T" && fields[i+1] == "root" && fields[i+2] == "C-v" {
				rootCtrlV = true
				break
			}
		}
		if !rootCtrlV {
			continue
		}
		// Upgrade our old session-scoped binding, but never overwrite the
		// user's own Ctrl-V binding.
		if !strings.Contains(line, "[[kh:clipboard-image]]") && !strings.Contains(line, "@kh_image_command") {
			return
		}
		break
	}
	_, _ = tmux("bind-key", "-T", "root", "C-v", "if-shell", "-F", "#{==:#{@kh_image_paste},1}",
		"run-shell '#{@kh_image_command} #{pane_id}'", "send-keys C-v")
}

func clearImagePastePane() {
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		_, _ = tmux("set-option", "-p", "-t", pane, "-u", "@kh_image_paste")
		_, _ = tmux("set-option", "-p", "-t", pane, "-u", "@kh_image_command")
	}
}

// Explicitly forward startup settings rather than relying on a long-lived
// tmux server's environment. Empty values also clear stale server overrides.
func sessionCommand(rebuilt bool, args ...string) string {
	prefix := shellCommand("env", "KH_SOURCE_DIR="+os.Getenv("KH_SOURCE_DIR"), "KH_AUTO_REBUILD="+os.Getenv("KH_AUTO_REBUILD"))
	if rebuilt {
		// exec preserves the shell PID, making the skip marker valid only for
		// this new main pane, not for children it may later create.
		prefix = "exec " + prefix + " KH_REBUILD_EXEC_PID=$$"
	} else {
		prefix += " " + shellQuote("KH_REBUILD_EXEC_PID=")
	}
	return prefix + " " + shellCommand(args...)
}

func chatTerminal() bool {
	in, _ := os.Stdin.Stat()
	out, _ := os.Stdout.Stat()
	return in != nil && out != nil && in.Mode()&os.ModeCharDevice != 0 && out.Mode()&os.ModeCharDevice != 0
}

func attachChat(args []string, rebuilt bool) error {
	if os.Getenv("TMUX") != "" {
		return nil
	}
	if !chatTerminal() {
		return nil
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux is required for chat: %w", err)
	}
	if _, err := tmux("has-session", "-t", "=kh"); err != nil {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		if _, err = tmux("new-session", "-d", "-s", "kh", "-n", "main", "-c", cwd, sessionCommand(rebuilt, append([]string{exe}, args...)...)); err != nil {
			return err
		}
		if _, err = tmux("set-option", "-p", "-t", "kh:main.0", "@kh_name", "main"); err != nil {
			return err
		}
		if _, err = tmux("select-pane", "-t", "kh:main.0", "-T", "main"); err != nil {
			return err
		}
		if _, err = tmux("set-option", "-w", "-t", "kh:main", "pane-border-status", "top"); err != nil {
			return err
		}
		if _, err = tmux("set-option", "-w", "-t", "kh:main", "pane-border-format", " #{pane_title} "); err != nil {
			return err
		}
	}
	cmd := exec.Command("tmux", "attach-session", "-t", "kh")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
