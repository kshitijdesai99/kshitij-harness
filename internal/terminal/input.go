package terminal

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
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
	stdin        *readline.CancelableStdin
	pending      []string
	pastes       *pasteStore
	approval     chan struct{}
	activity     *Activity
}

func NewConsole(in io.Reader, out, err io.Writer) *Console {
	lines := make(chan string, 16)
	c := &Console{Lines: lines, lines: lines, in: in, out: out, err: err,
		done: make(chan struct{}), approval: make(chan struct{}, 1), pastes: newPasteStore()}
	c.approval <- struct{}{}
	return c
}

// SetActivity attaches presentation only; input and approval remain independent.
func (c *Console) SetActivity(a *Activity) { c.mu.Lock(); c.activity = a; c.mu.Unlock() }
func (c *Console) activityPhase(phase string) {
	c.mu.Lock()
	a := c.activity
	c.mu.Unlock()
	if a != nil {
		a.Set(phase)
	}
}

// Start must run after the tmux launcher so it cannot steal client keystrokes.
func (c *Console) Start() { c.start.Do(func() { go c.read() }) }

func (c *Console) Close() {
	c.close.Do(func() {
		close(c.done)
		c.mu.Lock()
		rl, stdin := c.rl, c.stdin
		c.mu.Unlock()
		// readline's Close does not close the reader it was given, so it would
		// wait for one more keypress; closing it first lets kh exit at once.
		if stdin != nil {
			_ = stdin.Close()
		}
		if rl != nil {
			_ = rl.Close()
		}
	})
}

func (c *Console) send(line string) bool {
	line, err := c.pastes.expand(strings.TrimSpace(line))
	if err != nil {
		fmt.Fprintln(c.err, "paste:", err)
		return true // stay at the prompt; never submit a partial/rejected paste
	}
	select {
	case c.lines <- line:
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
	stdin := readline.NewCancelableStdin(newTerminalReader(in, c.pastes))
	var continuation atomic.Bool
	prompt := Color(c.out, Query, "> ")
	rl, err := readline.NewEx(&readline.Config{
		Stdin: stdin, Stdout: c.out, Stderr: c.err,
		Prompt: prompt, Painter: queryPainter{c.out},
		FuncFilterInputRune: func(key rune) (rune, bool) {
			if key == continuationKey {
				continuation.Store(true)
				// Ignoring a rune makes readline queue an extra read. A no-op
				// Bell instead preserves its CR handoff to the next prompt.
				return readline.CharBell, true
			}
			return key, true
		},
		HistoryLimit: 500, DisableAutoSaveHistory: true,
		InterruptPrompt: "^C", EOFPrompt: "\n",
	})
	if err != nil {
		fmt.Fprintln(c.err, "line editor:", err)
		c.readPlain()
		return
	}
	defer func() {
		_ = stdin.Close()
		_ = rl.Close()
	}()
	c.mu.Lock()
	c.rl, c.stdin = rl, stdin
	c.mu.Unlock()
	select {
	case <-c.done:
		return
	default:
	}
	// Frame pastes and distinguish modified Enter (Kitty / xterm protocols).
	// Restore keyboard and paste modes before leaving the editor.
	fmt.Fprint(c.out, "\x1b[?2004h\x1b[>1u\x1b[>4;2m")
	defer fmt.Fprint(c.out, "\x1b[>4;0m\x1b[<u\x1b[?2004l")
	// Raw-mode Ctrl-C is a key. Forward it to the active turn's context;
	// an idle chat must stay alive even without an active listener.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	var draft []string
	for {
		line, err := rl.Readline()
		if err == readline.ErrInterrupt {
			draft = nil
			continuation.Store(false)
			rl.SetPrompt(prompt)
			_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
			continue
		}
		if err != nil {
			return
		}
		if continuation.Swap(false) {
			draft = append(draft, line)
			rl.SetPrompt(Color(c.out, Query, "... "))
			continue
		}
		if len(draft) > 0 {
			draft = append(draft, line)
			// Reuse paste placeholders so recalling multiline history does not
			// put literal newlines into the single-line editor.
			line, err = c.pastes.expand(strings.TrimSpace(strings.Join(draft, "\n")))
			if err == nil {
				line, err = c.pastes.put(line, false)
			}
			draft = nil
			rl.SetPrompt(prompt)
			if err != nil {
				fmt.Fprintln(c.err, "input:", err)
				continue
			}
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
	r := bufio.NewReader(newPasteReader(c.in, c.pastes))
	for {
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			fmt.Fprintln(c.err, "input:", err)
			return // an incomplete paste must not flush an unfinished draft
		}
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
