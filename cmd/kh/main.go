// kh is a lightweight, fast coding harness.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"kh/internal/backend"
	"kh/internal/config"
	"kh/internal/help"
	"kh/internal/image"
	"kh/internal/learn"
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
	if value := os.Getenv("KH_AUTO"); value == "1" || value == "0" {
		cfg.Auto = value == "1"
	}
	// Flags override inherited settings and the config file.
	flag.StringVar(&cfg.Provider, "provider", cfg.Provider, "backend adapter")
	flag.StringVar(&cfg.Model, "model", cfg.Model, "model id")
	flag.StringVar(&cfg.Effort, "effort", cfg.Effort, "reasoning effort: low, medium, high")
	flag.BoolVar(&cfg.Auto, "auto", cfg.Auto, "automatically approve commands and requested folder writes; keep sandbox and path checks")
	// Older running chats pass this when spawning a rebuilt child.
	flag.BoolVar(&cfg.Auto, "auto-write", cfg.Auto, "deprecated alias for --auto")
	noSandbox := flag.Bool("nosandbox", false, "let bash write outside the project")
	resume := flag.Bool("r", false, "resume the latest session")
	id := flag.String("s", "", "resume a session by id (files in ~/.kh/sessions)")
	list := flag.Bool("sessions", false, "list this folder's saved sessions")
	stay := flag.Bool("i", false, "interactive chat (the default; retained for compatibility)")
	printOnly := flag.Bool("p", false, "run a task headlessly, then exit")
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
		if *resume || *id != "" || *stay || *printOnly || *list {
			exit(fmt.Errorf("kh --help with a request cannot be combined with -r, -s, -i, -p, or --sessions"))
		}
		exit(configErr)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		var usage strings.Builder
		previousOutput := flag.CommandLine.Output()
		flag.CommandLine.SetOutput(&usage)
		flag.PrintDefaults()
		flag.CommandLine.SetOutput(previousOutput)
		exit(help.Run(ctx, cfg, strings.Join(flag.Args(), " "), usage.String(), terminal.NewRenderer(os.Stdout, os.Stderr)))
		return
	}
	exit(configErr)
	cfg.Sandbox = cfg.Sandbox && !*noSandbox
	applyAuto(nil, cfg.Auto)
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
		exit(backend.Login(ctx, args[1]))
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
			exit(memoryCommand(args[1:], cfg))
			return
		}
	}
	if *printOnly && *stay {
		exit(fmt.Errorf("-p cannot be combined with -i"))
	}
	if *printOnly && strings.TrimSpace(strings.Join(args, " ")) == "" {
		exit(fmt.Errorf("usage: kh -p \"task\""))
	}
	interactive := !*printOnly
	// Build before handing a terminal to tmux so compiler failures remain
	// visible. The newly created main pane consumes a one-time skip marker;
	// other spawned/forked sessions still perform their own startup build.
	rebuildCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	rebuilt, err := rebuildBeforeSession(rebuildCtx)
	stop()
	exit(err)
	if interactive && os.Getenv("TMUX") == "" && chatTerminal() {
		exit(attachChat(os.Args[1:], rebuilt))
		return
	}

	if os.Getenv("TMUX") != "" && interactive {
		installImagePasteBinding()
		// "on" forwards modified keys only to applications requesting them,
		// unlike "always". Leave an existing "always" setting untouched.
		if mode, err := tmux("show-options", "-s", "-v", "extended-keys"); err == nil && mode == "off" {
			_, _ = tmux("set-option", "-s", "extended-keys", "on")
		}
		defer clearImagePastePane()
	}

	db, err := memory.Open(memory.Path())
	exit(err)
	defer db.Close()
	cwd, err := os.Getwd()
	exit(err)
	repoScope, owner := memory.RepoScope(cwd), memory.Owner(cwd)
	memoryActive := startMemory(ctx, db, cfg)
	var learner *learn.Learner
	// Delegated task prompts are not user preferences. Workers read shared
	// memory, but only user-facing chats learn durable knowledge.
	if cfg.MemoryModel != "off" && os.Getenv("KH_PARENT") == "" {
		learner = learn.New(db, repoScope, owner, hookModel(cfg), cfg.SummaryMaxTokens*4)
		// Give queued hooks time to finish so a one-shot task's lesson is kept.
		defer func() {
			for _, n := range learner.Close(30 * time.Second) {
				fmt.Fprintln(os.Stderr, terminal.Color(os.Stderr, terminal.Muted, "(memory) "+n))
			}
		}()
	}
	console := terminal.NewConsole(os.Stdin, os.Stdout, os.Stderr)
	defer console.Close()
	ui := terminal.NewRenderer(os.Stdout, os.Stderr)
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
	if cfg.Sandbox && runtime.GOOS != "darwin" {
		fmt.Fprintln(os.Stderr, "warning: kh's bash sandbox is macOS-only; shell commands are not OS-sandboxed on this platform. Approval checks still apply.")
	}
	writeAccess, err := tools.NewWriteAccess(cfg, console)
	exit(err)
	defer writeAccess.Close()
	ts := []tools.Tool{tools.BashWithAccess(cfg, console, writeAccess), tools.EditWithAccess(ui.Action, writeAccess), tools.Memory(db, repoScope, owner), tools.ViewImage(ui.Action), agentTool(cfg, ui.Action, writeAccess)}
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
	p, err := backend.New(cfg, repomap.Build(".", cfg.MapCap), ts, ui)
	exit(err)
	if *id != "" {
		exit(p.Load(saved.State))
		if interactive {
			fmt.Printf("--- session %s ---\n", *id)
			p.Replay(terminal.NewRenderer(os.Stdout, os.Stdout))
			fmt.Println("---")
		}
	} else {
		*id = session.New()
	}

	exportModel(p, cfg.Provider)
	c := chat{model: p, backend: cfg.Provider, id: *id, repo: repoScope,
		memory: db, memoryActive: memoryActive, learn: learner, input: console, ui: ui, tools: ts, seen: fromFile, writeAccess: writeAccess,
		budget: memory.Budget{SummaryChars: cfg.SummaryMaxTokens * 4, TopInstructions: cfg.TopInstructions, TopGotchas: cfg.TopGotchas,
			TopKnowledge: cfg.TopKnowledge, TotalChars: cfg.MemoryMaxTokens * 4}}
	exit(c.snapshotFork())
	console.SetCommandHandler(func(line string) bool {
		if line != "/fork" && line != "/new" {
			return false
		}
		if out, err := openChatPane(ctx, cfg, &c, line == "/fork"); err != nil {
			fmt.Fprintln(os.Stderr, strings.TrimPrefix(line, "/")+":", err)
		} else {
			// Do not flush or interrupt the original session's streaming reply.
			fmt.Fprintln(os.Stderr, terminal.Color(os.Stderr, terminal.Muted, "("+out+")"))
		}
		return true
	})
	console.Start()
	turn := func(msg string) error { return c.turn(ctx, msg) }

	setAgentState("waiting")
	task := strings.Join(args, " ")
	if task != "" && !interactive {
		err := turn(task)
		// A spawned agent's pane closes when kh exits. On failure stay in chat
		// instead, so the error stays visible and the agent can be steered.
		if err == nil || os.Getenv("KH_PARENT") == "" {
			exit(err)
			fmt.Fprintf(os.Stderr, "(session %s: kh -r to continue)\n", *id)
			return
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		task = ""
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
			c.command(ctx, line)
			if err := c.snapshotFork(); err != nil {
				fmt.Fprintln(os.Stderr, "fork checkpoint:", err)
			}
		} else if line != "" {
			if err := turn(line); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
		}
	}
}

// command handles native slash commands. Approval-policy changes apply to future
// requests immediately; model switches apply from the next message.
func (c *chat) command(ctx context.Context, line string) {
	p, access := c.model, c.writeAccess
	cmd, arg, _ := strings.Cut(line, " ")
	var model, effort string
	switch cmd {
	case "/memory":
		if err := c.memoryCommand(ctx, strings.Fields(arg)); err != nil {
			fmt.Println("error:", err)
		}
		return
	case "/auto":
		arg = strings.TrimSpace(arg)
		if arg != "" {
			if arg != "on" && arg != "off" {
				fmt.Println("usage: /auto [on|off]")
				return
			}
			enabled := arg == "on"
			if err := config.Set("auto", enabled); err != nil {
				fmt.Println("error:", err)
				return
			}
			applyAuto(access, enabled)
			c.seen.Auto = enabled
		}
		if access.Auto() {
			fmt.Println("(auto on: commands and requested outside-folder writes are automatically approved; sandbox and path checks remain enabled)")
		} else {
			fmt.Println("(auto off: commands and new folders require approval; existing session grants remain until restart)")
		}
		return
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
		fmt.Println("/compact   summarize context and save the session\n/fork   open the last completed conversation in a pane on the right, without steering\n/new   open a fresh session in a pane on the right, without steering\n/model [id]   show or switch the model, for all sessions\n/effort [low|medium|high]   show or switch thinking effort, for all sessions\n/image [path]   attach a clipboard image (macOS) or a local image for your next message\n/auto [on|off]   persist automatic command and folder approval; existing grants remain until restart\n/memory hybrid on|off   enable local hybrid retrieval or keyword-only memory")
		return
	}
	fmt.Println(terminal.Color(os.Stdout, terminal.Muted, fmt.Sprintf("(model %s, effort %s)", model, effort)))
}

// Keep bash-invoked kh subcommands on the session's active permission policy.
func applyAuto(access *tools.WriteAccess, enabled bool) {
	if access != nil {
		access.SetAuto(enabled)
	}
	value := "0"
	if enabled {
		value = "1"
	}
	os.Setenv("KH_AUTO", value)
}

func exit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
