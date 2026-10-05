package terminal

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"
	"github.com/chzyer/readline"
	"github.com/muesli/termenv"
)

// Explicit styles avoid terminal background queries, which could consume user
// input while the console is reading. Styles do not set a background color.
const replyStyle = `{
 "document": {"block_suffix":"\n"},
 "heading": {"bold":true,"color":"39","block_suffix":"\n"},
 "strong": {"bold":true}, "emph": {"italic":true},
 "strikethrough": {"crossed_out":true},
 "block_quote": {"indent":1,"indent_token":"│ "},
 "list": {"level_indent":2}, "item": {"block_prefix":"• "},
 "enumeration": {"block_prefix":". "},
 "task": {"ticked":"[✓] ","unticked":"[ ] "},
 "hr": {"format":"────────\n","color":"240"},
 "link": {"underline":true,"color":"36"}, "link_text": {"bold":true},
 "image_text": {"format":"Image: {{.text}} →"},
 "code": {"color":"36"},
 "code_block": {"margin":2,"chroma": {
   "text":{"color":"#d0d0d0"},"comment":{"color":"#808080"},
   "keyword":{"color":"#00afff"},"literal_string":{"color":"#87d787"}
 }},
 "table": {"center_separator":"┼","column_separator":"│","row_separator":"─"}
}`

const maxReplyBlock = 1 << 20

type markdownReply struct {
	mu       sync.Mutex // tool actions may flush concurrently after a step
	out      *os.File
	block    strings.Builder
	plain    bool // oversized replies fall back to raw output until message end
	width    int
	noColor  bool
	renderer *glamour.TermRenderer
}

func newMarkdownReply(out io.Writer) *markdownReply {
	f, ok := out.(*os.File)
	if !ok || f == nil || os.Getenv("TERM") == "dumb" || !readline.IsTerminal(int(f.Fd())) {
		return nil
	}
	return &markdownReply{out: f}
}

func cleanReply(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(text))
}

func (r *markdownReply) write(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.plain {
		fmt.Fprint(r.out, cleanReply(text))
		return
	}
	if len(text)+r.block.Len() > maxReplyBlock {
		fmt.Fprint(r.out, cleanReply(r.block.String()+text))
		r.block.Reset()
		r.plain = true
		return
	}
	r.block.WriteString(text)
}

// Render complete semantic replies instead of guessing Markdown boundaries:
// loose lists, reference links, and code may span arbitrary blank lines/tokens.
// No cursor repainting means drafts and steering retain their existing behavior.
func (r *markdownReply) flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.plain {
		r.render()
	}
	r.plain = false
}

func (r *markdownReply) render() {
	source := r.block.String()
	r.block.Reset()
	if strings.TrimSpace(source) == "" {
		return
	}
	width := max(1, SessionWidth(r.out))
	noColor := os.Getenv("NO_COLOR") != ""
	if r.renderer == nil || r.width != width || r.noColor != noColor {
		profile := termenv.ANSI256
		if noColor {
			profile = termenv.Ascii
		}
		r.renderer, _ = glamour.NewTermRenderer(glamour.WithStylesFromJSONBytes([]byte(replyStyle)),
			glamour.WithWordWrap(width), glamour.WithColorProfile(profile))
		r.width, r.noColor = width, noColor
	}
	if r.renderer != nil {
		if rendered, err := r.renderer.Render(ReadableMathMarkdown(cleanReply(source))); err == nil {
			if noColor {
				rendered = cleanReply(rendered)
			}
			fmt.Fprint(r.out, safeRenderedReply(rendered))
			return
		}
	}
	fmt.Fprint(r.out, cleanReply(source))
}

// Markdown entity decoding can introduce controls after source sanitization.
// Permit only SGR styling, never cursor movement, title changes, or OSC clipboard
// commands, including those introduced by entity decoding inside the renderer.
func safeRenderedReply(text string) string {
	var out strings.Builder
	var state byte
	for len(text) > 0 {
		seq, _, n, next := ansi.DecodeSequence(text, state, nil)
		if n == 0 {
			break
		}
		state = next
		text = text[n:]
		if strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m") && strings.Trim(seq[2:len(seq)-1], "0123456789;:") == "" {
			out.WriteString(seq)
		} else {
			out.WriteString(cleanReply(seq))
		}
	}
	return out.String()
}
