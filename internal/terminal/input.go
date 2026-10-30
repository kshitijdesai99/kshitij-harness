package terminal

import (
	"bufio"
	"encoding/base64"
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
	command      func(string) bool
	deliveryMu   sync.Mutex
	inputClosed  bool
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

// SetCommandHandler installs a local input handler. Handled lines never enter
// the message/steering or approval queues. It may be updated after Start.
func (c *Console) SetCommandHandler(handler func(string) bool) {
	c.mu.Lock()
	c.command = handler
	c.mu.Unlock()
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
		fmt.Fprintln(c.Stderr(), "paste:", err)
		return true // stay at the prompt; never submit a partial/rejected paste
	}
	c.mu.Lock()
	command := c.command
	c.mu.Unlock()
	if command != nil && command(line) {
		return true
	}
	return c.deliver(line)
}

// deliver is shared by submitted human input and out-of-band peer messages.
// Peer messages deliberately bypass commands, paste expansion, and history.
func (c *Console) deliver(line string) bool {
	// readline's cancelable reader can finish a frame after the read loop
	// exits. Serialize delivery with channel closure, including that late read.
	c.deliveryMu.Lock()
	defer c.deliveryMu.Unlock()
	if c.inputClosed {
		return false
	}
	select {
	case c.lines <- line:
		return true
	case <-c.done:
		return false
	}
}

func (c *Console) read() {
	defer func() {
		c.deliveryMu.Lock()
		c.inputClosed = true
		close(c.lines)
		c.deliveryMu.Unlock()
	}()
	in, inOK := c.in.(*os.File)
	out, outOK := c.out.(*os.File)
	if !inOK || !outOK || !readline.IsTerminal(int(in.Fd())) || !readline.IsTerminal(int(out.Fd())) {
		c.readPlain()
		return
	}
	stdin := readline.NewCancelableStdin(newTerminalReader(c.peerInput(in), c.pastes))
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
		fmt.Fprintln(c.Stderr(), "line editor:", err)
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
				fmt.Fprintln(c.Stderr(), "input:", err)
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
	r := bufio.NewReader(newPasteReader(c.peerInput(c.in), c.pastes))
	for {
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			fmt.Fprintln(c.Stderr(), "input:", err)
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

// PeerMessage frames a message for tmux delivery without simulating user keys.
// Decode it below paste handling so arrival during a paste cannot become payload.
func PeerMessage(message string) string {
	return peerPrefix + base64.RawStdEncoding.EncodeToString([]byte(message)) + "\a"
}

const peerPrefix = "\x1b]kh-peer;"

func (c *Console) peerInput(in io.Reader) io.Reader {
	return &peerReader{in: bufio.NewReader(in), deliver: c.deliver}
}

type peerReader struct {
	in      *bufio.Reader
	deliver func(string) bool
	pending []byte
}

func (r *peerReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		b, err := r.in.ReadByte()
		if err != nil {
			return 0, err
		}
		if b != peerPrefix[0] {
			// Pass through an available run of ordinary bytes without adding a
			// per-keystroke allocation or waiting for more terminal input.
			p[0] = b
			buffered, _ := r.in.Peek(min(r.in.Buffered(), len(p)-1))
			for i, next := range buffered {
				if next == peerPrefix[0] {
					buffered = buffered[:i]
					break
				}
			}
			n := copy(p[1:], buffered)
			_, _ = r.in.Discard(n)
			return n + 1, nil
		}
		sequence := []byte{b}
		for len(sequence) < len(peerPrefix) && string(sequence) == peerPrefix[:len(sequence)] {
			b, err = r.in.ReadByte()
			if err != nil {
				break
			}
			sequence = append(sequence, b)
		}
		if string(sequence) != peerPrefix {
			r.pending = sequence
			break
		}
		var encoded strings.Builder
		for {
			b, err = r.in.ReadByte()
			if err != nil {
				return 0, fmt.Errorf("incomplete peer message: %w", err)
			}
			if b == '\a' {
				break
			}
			if encoded.Len() >= base64.RawStdEncoding.EncodedLen(pasteLimit) {
				return 0, fmt.Errorf("peer message exceeds the 4 MiB limit")
			}
			encoded.WriteByte(b)
		}
		message, err := base64.RawStdEncoding.DecodeString(encoded.String())
		if err != nil {
			return 0, fmt.Errorf("invalid peer message: %w", err)
		}
		if !r.deliver(string(message)) {
			return 0, io.EOF
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

type queryPainter struct{ out io.Writer }

func (p queryPainter) Paint(line []rune, _ int) []rune {
	return []rune(colorQuery(p.out, string(line), ""))
}
