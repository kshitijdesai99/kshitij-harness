// kh is a lightweight, fast coding harness.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"kh/internal/config"
	"kh/internal/help"
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
	configErr := err
	if configErr != nil {
		cfg = config.Defaults
	} // --rebuild does not need valid chat config
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
	list := flag.Bool("sessions", false, "list this folder's saved sessions")
	stay := flag.Bool("i", false, "stay in chat after the task")
	rebuildFlag := flag.Bool("rebuild", false, "rebuild this executable from local kh sources, then exit")
	helpFlag := flag.Bool("help", false, "show usage, or answer a one-off question without saving a session")
	flag.BoolVar(helpFlag, "h", false, "alias for --help")
	flag.Parse()
	if *rebuildFlag {
		otherFlags := false
		flag.Visit(func(f *flag.Flag) {
			if f.Name != "rebuild" {
				otherFlags = true
			}
		})
		if otherFlags || flag.NArg() != 0 {
			exit(fmt.Errorf("kh --rebuild takes no other flags or task; rebuild, then run kh -r separately"))
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		exit(rebuild(ctx))
		return
	}
	if *helpFlag {
		if flag.NArg() == 0 {
			flag.Usage()
			return
		}
		if *resume || *id != "" || *stay || *list {
			exit(fmt.Errorf("kh --help with a request cannot be combined with -r, -s, -i, or --sessions"))
		}
		exit(configErr)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		var usage strings.Builder
		previousOutput := flag.CommandLine.Output()
		flag.CommandLine.SetOutput(&usage)
		flag.PrintDefaults()
		flag.CommandLine.SetOutput(previousOutput)
		exit(help.Run(ctx, cfg, strings.Join(flag.Args(), " "), usage.String(), terminal.Renderer{Out: os.Stdout, Err: os.Stderr}))
		return
	}
	exit(configErr)
	cfg.Sandbox = cfg.Sandbox && !*noSandbox
	if cfg.Auto {
		os.Setenv("KH_AUTO", "1") // carry --auto into bash-invoked kh subcommands
	} else if os.Getenv("KH_AUTO") == "1" {
		cfg.Auto = true
	}
	args := flag.Args()
	if *list || strings.Join(args, " ") == "sessions" {
		listSessions(cfg)
		return
	}
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

	db, err := memory.Open(memory.Path())
	exit(err)
	defer db.Close()
	cwd, err := os.Getwd()
	exit(err)
	repoScope := memory.RepoScope(cwd)
	console := terminal.NewConsole(os.Stdin, os.Stdout, os.Stderr)
	defer console.Close()
	ui := terminal.Renderer{Out: os.Stdout, Err: os.Stderr}
	if os.Getenv("TMUX") != "" {
		exit(nameChatPane())
	}
	activityName := "kh"
	if self, err := address(); err == nil {
		activityName = strings.TrimPrefix(self, "kh:")
	}
	ui.Activity = terminal.NewActivity(os.Stdout, activityName)
	defer ui.Activity.Close()
	console.SetActivity(ui.Activity)
	ts := []tools.Tool{tools.Bash(cfg, console), tools.Edit, tools.Memory(db, repoScope), agentTool(cfg, ui.Action)}
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
		// Multiline paste is message content, never a batch of slash commands.
		singleLine := !strings.ContainsAny(line, "\r\n")
		if singleLine && (line == "/image" || strings.HasPrefix(line, "/image ")) {
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
		if singleLine && line == "/compact" {
			if err := c.compact(ctx); err != nil {
				fmt.Fprintln(os.Stderr, "compact:", err)
			}
			continue
		}
		if singleLine && strings.HasPrefix(line, "/") {
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
		fmt.Println("/compact   summarize context and save the session\n/model [id]   show or switch the model, for all sessions\n/effort [low|medium|high]   show or switch thinking effort, for all sessions\n/image [path]   attach a clipboard image (macOS) or a local image for your next message")
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
