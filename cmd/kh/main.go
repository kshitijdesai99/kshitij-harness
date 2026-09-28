// kh is a lightweight, fast coding harness.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"kh/internal/agent"
	"kh/internal/auth"
	"kh/internal/config"
	"kh/internal/memory"
	"kh/internal/provider"
	"kh/internal/repomap"
	"kh/internal/session"
	"kh/internal/tools"
)

func main() {
	cfg, err := config.Load()
	exit(err)
	fromFile := cfg // before flags, to spot later /model or /effort switches
	// Flags override the config file, which overrides the defaults.
	flag.StringVar(&cfg.Model, "model", cfg.Model, "model id")
	flag.StringVar(&cfg.Effort, "effort", cfg.Effort, "reasoning effort: low, medium, high")
	flag.BoolVar(&cfg.Auto, "auto", cfg.Auto, "run every command without asking")
	noSandbox := flag.Bool("nosandbox", false, "let bash write outside the project")
	resume := flag.Bool("r", false, "resume the latest session")
	id := flag.String("s", "", "resume a session by id (files in ~/.kh/sessions)")
	stay := flag.Bool("i", false, "stay in chat after the task")
	flag.Parse()
	cfg.Sandbox = cfg.Sandbox && !*noSandbox
	if cfg.Auto {
		os.Setenv("KH_AUTO", "1") // carry --auto into bash-invoked kh subcommands
	} else if os.Getenv("KH_AUTO") == "1" {
		cfg.Auto = true
	}
	args := flag.Args()
	ctx := context.Background()
	if len(args) > 0 {
		switch args[0] {
		case "spawn", "send", "peek", "agents":
			exit(agentCommand(args, cfg.Auto))
			return
		case "memory":
			exit(memoryCommand(args[1:]))
			return
		}
	}
	if (len(args) == 0 || *stay) && strings.Join(args, " ") != "sessions" && strings.Join(args, " ") != "login codex" && os.Getenv("TMUX") == "" {
		exit(attachChat(os.Args[1:]))
		// attachChat returns immediately for redirected input (no terminal).
		if fi, _ := os.Stdin.Stat(); fi != nil && fi.Mode()&os.ModeCharDevice != 0 {
			return
		}
	}

	if strings.Join(args, " ") == "login codex" {
		exit(auth.Login(ctx))
		fmt.Println("Logged in.")
		return
	}
	if strings.Join(args, " ") == "sessions" {
		listSessions(cfg)
		return
	}

	db, err := memory.Open(memory.Path())
	exit(err)
	defer db.Close()
	cwd, err := os.Getwd()
	exit(err)
	repoScope := memory.RepoScope(cwd)
	tools.StartInput()
	ts := []tools.Tool{tools.Bash(cfg), tools.Edit, tools.Memory(db, repoScope)}
	// Built once per session, so it stays a stable, cacheable part of the prompt.
	p := provider.NewCodex(cfg, repomap.Build(".", cfg.MapCap), ts)

	if *resume {
		*id, err = session.Latest()
		exit(err)
	}
	if *id != "" {
		b, err := session.Load(*id)
		exit(err)
		exit(p.Load(b))
		if len(args) == 0 || *stay { // chat: show where we left off
			fmt.Printf("--- session %s ---\n%s", *id, grey)
			p.Replay(os.Stdout)
			fmt.Println(reset + "---")
		}
	} else {
		*id = session.New()
	}

	// One user message. Ctrl-C cancels just this turn; saving after every
	// turn means a closed terminal loses at most the current one.
	seen := fromFile // a -model/-effort flag holds until the file itself changes
	turn := func(msg string) error {
		setAgentState("busy")
		defer setAgentState("waiting")
		// Follow /model or /effort switches made in any session since the last turn.
		if now, err := config.Load(); err == nil {
			if now.Model != seen.Model {
				p.Use(now.Model, "")
			}
			if now.Effort != seen.Effort {
				p.Use("", now.Effort)
			}
			seen.Model, seen.Effort = now.Model, now.Effort
		}
		tctx, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		start := time.Now()
		// Search discovery metadata first and load only a few matching details.
		// Dynamic memories are sent for this turn, not saved in conversation history
		// or appended to the cacheable system prefix.
		relevant, e := db.Relevant(tctx, repoScope, msg)
		if e != nil {
			return fmt.Errorf("retrieve memory: %w", e)
		}
		p.SetMemory(relevant)
		err := agent.Run(tctx, p, ts, msg, tools.Lines)
		if tctx.Err() == context.Canceled { // Ctrl-C: expected, not an error
			fmt.Fprintln(os.Stderr, "\n(stopped)")
			err = nil
		}
		if s := p.Stats(); s.In > 0 {
			fmt.Fprintf(os.Stderr, "%s(ttft %s, total %s, %s in, %s cached %d%%, %s out, %s thinking, %s)%s\n", grey,
				secs(s.TTFT), secs(time.Since(start)), k(s.In), k(s.Cached), s.Cached*100/s.In, k(s.Out), k(s.Think), contextUsed(s), reset)
		}
		if b, e := p.Save(); e == nil {
			session.Save(*id, b)
		}
		return err
	}

	setAgentState("waiting")
	task := strings.Join(args, " ")
	if task != "" && !*stay {
		exit(turn(task))
		fmt.Fprintf(os.Stderr, "(session %s: kh -r to continue)\n", *id)
		return
	}
	fmt.Fprintf(os.Stderr, "kh chat, session %s (%s, %s). Type while it works to steer. Ctrl-C stops a task, Ctrl-D quits, /help for commands.\n", *id, cfg.Model, cfg.Effort)
	if task != "" {
		fmt.Println(">", task)
		if err := turn(task); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
	}
	for {
		fmt.Print("> ")
		line, ok := <-tools.Lines
		if !ok { // Ctrl-D
			fmt.Println()
			return
		}
		if strings.HasPrefix(line, "/") {
			command(p, line)
		} else if line != "" {
			if err := turn(line); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
		}
	}
}

// Grey for replayed history, only when printing to a terminal.
var grey, reset = func() (string, string) {
	if fi, _ := os.Stdout.Stat(); fi.Mode()&os.ModeCharDevice != 0 {
		return "\033[90m", "\033[0m"
	}
	return "", ""
}()

// k formats a token count: 950, 12.4k.
func k(n int) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// contextUsed reads "context 86k/272k 32%", or "context 86k" if the limit is unknown.
func contextUsed(s provider.Stats) string {
	if s.Window == 0 {
		return "context " + k(s.Context)
	}
	return fmt.Sprintf("context %s/%s %d%%", k(s.Context), k(s.Window), s.Context*100/s.Window)
}

func secs(d time.Duration) string {
	if d == 0 {
		return "-" // no text this turn
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// command handles chat slash commands. Switches apply from the next message.
func command(p provider.Provider, line string) {
	cmd, arg, _ := strings.Cut(line, " ")
	var model, effort string
	switch cmd {
	case "/model", "/effort":
		if arg = strings.TrimSpace(arg); arg != "" {
			// Saved to config, so every session (including open ones) follows.
			if err := config.Set(cmd[1:], arg); err != nil {
				fmt.Println("error:", err)
				return
			}
		}
		if cmd == "/model" {
			model, effort = p.Use(arg, "")
		} else {
			model, effort = p.Use("", arg)
		}
	default:
		fmt.Println("/model [id]   show or switch the model, for all sessions\n/effort [low|medium|high]   show or switch thinking effort, for all sessions")
		return
	}
	fmt.Printf("%s(model %s, effort %s)%s\n", grey, model, effort, reset)
}

// listSessions prints the last 20 sessions with their first message.
func listSessions(cfg config.Config) {
	ids := session.List()
	ids = ids[max(0, len(ids)-20):]
	for _, id := range ids {
		b, _ := session.Load(id)
		p := provider.NewCodex(cfg, "", nil)
		var buf strings.Builder
		if p.Load(b) == nil {
			p.Replay(&buf)
		}
		first, _, _ := strings.Cut(buf.String(), "\n")
		if len(first) > 70 {
			first = first[:70] + "..."
		}
		fmt.Printf("%s  %s\n", id, first)
	}
}

func exit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
