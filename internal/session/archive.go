package session

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	"kh/internal/provider"
)

const maxArchiveEvent = 32 << 20

// ArchiveRecord is one independently identified receipt-time event. Provider
// snapshots remain the only resume source; archives are never model input.
type ArchiveRecord struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id"`
	provider.Event
	Origin          string `json:"origin,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
}

type Archive struct {
	mu   sync.Mutex
	file *os.File
	id   string
	err  error
}

// OpenArchive validates once with bounded memory and holds an exclusive lock
// until Close, rejecting simultaneous writers of the same session. It repairs
// only an invalid unterminated final record, never a committed JSONL line.
func OpenArchive(id string) (*Archive, error) {
	snapshot, err := path(id)
	if err != nil {
		return nil, err
	}
	name := snapshot + "l"
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return nil, err
	}
	fd, err := unix.Open(name, unix.O_RDWR|unix.O_CREAT|unix.O_APPEND|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open conversation archive: %w", err)
	}
	f := os.NewFile(uintptr(fd), name)
	fail := func(err error) (*Archive, error) {
		f.Close()
		return nil, fmt.Errorf("open conversation archive: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() {
		return fail(fmt.Errorf("not a regular file"))
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fail(err)
	}
	if err := f.Chmod(0o600); err != nil {
		return fail(err)
	}
	if err := validateArchive(f); err != nil {
		return fail(err)
	}
	return &Archive{file: f, id: id}, nil
}

func validateArchive(f *os.File) error {
	r := bufio.NewReader(f)
	var offset int64
	for {
		var line []byte
		for {
			part, err := r.ReadSlice('\n')
			if len(line)+len(part) > maxArchiveEvent {
				return fmt.Errorf("archive record exceeds %d bytes", maxArchiveEvent)
			}
			line = append(line, part...)
			if err == bufio.ErrBufferFull {
				continue
			}
			if err != nil && err != io.EOF {
				return err
			}
			if len(line) == 0 && err == io.EOF {
				return nil
			}
			if err == io.EOF {
				if json.Valid(line) {
					if _, err := f.Write([]byte{'\n'}); err != nil {
						return err
					}
				} else if err := f.Truncate(offset); err != nil {
					return err
				}
				return f.Sync()
			}
			if !json.Valid(line) {
				return fmt.Errorf("invalid complete archive record at byte %d", offset)
			}
			offset += int64(len(line))
			break
		}
	}
}

// Image data URLs may occur inside arbitrary text or tool arguments as well as
// structured image fields. Omit those payloads; this is not general secret
// redaction. JSON's usual escaping protects record boundaries.
var archiveImage = regexp.MustCompile(`(?i)data:image/[a-z0-9.+-]+(?:;[a-z0-9=.+-]+)*,[a-z0-9+/=_%-]+`)

func archiveText(s string) string { return archiveImage.ReplaceAllString(s, "[image data omitted]") }
func scrubArchiveValue(v any) any {
	switch x := v.(type) {
	case string:
		return archiveText(x)
	case []any:
		for i := range x {
			x[i] = scrubArchiveValue(x[i])
		}
	case map[string]any:
		for k, value := range x {
			switch k {
			case "content_base64", "image_base64", "image_url":
				x[k] = "[image/binary data omitted]"
			default:
				x[k] = scrubArchiveValue(value)
			}
		}
	}
	return v
}

// Append writes immediately; semantic boundaries (not each token) sync to disk.
// A failure latches so a later successful call cannot hide archive loss.
func (a *Archive) Append(record ArchiveRecord) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	if a.file == nil {
		return fmt.Errorf("conversation archive closed")
	}
	record.Version, record.SessionID, record.Timestamp = 1, a.id, time.Now().UTC()
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return a.fail(err)
	}
	record.ID = hex.EncodeToString(id)
	record.Text = archiveText(record.Text)
	if len(record.Input) > 0 {
		// Scrub string values and explicit binary/image fields without breaking JSON.
		if !json.Valid(record.Input) {
			return a.fail(fmt.Errorf("invalid archive tool arguments"))
		}
		var input any
		decoder := json.NewDecoder(bytes.NewReader(record.Input))
		decoder.UseNumber()
		if err := decoder.Decode(&input); err != nil {
			return a.fail(err)
		}
		input = scrubArchiveValue(input)
		cleaned, err := json.Marshal(input)
		if err != nil {
			return a.fail(err)
		}
		record.Input = cleaned
	}
	b, err := json.Marshal(record)
	if err != nil {
		return a.fail(err)
	}
	if len(b)+1 > maxArchiveEvent {
		return a.fail(fmt.Errorf("archive event exceeds %d bytes", maxArchiveEvent))
	}
	b = append(b, '\n')
	n, err := a.file.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if err == nil && record.Type != "assistant_delta" {
		err = a.file.Sync()
	}
	if err != nil {
		return a.fail(err)
	}
	return nil
}
func (a *Archive) fail(err error) error {
	a.err = fmt.Errorf("conversation archive: %w", err)
	return a.err
}
func (a *Archive) Err() error { a.mu.Lock(); defer a.mu.Unlock(); return a.err }
func (a *Archive) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file == nil {
		return a.err
	}
	err := errors.Join(a.err, a.file.Sync(), a.file.Close())
	a.file = nil
	return err
}

// ReadArchive streams complete records and ignores only a partial final line.
// A malformed complete record is an error, not silently omitted evidence.
func ReadArchive(r io.Reader, visit func(ArchiveRecord) error) error {
	br := bufio.NewReader(r)
	for {
		var line []byte
		for {
			p, err := br.ReadSlice('\n')
			if len(line)+len(p) > maxArchiveEvent {
				return fmt.Errorf("archive event too large")
			}
			line = append(line, p...)
			if err == bufio.ErrBufferFull {
				continue
			}
			if err != nil && err != io.EOF {
				return err
			}
			if err == io.EOF && !json.Valid(line) {
				return nil
			}
			if len(bytes.TrimSpace(line)) == 0 && err == io.EOF {
				return nil
			}
			var record ArchiveRecord
			if e := json.Unmarshal(line, &record); e != nil {
				return e
			}
			if e := visit(record); e != nil {
				return e
			}
			if err == io.EOF {
				return nil
			}
			break
		}
	}
}
