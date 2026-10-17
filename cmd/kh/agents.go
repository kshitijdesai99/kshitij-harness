package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"

	"kh/internal/config"
	"kh/internal/session"
	"kh/internal/tools"
)

// The CLI and native tool share one transport; no shell or recursive kh launch
// is needed to send, inspect, or create an agent (only the new agent runs kh).
func agentCommand(args []string, cfg config.Config) error {
	out, err := agentOperation(context.Background(), args, cfg)
	if err == nil && out != "" {
		fmt.Println(out)
	}
	return err
}

func agentTool(cfg config.Config, action func(string), access *tools.WriteAccess) tools.Tool {
	// Serialize this chat's spawns, including parallel tool calls. Reads and
	// messages need not wait for a model request or the spawned task to finish.
	spawning := make(chan struct{}, 1)
	tool := tools.Agents(func(ctx context.Context, request tools.AgentRequest) (string, error) {
		var args []string
		switch request.Action {
		case "spawn":
			select {
			case spawning <- struct{}{}:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			defer func() { <-spawning }()
			args = []string{"spawn", request.Name, request.Task}
			if request.Keep {
				args = []string{"spawn", "--keep", request.Name, request.Task}
			}
		case "send":
			args = []string{"send", request.Address, request.Message}
		case "peek":
			args = []string{"peek", request.Address}
		case "list":
			args = []string{"agents"}
		}
		active := cfg
		if access != nil {
			active.Auto = access.Auto()
		}
		for key, field := range map[string]*string{"KH_PROVIDER": &active.Provider, "KH_MODEL": &active.Model, "KH_EFFORT": &active.Effort} {
			if value := os.Getenv(key); value != "" {
				*field = value
			}
		}
		out, err := agentOperation(ctx, args, active)
		if err == nil && action != nil {
			if request.Action == "spawn" {
				action(out)
			}
			if request.Action == "send" {
				action("sent to " + request.Address)
			}
		}
		return out, err
	})
	if self, err := address(); err == nil {
		tool.Description += " Your address is " + self + "."
	}
	return tool
}

func agentOperation(ctx context.Context, args []string, cfg config.Config) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(args) == 0 {
		return "", fmt.Errorf("need agent command")
	}
	switch args[0] {
	case "spawn":
		keep := len(args) > 1 && args[1] == "--keep"
		if keep {
			args = append(args[:1:1], args[2:]...)
		}
		if len(args) != 3 || !agentName.MatchString(args[1]) || args[1] == "main" || strings.TrimSpace(args[2]) == "" {
			return "", fmt.Errorf("usage: kh spawn [--keep] <name> <task> (name cannot be main)")
		}
		parent, err := agentAddress(ctx)
		if err != nil {
			return "", err
		}
		panes, err := agentPanesContext(ctx)
		if err != nil {
			return "", err
		}
		current, err := currentPane(ctx)
		if err != nil {
			return "", err
		}
		window := ""
		for _, pane := range panes {
			if pane.name == args[1] {
				return "", fmt.Errorf("agent kh:%s already exists", pane.name)
			}
			if pane.id == current {
				window = pane.window
			}
		}
		if window == "" {
			return "", fmt.Errorf("current pane is not a named kh agent")
		}
		// Keep the first pane on the left, then stack workers on the right. Pick
		// an existing worker for a vertical split before rebalancing the column.
		target, direction := current, "-h"
		for _, pane := range panes {
			if pane.window == window && pane.name != "main" && pane.id != current {
				target, direction = pane.id, "-v"
			}
		}
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		command := []string{exe, "--provider", cfg.Provider, "--model", cfg.Model, "--effort", cfg.Effort, "--auto=" + strconv.FormatBool(cfg.Auto)}
		if !cfg.Sandbox {
			command = append(command, "--nosandbox")
		}
		// Without -i the agent exits after its task and tmux closes the pane.
		if keep {
			command = append(command, "-i")
		}
		command = append(command, args[2])
		pane, err := tmuxContext(ctx, "set-option", "-p", "-t", current, "@kh_name", strings.TrimPrefix(parent, "kh:"), ";",
			"split-window", direction, "-d", "-P", "-F", "#{pane_id}", "-t", target, "-c", cwd,
			"-e", "KH_PARENT="+parent, "-e", "KH_AGENT=kh:"+args[1], sessionCommand(false, command...))
		if err != nil {
			return "", err
		}
		// Batch metadata and layout into a single round trip. Detached creation
		// and title-only select-pane preserve the user's focus and current tab.
		_, err = tmuxContext(ctx,
			"set-option", "-p", "-t", pane, "@kh_name", args[1], ";",
			"select-pane", "-t", pane, "-T", args[1], ";",
			"set-option", "-w", "-t", pane, "pane-border-status", "top", ";",
			"set-option", "-w", "-t", pane, "pane-border-format", " #{pane_title} ", ";",
			"set-option", "-w", "-t", pane, "main-pane-width", "50%", ";",
			"select-layout", "-t", pane, "main-vertical")
		if err != nil {
			return "", fmt.Errorf("agent kh:%s started in %s, but layout setup failed: %w", args[1], pane, err)
		}
		return "spawned kh:" + args[1] + " (" + pane + ", same window; parent " + parent + ")", nil
	case "send":
		if len(args) != 3 || !validAddress(args[1]) || strings.TrimSpace(args[2]) == "" || strings.IndexFunc(args[2], unicode.IsControl) >= 0 {
			return "", fmt.Errorf("usage: kh send kh:<name> <single-line message>")
		}
		pane, err := findAgentPane(ctx, strings.TrimPrefix(args[1], "kh:"))
		if err != nil {
			return "", err
		}
		sender, err := agentAddress(ctx)
		if err != nil {
			return "", err
		}
		// Attribution also prevents a peer's "yes" from answering a bash approval.
		// Hex-byte transport preserves UTF-8 and literal trailing semicolons:
		// tmux otherwise interprets a final ';' as a command separator, even
		// in a send-keys argument. Include Enter in the same server command.
		message := "[from " + sender + "] " + args[2] + "\r"
		keys := []string{"send-keys", "-H", "-t", pane}
		const hex = "0123456789abcdef"
		for i := 0; i < len(message); i++ {
			b := message[i]
			keys = append(keys, string([]byte{hex[b>>4], hex[b&15]}))
		}
		_, err = tmuxContext(ctx, keys...)
		return "sent to " + args[1], err
	case "peek":
		if len(args) != 2 || !validAddress(args[1]) {
			return "", fmt.Errorf("usage: kh peek kh:<name>")
		}
		pane, err := findAgentPane(ctx, strings.TrimPrefix(args[1], "kh:"))
		if err != nil {
			return "", err
		}
		return tmuxContext(ctx, "capture-pane", "-p", "-t", pane, "-S", "-25")
	case "agents":
		if len(args) != 1 {
			return "", fmt.Errorf("usage: kh agents")
		}
		panes, err := agentPanesContext(ctx)
		if err != nil {
			return "", err
		}
		var out strings.Builder
		for _, pane := range panes {
			state := pane.state
			if state == "" {
				state = "unknown"
			}
			fmt.Fprintf(&out, "kh:%s  %s\n", pane.name, state)
		}
		return strings.TrimSuffix(out.String(), "\n"), nil
	}
	return "", fmt.Errorf("unknown agent command: %s", args[0])
}

// openChatPane opens a fresh chat or an immutable conversation checkpoint to
// the right. It never accesses the live provider, so its reply keeps running.
func openChatPane(ctx context.Context, cfg config.Config, c *chat, fork bool) (string, error) {
	current, err := currentPane(ctx)
	if err != nil {
		return "", fmt.Errorf("opening a chat pane needs tmux: %w", err)
	}
	c.forkMu.Lock()
	state, model, effort := c.forkState, c.forkModel, c.forkEffort
	c.forkMu.Unlock()
	id, base := "", "new"
	if fork {
		if len(state) == 0 {
			return "", fmt.Errorf("nothing to fork yet")
		}
		id, base = session.New(), "fork"
		if err := session.Save(id, state); err != nil {
			return "", err
		}
	}
	panes, err := agentPanesContext(ctx)
	if err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for _, pane := range panes {
		taken[pane.name] = true
	}
	name := base
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s-%d", base, n)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	auto := cfg.Auto
	if c.writeAccess != nil {
		auto = c.writeAccess.Auto()
	}
	command := []string{exe, "--provider", c.backend, "--model", model, "--effort", effort, "--auto=" + strconv.FormatBool(auto)}
	if !cfg.Sandbox {
		command = append(command, "--nosandbox")
	}
	if fork {
		command = append(command, "-s", id)
	}
	pane, err := tmuxContext(ctx, "split-window", "-h", "-P", "-F", "#{pane_id}", "-t", current, "-c", cwd,
		"-e", "KH_AGENT=kh:"+name, sessionCommand(false, command...))
	if err != nil {
		return "", err
	}
	// Reserve the address before accepting another command, even if the child
	// is still rebuilding. Do not wait for its startup to publish the name.
	if _, err := tmuxContext(ctx, "set-option", "-p", "-t", pane, "@kh_name", name, ";",
		"select-pane", "-t", pane, "-T", name, ";",
		"set-option", "-w", "-t", pane, "pane-border-status", "top", ";",
		"set-option", "-w", "-t", pane, "pane-border-format", " #{pane_title} "); err != nil {
		return "", fmt.Errorf("chat started in %s, but address setup failed: %w", pane, err)
	}
	if fork {
		return fmt.Sprintf("forked to right pane %s (session %s, address kh:%s)", name, id, name), nil
	}
	return fmt.Sprintf("opened new session in right pane %s (address kh:%s)", name, name), nil
}

func findAgentPane(ctx context.Context, name string) (string, error) {
	panes, err := agentPanesContext(ctx)
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

// Chats in an existing tmux session also receive a stable logical address.
func nameChatPane() error {
	ctx := context.Background()
	pane, err := currentPane(ctx)
	if err != nil {
		return err
	}
	existing, err := tmux("display-message", "-p", "-t", pane, "#{@kh_name}")
	if err != nil || existing != "" {
		return err
	}
	name := strings.TrimPrefix(os.Getenv("KH_AGENT"), "kh:")
	if !agentName.MatchString(name) {
		name = "main"
	}
	panes, err := agentPanesContext(ctx)
	if err != nil {
		return err
	}
	for _, other := range panes {
		if other.name == name && other.id != pane {
			name = "agent-" + strings.TrimPrefix(pane, "%")
			break
		}
	}
	_, err = tmux("set-option", "-p", "-t", pane, "@kh_name", name, ";", "select-pane", "-t", pane, "-T", name, ";",
		"set-option", "-w", "-t", pane, "pane-border-status", "top", ";",
		"set-option", "-w", "-t", pane, "pane-border-format", " #{pane_title} ")
	return err
}
