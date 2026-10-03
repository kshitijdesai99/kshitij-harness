package terminal

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
)

const (
	pasteBegin  = "\x1b[200~"
	pasteEnd    = "\x1b[201~"
	pasteLimit  = 4 << 20
	pasteBudget = 16 << 20
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
	in      *bufio.Reader
	store   *pasteStore
	pending []byte
	final   error
}

func newPasteReader(in io.Reader, store *pasteStore) *pasteReader {
	return &pasteReader{in: bufio.NewReader(in), store: store}
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
		if b != pasteBegin[0] {
			r.pending = []byte{b}
			break
		}
		sequence := []byte{b}
		matched := true
		for i := 1; i < len(pasteBegin); i++ {
			b, err = r.in.ReadByte()
			if err != nil {
				r.final = err
				matched = false
				break
			}
			sequence = append(sequence, b)
			if b != pasteBegin[i] {
				matched = false
				break
			}
		}
		if !matched {
			r.pending = sequence
			break
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
