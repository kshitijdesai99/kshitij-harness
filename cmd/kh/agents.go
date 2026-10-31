package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"unicode"

	"github.com/chzyer/readline"

	"kh/internal/config"
	"kh/internal/ecosystem"
	"kh/internal/session"
	"kh/internal/terminal"
	"kh/internal/tools"
)

// The CLI and native tool share one transport; no shell or recursive kh launch
// is needed to send, inspect, or create an agent (only the new agent runs kh).
func agentCommand(args []string, cfg config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	out, err := agentOperation(ctx, args, cfg)
	if err == nil && out != "" {
		fmt.Println(out)
	}
	return err
}

func agentTool(cfg config.Config, action func(string), policy *tools.ApprovalPolicy) tools.Tool {
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
		case "stats":
			args = []string{"stats"}
		case "inspect":
			args = []string{"inspect", request.Address}
		case "set_role":
			args = []string{"set_role", request.Role}
		case "stop":
			args = []string{"stop", request.Address, request.Scope}
		}
		active := cfg
		if policy != nil {
			active.Auto = policy.Auto()
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

// agentTransport owns only tmux creation and isolated peer input. All admission
// and address resolution happen through the persistent registry.
func (e *ecosystemRuntime) agentTransport(ctx context.Context, args []string, cfg config.Config) (string, error) {
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
		parent := e.self.Address
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
		// One-shot workers exit after their task; keep retains interactive chat.
		if keep {
			command = append(command, "-i")
		} else {
			command = append(command, "-p")
		}
		command = append(command, args[2])
		reservation, ok := ctx.Value(spawnReservationKey{}).(ecosystem.Reservation)
		if !ok {
			return "", fmt.Errorf("spawn requires an ecosystem reservation")
		}
		// Ignore cancellation during the short tmux launch: killing the tmux
		// client leaves creation ambiguous and cannot justify releasing a slot.
		attempt, _ := ctx.Value(spawnAttemptKey{}).(*spawnAttempt)
		if attempt != nil {
			attempt.attempted = true
		}
		pane, err := tmuxContext(context.WithoutCancel(ctx), "split-window", direction, "-d", "-P", "-F", "#{pane_id}", "-t", target, "-c", cwd,
			"-e", "KH_PARENT="+parent, "-e", "KH_AGENT="+reservation.Agent.Address,
			"-e", "KH_ECOSYSTEM_ID="+reservation.Agent.EcosystemID,
			"-e", "KH_ECOSYSTEM_AGENT_ID="+reservation.Agent.ID,
			"-e", "KH_ECOSYSTEM_TOKEN="+reservation.Token,
			"tmux set-option -p -t \"$TMUX_PANE\" remain-on-exit failed && exec "+sessionCommand(false, command...))
		if err != nil {
			return "", err
		}
		if attempt != nil {
			attempt.created = true
		}
		// Record the pane process even if kh fails before its early Claim.
		pidText, pidErr := tmuxContext(context.WithoutCancel(ctx), "display-message", "-p", "-t", pane, "#{pane_pid}")
		if pidErr == nil {
			if pid, err := strconv.Atoi(pidText); err == nil {
				if process, err := ecosystem.ProcessForPID(pid); err == nil {
					_ = e.store.ObserveLaunch(context.WithoutCancel(ctx), reservation.Agent.ID, reservation.Token, process, ecosystem.PaneInfo{PaneID: pane})
					if observed, err := e.store.Get(context.WithoutCancel(ctx), reservation.Agent.ID); err == nil && observed.State == ecosystem.Stopping {
						_, _ = e.stop(context.WithoutCancel(ctx), reservation.Agent.Address, "agent")
					}
				} else if os.IsNotExist(err) {
					// An exec failure can exit before kh gets to Claim. A dead
					// pane proves this launch cannot later claim its ticket.
					dead, _ := tmuxContext(context.WithoutCancel(ctx), "display-message", "-p", "-t", pane, "#{pane_dead}")
					if dead == "1" {
						_ = e.store.FailLaunch(context.WithoutCancel(ctx), reservation.Agent.ID, reservation.Token, true)
					}
				}
			}
		}
		// Batch metadata and layout into a single round trip. Detached creation
		// and title-only select-pane preserve the user's focus and current tab.
		_, err = tmuxContext(context.WithoutCancel(ctx),
			"set-option", "-p", "-t", pane, "@kh_ecosystem", reservation.Agent.EcosystemID, ";",
			"set-option", "-p", "-t", pane, "@kh_agent_id", reservation.Agent.ID, ";",
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
		receiver, err := e.store.Resolve(ctx, e.self.EcosystemID, args[1])
		if err != nil {
			return "", err
		}
		if !receiver.State.Live() || !receiver.Process.Valid() || receiver.Pane.PaneID == "" {
			return "", fmt.Errorf("%s has no live receiver", receiver.Address)
		}
		pane := receiver.Pane.PaneID
		// Never fall back to raw keys: a legacy receiver would append them to
		// the human draft. The PID also rejects markers left by a crashed chat.
		capability, err := tmuxContext(ctx, "display-message", "-p", "-t", pane, "#{@kh_peer_input}")
		if err != nil {
			return "", err
		}
		pid, parseErr := strconv.Atoi(capability)
		if parseErr != nil || pid <= 0 || syscall.Kill(pid, 0) != nil {
			return "", fmt.Errorf("%s is not ready for isolated peer input; wait if it is starting, or restart it onto the new build", args[1])
		}
		identity, err := ecosystem.ProcessForPID(pid)
		if err != nil || identity != receiver.Process {
			return "", fmt.Errorf("%s is not ready for isolated peer input: receiver identity changed", args[1])
		}
		sender := e.self.Address
		// Attribution also prevents a peer's "yes" from answering a bash approval.
		// Hex-byte transport preserves UTF-8 and literal trailing semicolons:
		// tmux otherwise interprets a final ';' as a command separator, even
		// in a send-keys argument. The console consumes this frame separately
		// from the human editor; never inject Enter or append to its draft.
		message := terminal.PeerMessage("[from " + sender + "] " + args[2])
		keys := []string{"send-keys", "-H", "-t", pane}
		const hex = "0123456789abcdef"
		for i := 0; i < len(message); i++ {
			b := message[i]
			keys = append(keys, string([]byte{hex[b>>4], hex[b&15]}))
		}
		_, err = tmuxContext(ctx, keys...)
		return "sent to " + args[1], err
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
	if fork {
		saved, err := session.Decode(state)
		if err != nil {
			return "", err
		}
		state, err = session.EncodeWithIdentity(saved.Provider, saved.State, session.NewIdentity("", name))
		if err != nil {
			return "", err
		}
		if err := session.Save(id, state); err != nil {
			return "", err
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
	auto := cfg.Auto
	if c.policy != nil {
		auto = c.policy.Auto()
	}
	command := []string{exe, "--provider", c.backend, "--model", model, "--effort", effort, "--auto=" + strconv.FormatBool(auto)}
	if fork {
		command = append(command, "-s", id)
	}
	pane, err := tmuxContext(ctx, "split-window", "-h", "-P", "-F", "#{pane_id}", "-t", current, "-c", cwd,
		"-e", "KH_PARENT=", "-e", "KH_AGENT=kh:"+name,
		"-e", "KH_ECOSYSTEM_ID=", "-e", "KH_ECOSYSTEM_AGENT_ID=", "-e", "KH_ECOSYSTEM_TOKEN=", sessionCommand(false, command...))
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

// Advertise only a live terminal receiver, not redirected children sharing its
// pane environment. Clear only our own marker; PID checks reject crashed chats.
func setPeerInput(active bool) error {
	if os.Getenv("TMUX") == "" || !readline.IsTerminal(int(os.Stdin.Fd())) || !readline.IsTerminal(int(os.Stdout.Fd())) {
		return nil
	}
	pane, err := currentPane(context.Background())
	if err != nil {
		return err
	}
	self := strconv.Itoa(os.Getpid())
	value := self
	if !active {
		owner, err := tmux("display-message", "-p", "-t", pane, "#{@kh_peer_input}")
		if err != nil || owner != self {
			return err // a nested/older chat must not clear another receiver
		}
		value = ""
	}
	_, err = tmux("set-option", "-p", "-t", pane, "@kh_peer_input", value)
	return err
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
	if currentEcosystem != nil {
		name = currentEcosystem.self.Name
	}
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
