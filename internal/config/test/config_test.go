package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kh/internal/config"
)

// Set changes one key and keeps the rest of the user's file.
func TestSetKeepsOtherKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".kh"), 0o700)
	os.WriteFile(filepath.Join(home, ".kh", "config.json"), []byte(`{"timeout_sec": 99}`), 0o600)

	if err := config.Set("model", "gpt-5.5"); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Model != "gpt-5.5" || c.TimeoutSec != 99 || c.Effort != config.Defaults.Effort {
		t.Errorf("got model %q timeout %d effort %q", c.Model, c.TimeoutSec, c.Effort)
	}
}

func TestSetRefusesBrokenFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".kh"), 0o700)
	os.WriteFile(filepath.Join(home, ".kh", "config.json"), []byte(`{broken`), 0o600)
	if err := config.Set("model", "x"); err == nil {
		t.Error("should not overwrite a file it can't read")
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".kh", "config.json")); !strings.Contains(string(b), "broken") {
		t.Error("broken file was overwritten")
	}
}
