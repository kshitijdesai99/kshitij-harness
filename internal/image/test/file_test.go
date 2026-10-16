package test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"kh/internal/image"
)

func TestReadRejectsNonImagesAndNonregularFiles(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty")
	text := filepath.Join(dir, "text.png")
	large := filepath.Join(dir, "large")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(text, []byte("not an image"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(image.MaxBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	for _, path := range []string{dir, fifo, "/dev/null", empty, text, large, filepath.Join(dir, "missing")} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			if _, err := image.Read(path); err == nil {
				t.Fatalf("accepted %s", path)
			}
		})
	}
}
