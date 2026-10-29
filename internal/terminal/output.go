package terminal

import (
	"io"
	"os"
)

// Stdout and Stderr preserve the active editor while presenting chat output.
// Keep readline's own Config writers raw: feeding these back into readline
// would recurse while it redraws the prompt.
func (c *Console) Stdout() io.Writer { return consoleWriter{console: c} }
func (c *Console) Stderr() io.Writer { return consoleWriter{console: c, stderr: true} }

type consoleWriter struct {
	console *Console
	stderr  bool
}

func (w consoleWriter) target() io.Writer {
	if w.stderr {
		return w.console.err
	}
	return w.console.out
}

func (w consoleWriter) Write(p []byte) (int, error) {
	c := w.console
	c.mu.Lock()
	defer c.mu.Unlock()
	out := w.target()
	if c.rl != nil {
		if w.stderr {
			out = c.rl.Stderr()
		} else {
			out = c.rl.Stdout()
		}
	}
	return out.Write(p)
}

// UnderlyingFile exposes terminal capabilities without bypassing coordinated
// writes. Non-file destinations remain non-terminal.
func (w consoleWriter) UnderlyingFile() *os.File { return terminalFile(w.target()) }

func terminalFile(w io.Writer) *os.File {
	switch w := w.(type) {
	case *os.File:
		return w
	case interface{ UnderlyingFile() *os.File }:
		return w.UnderlyingFile()
	default:
		return nil
	}
}
