package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"kh/internal/config"
)

func TestSandbox(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox is macOS only")
	}
	t.Chdir(t.TempDir())
	// A host/container may prohibit starting sandbox-exec (including nesting).
	// Probe startup independently so that environment limitations aren't
	// mistaken for failures of kh's write policy.
	if out, err := exec.Command("/usr/bin/sandbox-exec", "-p", "(version 1)(allow default)", "/usr/bin/true").CombinedOutput(); err != nil {
		t.Skipf("sandbox-exec cannot start in this environment: %v: %s", err, out)
	}
	outside := t.TempDir()
	probe := filepath.Join(outside, "probe")

	cfg := config.Defaults
	cfg.Auto, cfg.Sandbox, cfg.Writable = true, true, nil
	out, err := bashWithSandbox(t, cfg, "touch inside && touch "+strconv.Quote(probe))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("inside"); err != nil {
		t.Error("write inside the project was blocked")
	}
	if _, err := os.Stat(probe); err == nil {
		t.Error("write outside the project was allowed")
	}
	if !strings.Contains(out, "-nosandbox") {
		t.Errorf("model is not told how to allow it: %s", out)
	}

	cfg.Sandbox = false
	if out, err := bash(t, cfg, "touch "+strconv.Quote(probe)); err != nil {
		t.Fatalf("unsandboxed command failed: %v: %s", err, out)
	}
	if _, err := os.Stat(probe); err != nil {
		t.Error("-nosandbox did not allow the write")
	}
}
