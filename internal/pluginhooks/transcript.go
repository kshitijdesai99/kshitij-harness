package pluginhooks

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Transcript is a private, text-only Claude-compatible projection, not provider
// history or the kh conversation archive. Callers must supply only real root
// user messages and completed assistant replies, never worker or peer input.
type Transcript struct {
	mu                              sync.Mutex
	path, configDir, cwd, sessionID string
}

func OpenTranscript(configDir, cwd, sessionID string) (*Transcript, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`).MatchString(sessionID) {
		return nil, fmt.Errorf("invalid projection session ID")
	}
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		configDir = filepath.Join(home, ".kh", "plugin-transcripts", "config")
	}
	var err error
	configDir, err = filepath.Abs(configDir)
	if err != nil {
		return nil, err
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	project := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(cwd, "-")
	dir := filepath.Join(configDir, "projects", project)
	for _, p := range []string{configDir, filepath.Join(configDir, "projects"), dir} {
		if err := os.MkdirAll(p, 0700); err != nil {
			return nil, err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("unsafe projection directory")
		}
		if err := os.Chmod(p, 0700); err != nil {
			return nil, err
		}
	}
	t := &Transcript{path: filepath.Join(dir, sessionID+".jsonl"), configDir: configDir, cwd: cwd, sessionID: sessionID}
	f, err := t.open()
	if err != nil {
		return nil, err
	}
	f.Close()
	return t, nil
}
func (t *Transcript) Path() string      { return t.path }
func (t *Transcript) ConfigDir() string { return t.configDir }
func (t *Transcript) AppendUser(id, text string, at time.Time) error {
	return t.append("user", id, text, at)
}
func (t *Transcript) AppendAssistant(id, text string, at time.Time) error {
	return t.append("assistant", id, text, at)
}
func (t *Transcript) open() (*os.File, error) {
	fd, err := unix.Open(t.path, unix.O_RDWR|unix.O_CREAT|unix.O_APPEND|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), t.path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("unsafe projection file")
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
func (t *Transcript) append(role, id, text string, at time.Time) error {
	if id == "" || at.IsZero() {
		return fmt.Errorf("projection requires stable receipt ID and timestamp")
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if len(text) > 1024*1024 {
		return fmt.Errorf("projection message exceeds limit")
	}
	sum := sha256.Sum256([]byte(t.sessionID + "\x00" + role + "\x00" + id))
	hex := fmt.Sprintf("%x", sum[:16])
	uuid := hex[:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:]
	t.mu.Lock()
	defer t.mu.Unlock()
	f, err := t.open()
	if err != nil {
		return err
	}
	defer f.Close()
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > 64*1024*1024 {
		return fmt.Errorf("projection exceeds size limit")
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 8*1024*1024)
	parent := ""
	for scanner.Scan() {
		line := scanner.Bytes()
		var rec struct {
			UUID string `json:"uuid"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			return fmt.Errorf("invalid projection record: %w", err)
		}
		if rec.UUID == uuid {
			return nil
		}
		parent = rec.UUID
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	// A partial line is never silently joined to a new event.
	if info.Size() > 0 {
		tail := []byte{0}
		if _, err = f.ReadAt(tail, info.Size()-1); err != nil && err != io.EOF {
			return err
		}
		if tail[0] != '\n' {
			return fmt.Errorf("unterminated projection record")
		}
	}
	var content any = text
	if role == "assistant" {
		content = []map[string]string{{"type": "text", "text": text}}
	}
	record := map[string]any{"type": role, "uuid": uuid, "parentUuid": nil, "sessionId": t.sessionID, "cwd": t.cwd, "timestamp": at.UTC().Format(time.RFC3339Nano), "isSidechain": false, "message": map[string]any{"role": role, "content": content}}
	if parent != "" {
		record["parentUuid"] = parent
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	n, err := f.Write(data)
	if err != nil || n != len(data) {
		_ = f.Truncate(info.Size())
		if err != nil {
			return err
		}
		return io.ErrShortWrite
	}
	return f.Sync()
}
