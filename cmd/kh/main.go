// kh is a lightweight, fast coding harness.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"kh/internal/agent"
	"kh/internal/auth"
	"kh/internal/config"
	"kh/internal/provider"
	"kh/internal/repomap"
	"kh/internal/tools"
)

func main() {
	cfg, err := config.Load()
	exit(err)
	// Flags override the config file, which overrides the defaults.
	flag.StringVar(&cfg.Model, "model", cfg.Model, "model id")
	flag.StringVar(&cfg.Effort, "effort", cfg.Effort, "reasoning effort: low, medium, high")
	flag.BoolVar(&cfg.Yes, "y", cfg.Yes, "run every command without asking")
	flag.Parse()
	args := flag.Args()
	ctx := context.Background()

	if strings.Join(args, " ") == "login codex" {
		exit(auth.Login(ctx))
		fmt.Println("Logged in.")
		return
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, `usage: kh [-model id] [-effort e] [-y] "your task"  |  kh login codex`)
		os.Exit(1)
	}

	// Built once per run, so it stays a stable, cacheable part of the prompt.
	if m := repomap.Build(".", cfg.MapCap); m != "" {
		cfg.System += "\n\nRepo map (path: top-level symbols). Use it to go straight to the right file:\n" + m
	}
	ts := []tools.Tool{tools.Bash(cfg), tools.Edit}
	p := provider.NewCodex(cfg, ts)
	exit(agent.Run(ctx, p, ts, strings.Join(args, " ")))
}

func exit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
