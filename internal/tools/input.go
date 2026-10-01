package tools

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/chzyer/readline"
)

func readInput() {
	defer close(Lines)
	if !readline.IsTerminal(int(os.Stdin.Fd())) || !readline.IsTerminal(int(os.Stdout.Fd())) {
		readPlainInput()
		return
	}
	rl, err := readline.NewEx(&readline.Config{
		Prompt: "> ", HistoryLimit: 500, DisableAutoSaveHistory: true,
		InterruptPrompt: "^C", EOFPrompt: "\n",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "line editor:", err)
		readPlainInput()
		return
	}
	defer rl.Close()

	// Raw-mode Ctrl-C is a key, not a terminal signal. Forward it so the
	// active turn's signal.NotifyContext still cancels. Keep an idle chat
	// alive even when there is no active turn listening for interrupts.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	for {
		line, err := rl.Readline()
		if err == readline.ErrInterrupt {
			_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
			continue
		}
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		// Approval answers should not replace the last query on Up.
		switch strings.ToLower(line) {
		case "", "y", "yes", "n", "no":
		default:
			_ = rl.SaveHistory(line)
		}
		Lines <- line
	}
}

func readPlainInput() {
	r := bufio.NewReader(os.Stdin)
	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			Lines <- strings.TrimSpace(line)
		}
		if err != nil {
			return
		}
	}
}
