package test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"kh/internal/config"
)

func TestSandbox(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox is macOS only")
	}
	os.Chdir(t.TempDir())
	home, _ := os.UserHomeDir()
	probe := filepath.Join(home, ".kh_sandbox_probe")
	defer os.Remove(probe)

	cfg := config.Defaults
	cfg.Yes = true
	out, _ := bash(t, cfg, "touch inside && touch "+probe)
	if _, err := os.Stat("inside"); err != nil {
		t.Error("write inside the project was blocked")
	}
	if _, err := os.Stat(probe); err == nil {
		t.Error("write to home was allowed")
	}
	if !strings.Contains(out, "-nosandbox") {
		t.Errorf("model is not told how to allow it: %s", out)
	}

	cfg.Sandbox = false
	bash(t, cfg, "touch "+probe)
	if _, err := os.Stat(probe); err != nil {
		t.Error("-nosandbox did not allow the write")
	}
}
