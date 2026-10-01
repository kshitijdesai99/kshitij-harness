// kh is a lightweight, fast coding harness.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"kh/internal/config"
	"kh/internal/image"
	"kh/internal/memory"
	"kh/internal/provider"
	"kh/internal/repomap"
	"kh/internal/session"
	"kh/internal/terminal"
	"kh/internal/tools"
)

func main() {
	cfg, err := config.Load()
	exit(err)
	fromFile := cfg // before inheritance/flags, to spot later file changes
	for key, field := range map[string]*string{
		"KH_PROVIDER": &cfg.Provider, "KH_MODEL": &cfg.Model, "KH_EFFORT": &cfg.Effort,
	} {
		if value := os.Getenv(key); value != "" {
			*field = value
		}
	}
	// Flags override the config file, which overrides the defaults.
	flag.StringVar(&cfg.Provider, "provider", cfg.Provider, "backend adapter")
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
	if len(args) > 0 && args[0] == "login" {
		if len(args) != 2 {
			exit(fmt.Errorf("usage: kh login <provider>"))
			return
		}
		exit(provider.Login(ctx, args[1]))
		fmt.Println("Logged in.")
		return
	}
	if len(args) > 0 {
		switch args[0] {
		case "clipboard-paste":
			if len(args) != 2 {
				exit(fmt.Errorf("usage: kh clipboard-paste <tmux-pane>"))
				return
			}
			exit(clipboardPaste(args[1]))
			return
		case "spawn", "send", "peek", "agents":
			exit(agentCommand(args, cfg))
			return
		case "memory":
			exit(memoryCommand(args[1:]))
			return
		}
	}
	if (len(args) == 0 || *stay) && strings.Join(args, " ") != "sessions" && os.Getenv("TMUX") == "" {
		exit(attachChat(os.Args[1:]))
		// attachChat returns immediately for redirected input (no terminal).
		if fi, _ := os.Stdin.Stat(); fi != nil && fi.Mode()&os.ModeCharDevice != 0 {
			return
		}
	}

	if os.Getenv("TMUX") != "" && (len(args) == 0 || *stay) {
		installImagePasteBinding()
		defer clearImagePastePane()
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
	console := terminal.NewConsole(os.Stdin, os.Stdout, os.Stderr)
	defer console.Close()
	ui := terminal.Renderer{Out: os.Stdout, Err: os.Stderr}
	ts := []tools.Tool{tools.Bash(cfg, console), tools.Edit, tools.Memory(db, repoScope)}
	if *resume {
		*id, err = session.Latest()
		exit(err)
	}
	var saved session.Record
	if *id != "" {
		b, err := session.Load(*id)
		exit(err)
		saved, err = session.Decode(b)
		exit(err)
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "provider" && cfg.Provider != saved.Provider {
				exit(fmt.Errorf("session uses provider %q, not %q", saved.Provider, cfg.Provider))
			}
		})
		cfg.Provider = saved.Provider
	}
	p, err := provider.New(cfg, repomap.Build(".", cfg.MapCap), ts, ui)
	exit(err)
	console.Start()
	if *id != "" {
		exit(p.Load(saved.State))
		if len(args) == 0 || *stay {
			fmt.Printf("--- session %s ---\n", *id)
			p.Replay(terminal.Renderer{Out: os.Stdout, Err: os.Stdout})
			fmt.Println("---")
		}
	} else {
		*id = session.New()
	}

	exportModel(p, cfg.Provider)
	c := chat{model: p, backend: cfg.Provider, id: *id, repo: repoScope,
		memory: db, input: console, ui: ui, tools: ts, seen: fromFile}
	turn := func(msg string) error { return c.turn(ctx, msg) }

	setAgentState("waiting")
	task := strings.Join(args, " ")
	if task != "" && !*stay {
		exit(turn(task))
		fmt.Fprintf(os.Stderr, "(session %s: kh -r to continue)\n", *id)
		return
	}
	model, effort := p.Use("", "")
	fmt.Fprintf(os.Stderr, "kh chat, session %s (%s, %s). Type while it works to steer. Ctrl-C stops a task, Ctrl-D quits, /help for commands.\n", *id, model, effort)
	if task != "" {
		ui.Query(task)
		if err := turn(task); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
	}
	for {
		line, ok := <-console.Lines
		if !ok { // Ctrl-D
			fmt.Println()
			return
		}
		if line == "/image" || strings.HasPrefix(line, "/image ") {
			attacher, ok := p.(provider.ImageAttacher)
			if !ok {
				fmt.Fprintln(os.Stderr, "image: this provider does not support images")
				continue
			}
			path := strings.TrimSpace(strings.TrimPrefix(line, "/image"))
			url, err := image.Read(path)
			if err != nil {
				fmt.Fprintln(os.Stderr, "image:", err)
				continue
			}
			attacher.AttachImage(url)
			fmt.Println("(image attached; type your message)")
			continue
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
		fmt.Println("/model [id]   show or switch the model, for all sessions\n/effort [low|medium|high]   show or switch thinking effort, for all sessions\n/image [path]   attach a clipboard image (macOS) or a local image for your next message")
		return
	}
	fmt.Println(terminal.Color(os.Stdout, terminal.Muted, fmt.Sprintf("(model %s, effort %s)", model, effort)))
}

func exit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
