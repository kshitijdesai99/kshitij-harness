// Package session saves opaque provider state per working folder.
package session

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
