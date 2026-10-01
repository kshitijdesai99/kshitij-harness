// Package terminal keeps chat colors out of redirected output.
package terminal

import (
	"io"
	"os"

	"github.com/chzyer/readline"
)

const (
	Muted    = "\033[90m"
	Query    = "\033[36m"
	Response = "\033[32m"
	Reset    = "\033[0m"
)

func Color(w io.Writer, color, text string) string {
	f, ok := w.(*os.File)
	if !ok || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" || !readline.IsTerminal(int(f.Fd())) {
		return text
	}
	return color + text + Reset
}
