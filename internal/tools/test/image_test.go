package test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"kh/internal/image"
	"kh/internal/tools"
)

func localImageFixture(t *testing.T) (string, []byte) {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lZkAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "画像 with spaces.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func TestViewImageLocalPaths(t *testing.T) {
	path, data := localImageFixture(t)
	expected, err := image.Encode(data)
	if err != nil {
		t.Fatal(err)
	}
	// TempDir is outside the repository. A relative traversal is valid too.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Dir(path))
	for _, name := range []string{path, relative, "~/" + filepath.Base(path)} {
		t.Run(name, func(t *testing.T) {
			var notices []string
			tool := tools.ViewImage(func(s string) { notices = append(notices, s) })
			if tool.Name != "view_image" || len(tool.Required) != 1 || tool.Required[0] != "path" {
				t.Fatal("invalid tool schema")
			}
			input, _ := json.Marshal(map[string]string{"path": name})
			got, err := tool.RunResult(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Images) != 1 || got.Images[0] != expected {
				t.Fatal("image data mismatch")
			}
			if !strings.Contains(got.Output, "image/png") || !strings.Contains(got.Output, filepath.Base(path)) || strings.Contains(got.Output, "base64") {
				t.Fatalf("output: %s", got.Output)
			}
			if len(notices) != 1 || notices[0] != "view_image "+path {
				t.Fatalf("notices: %q", notices)
			}
		})
	}
}

func TestViewImageBareColonFilename(t *testing.T) {
	path, _ := localImageFixture(t)
	t.Chdir(filepath.Dir(path))
	if err := os.Rename(path, "build-10:30.png"); err != nil {
		t.Fatal(err)
	}
	result, err := tools.ViewImage(nil).RunResult(context.Background(), json.RawMessage(`{"path":"build-10:30.png"}`))
	if err != nil || len(result.Images) != 1 {
		t.Fatalf("valid colon filename rejected: %+v %v", result, err)
	}
}

func TestViewImageValidation(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.png")
	unsupported := filepath.Join(dir, "text.png")
	large := filepath.Join(dir, "large.png")
	for name, data := range map[string][]byte{empty: nil, unsupported: []byte("plain text")} {
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(image.MaxBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	fifo := filepath.Join(dir, "fifo.png")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	inputs := []string{`{`, `{}`, `null`, `{"path":null}`, `{"path":12}`, `{"path":""}`, `{"path":"  \n "}`}
	for _, path := range []string{filepath.Join(dir, "missing"), empty, unsupported, large, dir, fifo, "https://example.com/image.png", "data:image/png;base64,abc", "file:///tmp/image.png", "/dev/null"} {
		input, _ := json.Marshal(map[string]string{"path": path})
		inputs = append(inputs, string(input))
	}
	tool := tools.ViewImage(nil)
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			result, err := tool.RunResult(context.Background(), json.RawMessage(input))
			if err == nil || len(result.Images) != 0 {
				t.Fatalf("accepted %s", input)
			}
		})
	}
}

func TestViewImageCancellation(t *testing.T) {
	path, _ := localImageFixture(t)
	input, _ := json.Marshal(map[string]string{"path": path})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := tools.ViewImage(nil).RunResult(ctx, input)
	if !errors.Is(err, context.Canceled) || len(result.Images) != 0 {
		t.Fatalf("cancel: %v", err)
	}
	// Cancelling in the notification also checks cancellation after the read.
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	result, err = tools.ViewImage(func(string) { cancel() }).RunResult(ctx, input)
	if !errors.Is(err, context.Canceled) || len(result.Images) != 0 {
		t.Fatalf("cancel after notification: %v", err)
	}
}
