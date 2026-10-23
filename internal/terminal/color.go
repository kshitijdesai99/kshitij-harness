// Package terminal keeps chat colors out of redirected output.
package terminal

import (
	"io"
	"os"
	"strings"

	"github.com/chzyer/readline"
)

const (
	Muted       = "\033[90m"
	LightOrange = "\033[38;5;215m"
	Query       = "\033[36m"
	PeerQuery   = "\033[38;5;214m"
	Response    = "\033[32m"
	Reset       = "\033[0m"
)

// colorQuery also handles newline-joined steering/approval messages on replay.
// Peer sends are single-line messages, so each line retains its own style.
func colorQuery(w io.Writer, text, prefix string) string {
	lines := strings.SplitAfter(text, "\n")
	for i, line := range lines {
		color := Query
		if strings.HasPrefix(strings.TrimSpace(line), "[from kh:") {
			color = PeerQuery
		}
		lines[i] = Color(w, color, prefix+line)
		prefix = ""
	}
	return strings.Join(lines, "")
}

func Color(w io.Writer, color, text string) string {
	f, ok := w.(*os.File)
	if !ok || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" || !readline.IsTerminal(int(f.Fd())) {
		return text
	}
	return color + text + Reset
}
