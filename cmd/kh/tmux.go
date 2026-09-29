// Package main implements the kh command line.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"kh/internal/image"
)

var agentName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func tmux(args ...string) (string, error) {
	b, err := exec.Command("tmux", args...).CombinedOutput()
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

func address() (string, error) {
	if os.Getenv("TMUX") == "" {
		return "", fmt.Errorf("not inside tmux")
	}
	s, err := tmux("display-message", "-p", "#{session_name}:#{@kh_name}:#{window_name}")
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid tmux address: %q", s)
	}
	if parts[1] != "" {
		return parts[0] + ":" + parts[1], nil
	}
	return parts[0] + ":" + parts[2], nil
}

func validAddress(s string) bool {
	name, ok := strings.CutPrefix(s, "kh:")
	return ok && agentName.MatchString(name)
}

// Agent identities belong to panes, not windows: an agent keeps its name when
// its pane is moved or joined to a different window.
type namedPane struct {
	id, name, state string
}

func agentPanes() ([]namedPane, error) {
	s, err := tmux("list-panes", "-s", "-t", "kh", "-F", "#{pane_id}|#{@kh_name}|#{window_name}|#{window_panes}|#{@kh_state}")
	if err != nil {
		return nil, err
	}
	var panes []namedPane
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Split(line, "|")
		if len(fields) != 5 {
			continue
		}
		name := fields[1]
		if name == "" && fields[3] == "1" { // older, one-agent-per-window sessions
			name = fields[2]
		}
		if name != "" {
			panes = append(panes, namedPane{fields[0], name, fields[4]})
		}
	}
	return panes, nil
}

func agentPane(name string) (string, error) {
	panes, err := agentPanes()
	if err != nil {
		return "", err
	}
	for _, pane := range panes {
		if pane.name == name {
			return pane.id, nil
		}
	}
	return "", fmt.Errorf("agent kh:%s not found", name)
}

func agentCommand(args []string, auto bool) error {
	switch args[0] {
	case "spawn":
		if len(args) != 3 || !agentName.MatchString(args[1]) || args[1] == "main" || args[2] == "" {
			return fmt.Errorf("usage: kh spawn <name> <task> (name cannot be main)")
		}
		parent, err := address()
		if err != nil {
			return err
		}
		if !validAddress(parent) {
			return fmt.Errorf("agents must be in the kh tmux session")
		}
		panes, err := agentPanes()
		if err != nil {
			return err
		}
		for _, pane := range panes {
			if pane.name == args[1] {
				return fmt.Errorf("agent kh:%s already exists", pane.name)
			}
		}
		// tmux window names must also be unique, even when their agent moved away.
		names, err := tmux("list-windows", "-t", "kh", "-F", "#{window_name}")
		if err != nil {
			return err
		}
		for _, n := range strings.Split(names, "\n") {
			if n == args[1] {
				return fmt.Errorf("window kh:%s already exists", n)
			}
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		cmd := []string{exe}
		if auto {
			cmd = append(cmd, "--auto")
		}
		cmd = append(cmd, "-i", args[2])
		_, err = tmux("new-window", "-d", "-t", "kh:", "-n", args[1], "-c", cwd, "-e", "KH_PARENT="+parent, shellCommand(cmd...))
		if err != nil {
			return err
		}
		// Store the name on the pane so joining it to another window does not
		// break kh send/peek/agents. The title also labels pane borders in tmux.
		target := "kh:" + args[1] + ".0"
		if _, err = tmux("set-option", "-p", "-t", target, "@kh_name", args[1]); err != nil {
			return err
		}
		if _, err = tmux("select-pane", "-t", target, "-T", args[1]); err != nil {
			return err
		}
		if _, err = tmux("set-option", "-w", "-t", target, "pane-border-status", "top"); err != nil {
			return err
		}
		if _, err = tmux("set-option", "-w", "-t", target, "pane-border-format", " #{pane_title} "); err != nil {
			return err
		}
		fmt.Println("spawned kh:" + args[1])
		return nil
	case "send":
		if len(args) != 3 || !validAddress(args[1]) || args[2] == "" || strings.ContainsAny(args[2], "\r\n") {
			return fmt.Errorf("usage: kh send kh:<name> <single-line message>")
		}
		name := strings.TrimPrefix(args[1], "kh:")
		pane, err := agentPane(name)
		if err != nil {
			return err
		}
		if _, err = tmux("send-keys", "-t", pane, "-l", "--", args[2]); err != nil {
			return err
		}
		_, err = tmux("send-keys", "-t", pane, "Enter")
		return err
	case "peek":
		if len(args) != 2 || !validAddress(args[1]) {
			return fmt.Errorf("usage: kh peek kh:<name>")
		}
		pane, err := agentPane(strings.TrimPrefix(args[1], "kh:"))
		if err != nil {
			return err
		}
		s, err := tmux("capture-pane", "-p", "-t", pane, "-S", "-25")
		if err == nil {
			fmt.Println(s)
		}
		return err
	case "agents":
		if len(args) != 1 {
			return fmt.Errorf("usage: kh agents")
		}
		panes, err := agentPanes()
		if err != nil {
			return err
		}
		for _, pane := range panes {
			state := pane.state
			if state == "" {
				state = "unknown"
			}
			fmt.Printf("kh:%s  %s\n", pane.name, state)
		}
		return nil
	}
	return fmt.Errorf("unknown agent command: %s", args[0])
}

func setAgentState(state string) {
	if os.Getenv("TMUX") != "" {
		_, _ = tmux("set-option", "-p", "@kh_state", state)
		_, _ = tmux("set-option", "-w", "@kh_state", state) // older windows
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

func attachChat(args []string) error {
	if os.Getenv("TMUX") != "" {
		return nil
	}
	in, _ := os.Stdin.Stat()
	out, _ := os.Stdout.Stat()
	if in == nil || out == nil || in.Mode()&os.ModeCharDevice == 0 || out.Mode()&os.ModeCharDevice == 0 {
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
		if _, err = tmux("new-session", "-d", "-s", "kh", "-n", "main", "-c", cwd, shellCommand(append([]string{exe}, args...)...)); err != nil {
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
