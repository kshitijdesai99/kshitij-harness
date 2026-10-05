package terminal

import (
	"fmt"
	"io"
	"strings"
)

// Renderer owns chat presentation. Model adapters emit text, not ANSI codes.
type Renderer struct {
	Out      io.Writer
	Err      io.Writer
	Activity *Activity
	reply    *markdownReply // shared when Renderer values are copied into adapters
}

// NewRenderer enables Markdown presentation only on an interactive, capable
// terminal. Literal Renderer values retain the original plain streaming API.
func NewRenderer(out, err io.Writer) Renderer {
	r := Renderer{Out: out, Err: err}
	r.reply = newMarkdownReply(out)
	return r
}

// FlushReply finishes an assistant message, including partial/cancelled replies.
// Adapters call this at semantic boundaries, not at arbitrary token boundaries.
func (r Renderer) FlushReply() {
	if r.reply != nil {
		r.reply.flush()
	}
}

func (r Renderer) Reply(text string) {
	if r.Activity != nil && strings.TrimSpace(text) != "" {
		r.Activity.Set("responding")
	}
	if r.reply != nil {
		r.reply.write(text)
		return
	}
	fmt.Fprint(r.Out, Color(r.Out, Response, text))
}

func (r Renderer) Query(text string) {
	r.FlushReply()
	fmt.Fprintln(r.Out, Color(r.Out, Query, "> "+text))
}

func (r Renderer) Action(text string) {
	r.FlushReply()
	fmt.Fprintln(r.Err, text)
}
func (r Renderer) Notice(text string) {
	r.FlushReply()
	fmt.Fprintln(r.Err, Color(r.Err, Muted, text))
}
