package terminal

import (
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/chzyer/readline"
	"github.com/rivo/uniseg"
)

// SessionWidth prefers the actual stdout TTY size. COLUMNS is only a fallback;
// redirected output defaults to 120 cells when it is unset or invalid.
func SessionWidth(out *os.File) int {
	if out != nil && readline.IsTerminal(int(out.Fd())) {
		if width, _, err := readline.GetSize(int(out.Fd())); err == nil && width > 0 {
			return width
		}
	}
	if width, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && width > 0 {
		return width
	}
	return 120
}

// SessionRow returns one control-free line no wider than width display cells.
// Previews use at most 70 cells, including an ASCII ellipsis. The saved time is
// omitted when it would crowd out the exact ID. Only panes narrower than the ID
// itself truncate the ID; truncation never splits a UTF-8 grapheme cluster.
func SessionRow(savedAt time.Time, id, preview string, width int) string {
	if width <= 0 {
		return ""
	}
	id, preview = sessionText(id), sessionText(preview)
	if uniseg.StringWidth(id) > width {
		return sessionTruncate(id, width)
	}
	prefix := id
	stamp := savedAt.Format("2006-01-02 15:04")
	if uniseg.StringWidth(stamp)+2+uniseg.StringWidth(id) <= width {
		prefix = stamp + "  " + id
	}
	remaining := min(70, width-uniseg.StringWidth(prefix)-2)
	if remaining <= 0 || preview == "" {
		return prefix
	}
	return prefix + "  " + sessionTruncate(preview, remaining)
}

func sessionTruncate(text string, width int) string {
	if uniseg.StringWidth(text) <= width {
		return text
	}
	ellipsis := strings.Repeat(".", min(3, width))
	budget := width - len(ellipsis)
	var b strings.Builder
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		if g.Width() > budget {
			break
		}
		b.WriteString(g.Str())
		budget -= g.Width()
	}
	return b.String() + ellipsis
}

// Remove terminal escape sequences rather than merely removing their ESC byte:
// otherwise CSI parameters and OSC titles/links would leak into the preview.
func sessionText(text string) string {
	runes := []rune(strings.ToValidUTF8(text, ""))
	var b strings.Builder
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\x1b' {
			if i+1 == len(runes) {
				break
			}
			i++
			r = runes[i]
			if r != '[' && r != ']' && r != 'P' && r != 'X' && r != '^' && r != '_' {
				for r >= 0x20 && r <= 0x2f && i+1 < len(runes) {
					i++
					r = runes[i]
				}
				continue
			}
		} else if r != '\u009b' && r != '\u009d' && r != '\u0090' && r != '\u0098' && r != '\u009e' && r != '\u009f' {
			if unicode.IsSpace(r) {
				b.WriteByte(' ')
			} else if !unicode.IsControl(r) && r != '\u061c' && r != '\u200e' && r != '\u200f' && !(r >= '\u202a' && r <= '\u202e') && !(r >= '\u2066' && r <= '\u2069') {
				b.WriteRune(r)
			}
			continue
		}
		if r == '[' || r == '\u009b' {
			for i++; i < len(runes); i++ {
				if runes[i] >= 0x40 && runes[i] <= 0x7e {
					break
				}
			}
		} else {
			for i++; i < len(runes); i++ {
				if runes[i] == '\a' || runes[i] == '\u009c' {
					break
				}
				if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '\\' {
					i++
					break
				}
			}
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
