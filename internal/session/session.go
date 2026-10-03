// Package session saves opaque provider state per working folder.
package session

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"kh/internal/config"
)

func dir() string {
	wd, _ := os.Getwd()
	if r, err := filepath.EvalSymlinks(wd); err == nil {
		wd = r
	}
	return filepath.Join(config.Dir(), "sessions", strings.ReplaceAll(wd, string(filepath.Separator), "-"))
}

func New() string { return time.Now().Format("20060102-150405.000000000") }

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func path(id string) (string, error) {
	if !validID.MatchString(id) {
		return "", fmt.Errorf("invalid session id %q", id)
	}
	return filepath.Join(dir(), id+".json"), nil
}

// List ignores temporary files from interrupted saves.
func List() []string {
	entries, _ := os.ReadDir(dir())
	var ids []string
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".json")
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && validID.MatchString(id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// Entry describes a saved chat without decoding provider state.
// SavedAt is the file modification time, not a response-generation timestamp.
type Entry struct {
	ID      string
	SavedAt time.Time
}

// Recent lists all chats newest-save first. Equal times use descending IDs.
// List and Latest retain their existing creation-ID ordering.
func Recent() []Entry {
	var entries []Entry
	for _, id := range List() {
		name, err := path(id)
		if err != nil {
			continue
		}
		info, err := os.Stat(name)
		if err != nil {
			continue
		}
		entries = append(entries, Entry{ID: id, SavedAt: info.ModTime()})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].SavedAt.Equal(entries[j].SavedAt) {
			return entries[i].ID > entries[j].ID
		}
		return entries[i].SavedAt.After(entries[j].SavedAt)
	})
	return entries
}

func Latest() (string, error) {
	ids := List()
	if len(ids) == 0 {
		return "", fmt.Errorf("no saved sessions")
	}
	return ids[len(ids)-1], nil
}

// Save never truncates the previous chat. The temporary file is private and
// lives beside its destination so rename is atomic on the same filesystem.
func Save(id string, b []byte) error {
	name, err := path(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir(), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir(), ".save-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), name)
}

func Load(id string) ([]byte, error) {
	name, err := path(id)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(name)
}
