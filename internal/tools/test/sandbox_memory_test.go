package test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"kh/internal/config"
	"kh/internal/memory"
)

// The subprocess exercises the same public store used by kh memory remember,
// including creating the directory/database, chmod, SQLite writes and WAL.
func TestSandboxMemoryHelper(t *testing.T) {
	if os.Getenv("KH_SANDBOX_MEMORY_HELPER") != "1" {
		return
	}
	s, err := memory.Open(memory.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _, err = s.Add(context.Background(), memory.Note{
		Repo: "sandbox-test", Kind: "instruction", Key: "memory-access",
		Text: "Memory remains writable under the project sandbox.", Source: "you",
	}, memory.Any)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(memory.Path() + "-wal"); err != nil {
		t.Fatalf("SQLite WAL not created: %v", err)
	}
	fmt.Println("MEMORY_WRITE_OK")
}

func TestSandboxMemoryAlwaysWritable(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox is macOS only")
	}
	if out, err := exec.Command("/usr/bin/sandbox-exec", "-p", "(version 1)(allow default)", "/usr/bin/true").CombinedOutput(); err != nil {
		t.Skipf("sandbox-exec cannot start in this environment: %v: %s", err, out)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"new-directory", "existing-directory", "symlink-directory"} {
		t.Run(mode, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("HOME", t.TempDir())
			dir := config.Dir()
			if mode == "existing-directory" {
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "symlink-directory" {
				if err := os.Symlink(t.TempDir(), dir); err != nil {
					t.Fatal(err)
				}
			}
			cfg := config.Defaults
			cfg.Auto, cfg.Sandbox, cfg.Writable = true, true, nil
			command := "KH_SANDBOX_MEMORY_HELPER=1 " + strconv.Quote(binary) + " -test.run=^TestSandboxMemoryHelper$"
			out, err := bashWithSandbox(t, cfg, command)
			if err != nil || !strings.Contains(out, "MEMORY_WRITE_OK") {
				t.Fatalf("sandbox blocked memory: %v: %s", err, out)
			}
			s, err := memory.Open(memory.Path())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			notes, err := s.Current(context.Background(), "sandbox-test", "instruction")
			if err != nil || len(notes) != 1 || notes[0].Key != "memory-access" {
				t.Fatalf("memory not persisted: %+v %v", notes, err)
			}
			info, err := os.Stat(memory.Path())
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("memory permissions: %v %v", info, err)
			}
			// Neither existing config/login nor unrelated files become writable.
			for _, name := range []string{"config.json", "auth.json", "other", "memory.db.backup"} {
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, []byte("unchanged"), 0o600); err != nil {
					t.Fatal(err)
				}
				out, err := bashWithSandbox(t, cfg, "printf modified > "+strconv.Quote(path))
				if err != nil || !strings.Contains(out, "Operation not permitted") {
					t.Fatalf("expected denied write to %s: %v: %s", name, err, out)
				}
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "unchanged" {
					t.Fatalf("protected file changed: %s: %q %v", name, data, err)
				}
			}
		})
	}
}
