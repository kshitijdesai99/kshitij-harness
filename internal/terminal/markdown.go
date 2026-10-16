package terminal

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
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
 "link": {"underline":true,"color":"36","format":"{{khLink .text \"LINK_ID\"}}"}, "link_text": {"bold":true},
 "image_text": {"format":"Image: {{.text}} →"},
 "code": {"color":"36"},
 "code_block": {"margin":2,"chroma": {
   "text":{"color":"#d0d0d0"},"comment":{"color":"#808080"},
   "keyword":{"color":"#00afff"},"literal_string":{"color":"#87d787"}
 }},
 "table": {"center_separator":"┼","column_separator":"│","row_separator":"─"}
}`

// Register once, before any renderer runs. Glamour's wrapper understands SGR,
// but not OSC 8: carry the original URL as decimal bytes in a private SGR marker
// and generate the hyperlink only after wrapping and sanitization. Fixed-width
// byte encoding keeps this to three parameters (the table parser has a small
// parameter limit). Never derive destinations from decorated or truncated text.
func init() {
	glamouransi.TemplateFuncMap["khLink"] = func(target, id string) string {
		var out strings.Builder
		out.WriteString("\x1b[777;" + id + ";")
		for i := 0; i < len(target); i++ {
			fmt.Fprintf(&out, "%03d", target[i])
		}
		out.WriteString("m" + target + "\x1b[778m")
		return out.String()
	}
}

const maxReplyBlock = 1 << 20

type markdownReply struct {
	mu         sync.Mutex // tool actions may flush concurrently after a step
	out        *os.File
	block      strings.Builder
	plain      bool // oversized replies fall back to raw output until message end
	width      int
	noColor    bool
	renderer   *glamour.TermRenderer
	commentary bool
	lineStart  bool
	linkID     string // unpredictable marker prevents entity-decoded link forgery
}

func newMarkdownReply(out io.Writer) *markdownReply {
	f, ok := out.(*os.File)
	if !ok || f == nil || os.Getenv("TERM") == "dumb" || !readline.IsTerminal(int(f.Fd())) {
		return nil
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil
	}
	return &markdownReply{out: f, lineStart: true, linkID: strconv.FormatUint(binary.BigEndian.Uint64(nonce[:]), 10)}
}

func cleanReply(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(text))
}

func (r *markdownReply) write(text string, commentary bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.commentary != commentary {
		if !r.plain {
			r.render()
		}
		r.plain = false
		r.commentary = commentary
		r.renderer = nil
	}
	if r.plain {
		r.present(cleanReply(text))
		return
	}
	if len(text)+r.block.Len() > maxReplyBlock {
		r.present(cleanReply(r.block.String() + text))
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
	if r.commentary {
		width = max(1, width-4)
	}
	noColor := os.Getenv("NO_COLOR") != ""
	if r.renderer == nil || r.width != width || r.noColor != noColor {
		profile := termenv.ANSI256
		if noColor || r.commentary {
			profile = termenv.Ascii
		}
		style := strings.ReplaceAll(replyStyle, "LINK_ID", r.linkID)
		r.renderer, _ = glamour.NewTermRenderer(glamour.WithStylesFromJSONBytes([]byte(style)),
			glamour.WithWordWrap(width), glamour.WithColorProfile(profile), glamour.WithInlineTableLinks(true))
		r.width, r.noColor = width, noColor
	}
	if r.renderer != nil {
		if rendered, err := r.renderer.Render(ReadableMathMarkdown(cleanReply(source))); err == nil {
			r.present(safeRenderedReply(rendered, noColor || r.commentary, r.linkID))
			return
		}
	}
	r.present(cleanReply(source))
}

// Four spaces provide a stable tab-width indent without terminal tab stops.
func (r *markdownReply) present(text string) {
	if r.commentary {
		var indented strings.Builder
		for _, part := range strings.SplitAfter(text, "\n") {
			if part == "" {
				continue
			}
			if r.lineStart && part != "\n" {
				indented.WriteString("    ")
			}
			indented.WriteString(part)
			r.lineStart = strings.HasSuffix(part, "\n")
		}
		text = Color(r.out, Muted, indented.String())
	}
	if !r.commentary && text != "" {
		r.lineStart = strings.HasSuffix(text, "\n")
	}
	fmt.Fprint(r.out, text)
}

// Glamour's wrapping does not understand OSC 8. The link format uses private
// SGR markers (777;<nonce>;<URL bytes>/778), which survive wrapping, then replaces
// them with validated hyperlinks. Markers and untrusted OSC never reach the terminal.
// Entity decoding can introduce controls after source sanitization, so only SGR
// styling is accepted from the rendered text; hyperlinks are generated here.
func safeRenderedReply(text string, noStyle bool, linkID string) string {
	var out, link strings.Builder
	var state byte
	prefix := "\x1b[777;" + linkID + ";"
	inLink, valid := false, true
	target := ""
	finishLink := func(complete bool) {
		body := link.String()
		if complete && valid && safeLinkTarget(target) {
			out.WriteString("\x1b]8;;" + target + "\x1b\\")
			out.WriteString(body)
			out.WriteString("\x1b]8;;\x1b\\")
		} else {
			out.WriteString(body)
		}
		link.Reset()
		inLink = false
	}
	for len(text) > 0 {
		seq, _, n, next := ansi.DecodeSequence(text, state, nil)
		if n == 0 {
			break
		}
		state = next
		text = text[n:]
		if strings.HasPrefix(seq, prefix) && strings.HasSuffix(seq, "m") {
			var decoded strings.Builder
			encoded := seq[len(prefix) : len(seq)-1]
			if len(encoded)%3 != 0 {
				encoded = ""
			}
			for i := 0; i < len(encoded); i += 3 {
				b, err := strconv.ParseUint(encoded[i:i+3], 10, 8)
				if err != nil {
					decoded.Reset()
					break
				}
				decoded.WriteByte(byte(b))
			}
			// Wrapping repeats active markers; table rows can interleave
			// different cells. Never let one destination swallow another.
			if inLink && target != decoded.String() {
				finishLink(true)
			}
			target = decoded.String()
			if !inLink {
				inLink, valid = true, true
			}
			continue
		}
		if strings.HasPrefix(seq, "\x1b[777") {
			// Drop forged or malformed private markers too.
			continue
		}
		if seq == "\x1b[778m" {
			if inLink {
				finishLink(true)
			}
			continue
		}
		if strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m") && strings.Trim(seq[2:len(seq)-1], "0123456789;:") == "" {
			// Close before padding, borders or another cell. Repeated markers
			// reopen wrapped links where the layout preserves them. Glamour
			// drops markers on table continuations: only their first fragment
			// is linked, rather than risking a link across adjacent cells.
			if inLink && (seq == "\x1b[0m" || seq == "\x1b[m") {
				finishLink(true)
			}
			if noStyle {
				continue
			}
		} else {
			clean := cleanReply(seq)
			if clean != seq {
				valid = false
			}
			seq = clean
		}
		if inLink {
			link.WriteString(seq)
		} else {
			out.WriteString(seq)
		}
	}
	if inLink {
		finishLink(false)
	}
	return out.String()
}

func safeLinkTarget(target string) bool {
	if strings.ContainsAny(target, "\\") || strings.ContainsFunc(target, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r) || unicode.Is(unicode.Cf, r)
	}) {
		return false
	}
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Hostname() != ""
	case "mailto":
		return u.Opaque != ""
	case "vscode":
		return u.Host == "file" && strings.HasPrefix(u.Path, "/")
	case "file":
		return (u.Host == "" || u.Host == "localhost") && strings.HasPrefix(u.Path, "/")
	}
	return false
}
