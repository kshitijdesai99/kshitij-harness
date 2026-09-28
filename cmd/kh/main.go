// kh is a lightweight, fast coding harness.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"kh/internal/agent"
	"kh/internal/auth"
	"kh/internal/config"
	"kh/internal/provider"
	"kh/internal/repomap"
	"kh/internal/session"
	"kh/internal/tools"
)

func main() {
	cfg, err := config.Load()
	exit(err)
	// Flags override the config file, which overrides the defaults.
	flag.StringVar(&cfg.Model, "model", cfg.Model, "model id")
	flag.StringVar(&cfg.Effort, "effort", cfg.Effort, "reasoning effort: low, medium, high")
	flag.BoolVar(&cfg.Yes, "y", cfg.Yes, "run every command without asking")
	noSandbox := flag.Bool("nosandbox", false, "let bash write outside the project")
	resume := flag.Bool("r", false, "resume the latest session")
	id := flag.String("s", "", "resume a session by id (files in ~/.kh/sessions)")
	flag.Parse()
	cfg.Sandbox = cfg.Sandbox && !*noSandbox
	args := flag.Args()
	ctx := context.Background()

	if strings.Join(args, " ") == "login codex" {
		exit(auth.Login(ctx))
		fmt.Println("Logged in.")
		return
	}
	// Built once per run, so it stays a stable, cacheable part of the prompt.
	if m := repomap.Build(".", cfg.MapCap); m != "" {
		cfg.System += "\n\nRepo map (path: top-level symbols). Use it to go straight to the right file:\n" + m
	}
	ts := []tools.Tool{tools.Bash(cfg), tools.Edit}
	p := provider.NewCodex(cfg, ts)

	if *resume {
		*id, err = session.Latest()
		exit(err)
	}
	if *id != "" {
		b, err := session.Load(*id)
		exit(err)
		exit(p.Load(b))
	} else {
		*id = session.New()
	}

	// One user message. Ctrl-C cancels just this turn; saving after every
	// turn means a closed terminal loses at most the current one.
	turn := func(msg string) error {
		tctx, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		err := agent.Run(tctx, p, ts, msg)
		if b, e := p.Save(); e == nil {
			session.Save(*id, b)
		}
		return err
	}

	if len(args) > 0 {
		exit(turn(strings.Join(args, " ")))
		fmt.Fprintf(os.Stderr, "(session %s: kh -r to continue)\n", *id)
		return
	}
	fmt.Fprintf(os.Stderr, "kh chat, session %s. Ctrl-C stops a task, Ctrl-D quits.\n", *id)
	for {
		fmt.Print("> ")
		line, err := tools.In.ReadString('\n')
		if err != nil { // Ctrl-D
			fmt.Println()
			return
		}
		if line = strings.TrimSpace(line); line != "" {
			if err := turn(line); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
		}
	}
}

func exit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
