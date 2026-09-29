package test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kh/internal/image"
)

func TestEncodeAndReadImage(t *testing.T) {
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lZkAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	mime, url, err := image.Encode(png)
	if err != nil || mime != "image/png" || !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("encode: %s %s %v", mime, url, err)
	}
	path := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(path, png, 0o600); err != nil {
		t.Fatal(err)
	}
	fileMime, fileURL, err := image.Read(path)
	if err != nil || fileMime != mime || fileURL != url {
		t.Fatalf("read: %s %s %v", fileMime, fileURL, err)
	}
}

func TestImageValidation(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("not an image"), make([]byte, image.MaxBytes+1)} {
		if _, _, err := image.Encode(data); err == nil {
			t.Errorf("accepted %d bytes", len(data))
		}
	}
}
