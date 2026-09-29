package test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"kh/internal/image"
)

func TestSnapshotAtKeypressAndDiagnostic(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS clipboard only")
	}
	t.Setenv("HOME", t.TempDir())
	fake := t.TempDir()
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	swift := filepath.Join(fake, "swift")
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lZkAAAAASUVORK5CYII="
	if err := os.WriteFile(swift, []byte("#!/bin/sh\nprintf '%s\\n' '"+png+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker, err := image.Snapshot()
	if err != nil || !strings.HasPrefix(marker, "[[kh:image:") {
		t.Fatalf("snapshot: %q %v", marker, err)
	}
	// Simulate copying text after pressing Ctrl-V but before Enter.
	if err := os.WriteFile(swift, []byte("#!/bin/sh\necho 'clipboard has no image (pasteboard types: public.utf8-plain-text)' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := image.Snapshot(); err == nil || !strings.Contains(err.Error(), "public.utf8-plain-text") {
		t.Fatalf("missing useful pasteboard diagnosis: %v", err)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(marker, "[[kh:image:"), "]]")
	url, err := image.Take(id)
	if err != nil || !strings.HasPrefix(url, "data:image/png;base64,"+png) {
		t.Fatalf("image changed after Ctrl-V: %s %v", url, err)
	}
	if _, err := image.Take(id); err == nil || !strings.Contains(err.Error(), "press Ctrl-V again") {
		t.Fatalf("snapshot was not consumed: %v", err)
	}
	if _, err := image.Take("../etc/passwd"); err == nil {
		t.Fatal("accepted arbitrary path")
	}
}
