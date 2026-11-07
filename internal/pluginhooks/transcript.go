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
	mu                                       sync.Mutex
	path, configDir, project, cwd, sessionID string
}

func OpenTranscript(configDir, cwd, sessionID string) (*Transcript, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`).MatchString(sessionID) {
		return nil, fmt.Errorf("invalid projection session ID")
	}
	migrate := configDir == ""
	if configDir == "" {
		configDir = os.Getenv("CLAUDE_CONFIG_DIR")
		if configDir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			configDir = filepath.Join(home, ".claude")
		}
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
	sum := sha256.Sum256([]byte(cwd))
	project := fmt.Sprintf("kh-plugin-%x", sum[:])
	dir := filepath.Join(configDir, "projects", project)
	fd, err := projectionDir(configDir, project, true)
	if err != nil {
		return nil, err
	}
	unix.Close(fd)
	t := &Transcript{path: filepath.Join(dir, sessionID+".jsonl"), configDir: configDir, project: project, cwd: cwd, sessionID: sessionID}
	f, err := t.open()
	if err != nil {
		return nil, err
	}
	f.Close()
	if migrate {
		if err := t.migrateLegacy(); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// projectionDir walks the shared config and projects directories using pinned
// descriptors. Only kh's own namespace is chmodded; native Claude data is not.
func projectionDir(config, project string, create bool) (int, error) {
	if create {
		if err := os.MkdirAll(config, 0700); err != nil {
			return -1, err
		}
	}
	fd, err := unix.Open(config, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, name := range []string{"projects", project} {
		if create {
			if err = unix.Mkdirat(fd, name, 0700); err != nil && err != unix.EEXIST {
				unix.Close(fd)
				return -1, err
			}
		}
		next, openErr := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if openErr != nil {
			return -1, openErr
		}
		fd = next
	}
	if create {
		if err := unix.Fchmod(fd, 0700); err != nil {
			unix.Close(fd)
			return -1, err
		}
	}
	return fd, nil
}

// migrateLegacy preserves existing projection identities and timestamps without
// moving or deleting the private original. Never merge over a published stream.
func (t *Transcript) migrateLegacy() error {
	target, err := t.open()
	if err != nil {
		return err
	}
	defer target.Close()
	if err := unix.Flock(int(target.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(target.Fd()), unix.LOCK_UN)
	info, err := target.Stat()
	if err != nil || info.Size() != 0 {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	legacy := filepath.Join(home, ".kh", "plugin-transcripts", "config")
	project := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(t.cwd, "-")
	dir, err := projectionDir(legacy, project, false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, t.sessionID+".jsonl", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	source := os.NewFile(uintptr(fd), "legacy projection")
	defer source.Close()
	info, err = source.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("unsafe legacy projection")
	}
	if err := unix.Flock(fd, unix.LOCK_SH); err != nil {
		return err
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	data, err := io.ReadAll(io.LimitReader(source, 64*1024*1024+1))
	if err != nil {
		return err
	}
	if len(data) > 64*1024*1024 || (len(data) > 0 && data[len(data)-1] != '\n') {
		return fmt.Errorf("invalid legacy projection size or termination")
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" && len(data) == 0 {
			break
		}
		var rec struct {
			Type, UUID, SessionID, Cwd string
		}
		if json.Unmarshal([]byte(line), &rec) != nil || rec.UUID == "" || rec.SessionID != t.sessionID || rec.Cwd != t.cwd || (rec.Type != "user" && rec.Type != "assistant") {
			return fmt.Errorf("invalid legacy projection record")
		}
	}
	if n, err := target.Write(data); err != nil || n != len(data) {
		_ = target.Truncate(0)
		return fmt.Errorf("copy legacy projection: %w", io.ErrShortWrite)
	}
	return target.Sync()
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
	dir, err := projectionDir(t.configDir, t.project, false)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, t.sessionID+".jsonl", unix.O_RDWR|unix.O_CREAT|unix.O_APPEND|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), t.path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("unsafe projection file")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Nlink != 1 {
		f.Close()
		return nil, fmt.Errorf("unsafe projection hard link")
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
