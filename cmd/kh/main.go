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
	if strings.Join(args, " ") == "sessions" {
		listSessions(cfg)
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
		if len(args) == 0 { // chat: show where we left off
			fmt.Printf("--- session %s ---\n%s", *id, grey)
			p.Replay(os.Stdout)
			fmt.Println(reset + "---")
		}
	} else {
		*id = session.New()
	}

	// One user message. Ctrl-C cancels just this turn; saving after every
	// turn means a closed terminal loses at most the current one.
	turn := func(msg string) error {
		tctx, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		start := time.Now()
		err := agent.Run(tctx, p, ts, msg)
		if tctx.Err() == context.Canceled { // Ctrl-C: expected, not an error
			fmt.Fprintln(os.Stderr, "\n(stopped)")
			err = nil
		}
		if s := p.Stats(); s.In > 0 {
			fmt.Fprintf(os.Stderr, "%s(ttft %s, total %s, %s in, %s cached %d%%, %s out)%s\n", grey,
				secs(s.TTFT), secs(time.Since(start)), k(s.In), k(s.Cached), s.Cached*100/s.In, k(s.Out), reset)
		}
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

func secs(d time.Duration) string {
	if d == 0 {
		return "-" // no text this turn
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// listSessions prints the last 20 sessions with their first message.
func listSessions(cfg config.Config) {
	ids := session.List()
	ids = ids[max(0, len(ids)-20):]
	for _, id := range ids {
		b, _ := session.Load(id)
		p := provider.NewCodex(cfg, nil)
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
