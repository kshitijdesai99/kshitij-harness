package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultInteractiveAndHeadlessModes(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		input string
		want  string
		fails bool
	}{
		{"bare", nil, "/auto\n", "auto on:", false},
		{"task-stays", []string{"hello"}, "", "kh chat, session", false},
		{"interactive-alias", []string{"-i", "hello"}, "", "kh chat, session", false},
		{"headless", []string{"-p", "hello"}, "", "not logged in", true},
		{"headless-empty", []string{"-p"}, "", "usage: kh -p", true},
		{"headless-blank", []string{"-p", " "}, "", "usage: kh -p", true},
		{"conflict", []string{"-p", "-i", "hello"}, "", "cannot be combined", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Join(home, ".kh"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".kh", "config.json"), []byte(`{"memory_model":"off"}`), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, tc.args...)
			cmd.Dir = home
			cmd.Env = append(os.Environ(), "HOME="+home, "KH_AUTO_REBUILD=0", "KH_AUTO=", "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", "KH_PARENT=", "KH_AGENT=", "TMUX=", "TMUX_PANE=")
			cmd.Stdin = strings.NewReader(tc.input)
			out, err := cmd.CombinedOutput()
			if (err != nil) != tc.fails || !strings.Contains(string(out), tc.want) {
				t.Fatalf("output: %s err=%v", out, err)
			}
			if len(tc.args) > 0 && tc.args[0] == "-p" && strings.Contains(string(out), "kh chat, session") {
				t.Fatalf("headless entered chat: %s", out)
			}
		})
	}
}
