package terminal

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/chzyer/readline"
)

// Console owns input and approval state for one chat, not the whole process.
type Console struct {
	Lines        <-chan string
	lines        chan string
	in           io.Reader
	out, err     io.Writer
	done         chan struct{}
	start, close sync.Once
	mu           sync.Mutex
	rl           *readline.Instance
	pending      []string
	approval     chan struct{}
}

func NewConsole(in io.Reader, out, err io.Writer) *Console {
	lines := make(chan string, 16)
	c := &Console{Lines: lines, lines: lines, in: in, out: out, err: err,
		done: make(chan struct{}), approval: make(chan struct{}, 1)}
	c.approval <- struct{}{}
	return c
}

// Start must run after the tmux launcher so it cannot steal client keystrokes.
func (c *Console) Start() { c.start.Do(func() { go c.read() }) }

func (c *Console) Close() {
	c.close.Do(func() {
		close(c.done)
		c.mu.Lock()
		rl := c.rl
		c.mu.Unlock()
		if rl != nil {
			_ = rl.Close()
		}
	})
}

func (c *Console) send(line string) bool {
	select {
	case c.lines <- strings.TrimSpace(line):
		return true
	case <-c.done:
		return false
	}
}

func (c *Console) read() {
	defer close(c.lines)
	in, inOK := c.in.(*os.File)
	out, outOK := c.out.(*os.File)
	if !inOK || !outOK || !readline.IsTerminal(int(in.Fd())) || !readline.IsTerminal(int(out.Fd())) {
		c.readPlain()
		return
	}
	rl, err := readline.NewEx(&readline.Config{
		Stdin: readline.NewCancelableStdin(in), Stdout: c.out, Stderr: c.err,
		Prompt: Color(c.out, Query, "> "), Painter: queryPainter{c.out},
		HistoryLimit: 500, DisableAutoSaveHistory: true,
		InterruptPrompt: "^C", EOFPrompt: "\n",
	})
	if err != nil {
		fmt.Fprintln(c.err, "line editor:", err)
		c.readPlain()
		return
	}
	defer rl.Close()
	c.mu.Lock()
	c.rl = rl
	c.mu.Unlock()
	select {
	case <-c.done:
		return
	default:
	}
	// Raw-mode Ctrl-C is a key. Forward it to the active turn's context;
	// an idle chat must stay alive even without an active listener.
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
		switch strings.ToLower(line) {
		case "", "y", "yes", "n", "no":
		default:
			_ = rl.SaveHistory(line)
		}
		if !c.send(line) {
			return
		}
	}
}

func (c *Console) readPlain() {
	r := bufio.NewReader(c.in)
	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 && !c.send(line) {
			return
		}
		if err != nil {
			return
		}
	}
}

type queryPainter struct{ out io.Writer }

func (p queryPainter) Paint(line []rune, _ int) []rune {
	return []rune(Color(p.out, Query, string(line)))
}
