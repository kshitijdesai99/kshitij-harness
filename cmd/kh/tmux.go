// Package main implements the kh command line.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
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
	return tmux("display-message", "-p", "#{session_name}:#{window_name}")
}

func validAddress(s string) bool {
	name, ok := strings.CutPrefix(s, "kh:")
	return ok && agentName.MatchString(name)
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
		names, err := tmux("list-windows", "-t", "kh", "-F", "#{window_name}")
		if err != nil {
			return err
		}
		for _, n := range strings.Split(names, "\n") {
			if n == args[1] {
				return fmt.Errorf("agent kh:%s already exists", n)
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
		if err == nil {
			fmt.Println("spawned kh:" + args[1])
		}
		return err
	case "send":
		if len(args) != 3 || !validAddress(args[1]) || args[2] == "" || strings.ContainsAny(args[2], "\r\n") {
			return fmt.Errorf("usage: kh send kh:<name> <single-line message>")
		}
		if _, err := tmux("send-keys", "-t", args[1], "-l", "--", args[2]); err != nil {
			return err
		}
		_, err := tmux("send-keys", "-t", args[1], "Enter")
		return err
	case "peek":
		if len(args) != 2 || !validAddress(args[1]) {
			return fmt.Errorf("usage: kh peek kh:<name>")
		}
		s, err := tmux("capture-pane", "-p", "-t", args[1], "-S", "-25")
		if err == nil {
			fmt.Println(s)
		}
		return err
	case "agents":
		if len(args) != 1 {
			return fmt.Errorf("usage: kh agents")
		}
		s, err := tmux("list-windows", "-t", "kh", "-F", "#{window_name} #{@kh_state}")
		if err != nil {
			return err
		}
		for _, line := range strings.Split(s, "\n") {
			name, state, _ := strings.Cut(line, " ")
			if state == "" {
				state = "unknown"
			}
			fmt.Printf("kh:%s  %s\n", name, state)
		}
		return nil
	}
	return fmt.Errorf("unknown agent command: %s", args[0])
}

func setAgentState(state string) {
	if os.Getenv("TMUX") != "" {
		_, _ = tmux("set-option", "-w", "@kh_state", state)
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
	}
	cmd := exec.Command("tmux", "attach-session", "-t", "kh")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
