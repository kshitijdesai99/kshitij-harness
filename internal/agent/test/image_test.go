package test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"kh/internal/agent"
	"kh/internal/image"
	"kh/internal/provider"
)

type imageFake struct {
	fake
	attached string
}

func (f *imageFake) AttachImage(url string) { f.attached = url }

func TestCapturedImageIsSentWithText(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS clipboard only")
	}
	t.Setenv("HOME", t.TempDir())
	fake := t.TempDir()
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lZkAAAAASUVORK5CYII="
	if err := os.WriteFile(filepath.Join(fake, "swift"), []byte("#!/bin/sh\nprintf '%s\\n' '"+png+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker, err := image.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	f := &imageFake{}
	f.step = func(_ context.Context, _ int, _ []provider.Result) ([]provider.Call, error) { return nil, nil }
	if err := (agent.Runner{Model: f}).Run(context.Background(), "Describe "+marker+" briefly"); err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 1 || f.got[0] != "Describe  briefly" || !strings.Contains(f.attached, png) {
		t.Errorf("message: %q, image attached: %v", f.got, f.attached != "")
	}
	if err := (agent.Runner{Model: f}).Run(context.Background(), "Again "+marker); err == nil || !strings.Contains(err.Error(), "press Ctrl-V again") {
		t.Errorf("consumed image accepted: %v", err)
	}
}
