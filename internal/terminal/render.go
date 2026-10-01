package terminal

import (
	"fmt"
	"io"
)

// Renderer owns chat presentation. Model adapters emit text, not ANSI codes.
type Renderer struct {
	Out io.Writer
	Err io.Writer
}

func (r Renderer) Reply(text string) {
	fmt.Fprint(r.Out, Color(r.Out, Response, text))
}

func (r Renderer) Query(text string) {
	fmt.Fprintln(r.Out, Color(r.Out, Query, "> "+text))
}

func (r Renderer) Action(text string) { fmt.Fprintln(r.Err, text) }
func (r Renderer) Notice(text string) {
	fmt.Fprintln(r.Err, Color(r.Err, Muted, text))
}
