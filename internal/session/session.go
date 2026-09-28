// Package session saves chats in ~/.kh/sessions so they can be resumed.
package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kh/internal/config"
)

func dir() string { return filepath.Join(config.Dir(), "sessions") }

// New returns an id that sorts by time, so the latest is simply the last file.
// Milliseconds, so two runs in the same second never clash.
func New() string { return time.Now().Format("20060102-150405.000") }

func Latest() (string, error) {
	entries, _ := os.ReadDir(dir()) // sorted by name
	if len(entries) == 0 {
		return "", fmt.Errorf("no saved sessions")
	}
	return strings.TrimSuffix(entries[len(entries)-1].Name(), ".json"), nil
}

// Save is 0600: sessions contain your code and command output.
func Save(id string, b []byte) error {
	if err := os.MkdirAll(dir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir(), id+".json"), b, 0o600)
}

func Load(id string) ([]byte, error) { return os.ReadFile(filepath.Join(dir(), id+".json")) }
