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
	"kh/internal/provider"
	"kh/internal/tools"
)

func main() {
	model := flag.String("model", "gpt-5.5", "model id")
	yes := flag.Bool("y", false, "run every command without asking")
	flag.Parse()
	args := flag.Args()
	ctx := context.Background()

	if strings.Join(args, " ") == "login codex" {
		exit(auth.Login(ctx))
		fmt.Println("Logged in.")
		return
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, `usage: kh [-model id] [-y] "your task"  |  kh login codex`)
		os.Exit(1)
	}

	ts := []tools.Tool{tools.Bash(*yes), tools.Edit}
	p := provider.NewCodex(*model, agent.System, ts)
	exit(agent.Run(ctx, p, ts, strings.Join(args, " ")))
}

func exit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
