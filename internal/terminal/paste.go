package terminal

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const (
	pasteBegin  = "\x1b[200~"
	pasteEnd    = "\x1b[201~"
	pasteLimit  = 4 << 20
	pasteBudget = 16 << 20
	// Internal key followed by CR: readline pauses input at CR until the
	// next Readline call, so a continuation cannot race the following line.
	continuationKey = '\ue000'
)

// Pasted newlines and control keys never reach readline. It edits a compact
// placeholder instead; only submission expands that placeholder into text.
// This keeps a large paste cheap to display and preserves normal arrow/history
// behavior without asking the single-line editor to render multiline input.
type pasteStore struct {
	mu      sync.Mutex
	entries map[string]pasteEntry
	order   []string
	bytes   int
}

type pasteEntry struct {
	text string
	err  error
}

func newPasteStore() *pasteStore { return &pasteStore{entries: make(map[string]pasteEntry)} }

func (s *pasteStore) put(text string, rejected bool) (string, error) {
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	token := fmt.Sprintf("[paste %s: %d lines]", hex.EncodeToString(id[:]), strings.Count(text, "\n")+1)
	entry := pasteEntry{text: text}
	if rejected {
		entry = pasteEntry{err: fmt.Errorf("paste exceeds the 4 MiB limit; split it or put the text in a file")}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Match the editor's history bound and cap all retained paste payloads.
	for len(s.order) > 0 && (len(s.order) >= 500 || s.bytes+len(entry.text) > pasteBudget) {
		old := s.order[0]
		s.order = s.order[1:]
		s.bytes -= len(s.entries[old].text)
		delete(s.entries, old)
	}
	s.entries[token] = entry
	s.order = append(s.order, token)
	s.bytes += len(entry.text)
	return token, nil
}

var pasteToken = regexp.MustCompile(`\[paste [a-f0-9]{16}: [0-9]+ lines\]`)

func (s *pasteStore) expand(line string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var failure error
	expandedBytes := len(line)
	line = pasteToken.ReplaceAllStringFunc(line, func(token string) string {
		if failure != nil {
			return ""
		}
		entry, ok := s.entries[token]
		if !ok {
			failure = fmt.Errorf("paste no longer available; paste it again")
			return ""
		}
		if entry.err != nil {
			failure = entry.err
			return ""
		}
		expandedBytes += len(entry.text) - len(token)
		if expandedBytes > pasteBudget {
			failure = fmt.Errorf("combined paste exceeds the 16 MiB message limit")
			return ""
		}
		return entry.text
	})
	return line, failure
}

// pasteReader recognizes the terminal's explicit paste boundaries, including
// fragmented input. There is no timing heuristic that could eat normal Enter
// keys or mistake a slow paste for many separate submissions.
type pasteReader struct {
	in       *bufio.Reader
	store    *pasteStore
	pending  []byte
	final    error
	terminal bool
}

func newPasteReader(in io.Reader, store *pasteStore) *pasteReader {
	return &pasteReader{in: bufio.NewReader(in), store: store}
}

func newTerminalReader(in io.Reader, store *pasteStore) *pasteReader {
	r := newPasteReader(in, store)
	r.terminal = true
	return r
}

func (r *pasteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		if r.final != nil {
			return 0, r.final
		}
		b, err := r.in.ReadByte()
		if err != nil {
			return 0, err
		}
		if r.terminal && b == '\n' { // Ctrl-J fallback for legacy terminals.
			r.pending = []byte(string(continuationKey) + "\r")
			break
		}
		if b != pasteBegin[0] {
			r.pending = []byte{b}
			break
		}
		sequence := string(b)
		for len(sequence) < 64 {
			if len(sequence) >= 2 && sequence[1] != '[' {
				break
			}
			if len(sequence) > 2 {
				last := sequence[len(sequence)-1]
				if last < '0' || last > '9' && last != ';' {
					break
				}
			}
			b, err = r.in.ReadByte()
			if err != nil {
				r.final = err
				break
			}
			sequence += string([]byte{b})
		}
		if sequence != pasteBegin {
			if r.terminal {
				sequence = decodeTerminalKey(sequence)
			}
			r.pending = []byte(sequence)
			continue
		}
		text, rejected, err := r.readPaste()
		if err != nil {
			return 0, err
		}
		if text == "" && !rejected {
			continue
		}
		token, err := r.store.put(text, rejected)
		if err != nil {
			return 0, err
		}
		r.pending = []byte(token)
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

// Extended keyboard modes also encode keys such as Ctrl-C and Ctrl-D.
// Normalize those back to the bytes readline understands, not just Enter.
func decodeTerminalKey(sequence string) string {
	if !strings.HasPrefix(sequence, "\x1b[") || len(sequence) < 3 {
		return sequence
	}
	fields := strings.Split(sequence[2:len(sequence)-1], ";")
	modifier := 1
	var code int
	var err error
	switch {
	case strings.HasSuffix(sequence, "u") && (len(fields) == 1 || len(fields) == 2):
		code, err = strconv.Atoi(fields[0])
		if len(fields) == 2 && err == nil {
			modifier, err = strconv.Atoi(fields[1])
		}
	case strings.HasSuffix(sequence, "~") && len(fields) == 3 && fields[0] == "27":
		code, err = strconv.Atoi(fields[2])
		if err == nil {
			modifier, err = strconv.Atoi(fields[1])
		}
	default:
		return sequence
	}
	if err != nil || modifier < 1 || modifier > 256 {
		return sequence
	}
	// Kitty disambiguation also gives keypad keys dedicated codes.
	if code >= 57399 && code <= 57408 {
		code = '0' + code - 57399
	} else if code >= 57409 && code <= 57416 {
		code = int([]rune("./*-+\r=,")[code-57409])
	} else if code >= 57417 && code <= 57426 {
		keys := []string{"D", "C", "A", "B", "5~", "6~", "H", "F", "2~", "3~"}
		return "\x1b[" + keys[code-57417]
	}
	if code <= 0 || code > 127 {
		return sequence
	}
	mods := (modifier - 1) & 63 // Caps/Num Lock do not change the shortcut.
	if mods > 7 {
		return sequence
	}
	if code == '\r' && mods == 1 {
		return string(continuationKey) + "\r"
	}
	if mods&4 != 0 {
		if code >= 'a' && code <= 'z' {
			code -= 'a' - 'A'
		}
		if code >= '@' && code <= '_' {
			code &= 31
		}
	} else if mods&1 != 0 && code >= 'a' && code <= 'z' {
		code -= 'a' - 'A'
	}
	if code == 0 { // readline treats NUL as EOF, not Ctrl-Space.
		return ""
	}
	if code == '\n' && mods&2 == 0 {
		return string(continuationKey) + "\r"
	}
	key := string(rune(code))
	if mods&2 != 0 {
		key = "\x1b" + key
	}
	return key
}

func (r *pasteReader) readPaste() (string, bool, error) {
	var text strings.Builder
	rejected := false
	appendByte := func(b byte) {
		if text.Len() < pasteLimit {
			text.WriteByte(b)
		} else {
			rejected = true
		}
	}
	matched := 0
	for {
		b, err := r.in.ReadByte()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return "", false, fmt.Errorf("incomplete bracketed paste: %w", err)
		}
		if b == pasteEnd[matched] {
			matched++
			if matched == len(pasteEnd) {
				return text.String(), rejected, nil
			}
			continue
		}
		for i := 0; i < matched; i++ {
			appendByte(pasteEnd[i])
		}
		matched = 0
		if b == pasteEnd[0] {
			matched = 1
		} else {
			appendByte(b)
		}
	}
}
