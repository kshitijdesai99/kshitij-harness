// Package image reads image attachments for chat messages.
package image

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const MaxBytes = 10 << 20
const PasteMarker = "[[kh:clipboard-image]]" // old input from already running kh instances

var snapshotID = regexp.MustCompile(`^[a-f0-9]{32}$`)

// Snapshot reads the clipboard at the time Ctrl-V is pressed, not when Enter
// is pressed. It stores the validated image in a private file until consumed.
func Snapshot() (string, error) {
	_, url, err := Read("")
	if err != nil {
		return "", err
	}
	dir, err := snapshotDir()
	if err != nil {
		return "", err
	}
	// Abandoned snapshots (for example, when a prompt is cancelled) are
	// private but should not accumulate indefinitely.
	if entries, e := os.ReadDir(dir); e == nil {
		for _, entry := range entries {
			if !snapshotID.MatchString(entry.Name()) || !entry.Type().IsRegular() {
				continue
			}
			if info, e := entry.Info(); e == nil && time.Since(info.ModTime()) > 24*time.Hour {
				_ = os.Remove(filepath.Join(dir, entry.Name()))
			}
		}
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	name := hex.EncodeToString(id)
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err = f.WriteString(url); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err = f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return "[[kh:image:" + name + "]]", nil
}

// Take consumes only snapshots created by kh (never an arbitrary path).
func Take(id string) (string, error) {
	if !snapshotID.MatchString(id) {
		return "", fmt.Errorf("invalid image attachment")
	}
	dir, err := snapshotDir()
	if err != nil {
		return "", err
	}
	name := filepath.Join(dir, id)
	defer os.Remove(name)
	b, err := os.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("image attachment expired or missing; press Ctrl-V again: %w", err)
	}
	if len(b) > 2*MaxBytes || !strings.HasPrefix(string(b), "data:image/") {
		return "", fmt.Errorf("invalid image attachment")
	}
	return string(b), nil
}

func snapshotDir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "kh", "clipboard")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("clipboard cache is not private: %s", dir)
	}
	return dir, nil
}

// Read reads a raster image from a file, or from the macOS clipboard if path is empty.
func Read(path string) (string, string, error) {
	var data []byte
	if path == "" {
		if runtime.GOOS != "darwin" {
			return "", "", fmt.Errorf("clipboard images require macOS; use /image path/to/image.png")
		}
		// AppKit reads raster formats exposed by the pasteboard, including
		// images that have to be converted from TIFF to PNG.
		out, err := exec.Command("swift", "-e", `import AppKit
let pb = NSPasteboard.general
let png = pb.data(forType: NSPasteboard.PasteboardType("public.png")) ?? {
    guard let image = NSImage(pasteboard: pb), let tiff = image.tiffRepresentation,
          let bitmap = NSBitmapImageRep(data: tiff) else { return nil }
    return bitmap.representation(using: .png, properties: [:])
}()
if let png { print(png.base64EncodedString()) }
else {
    let types = pb.types?.map { $0.rawValue }.joined(separator: ", ") ?? "empty"
    fputs("clipboard has no image (pasteboard types: \(types))\n", stderr)
    exit(1)
}
`).Output()
		if err != nil {
			if failure, ok := err.(*exec.ExitError); ok {
				return "", "", fmt.Errorf("%s; copy an image or use /image path/to/image.png", strings.TrimSpace(string(failure.Stderr)))
			}
			return "", "", fmt.Errorf("read macOS clipboard: %w", err)
		}
		var errDecode error
		data, errDecode = base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
		if errDecode != nil {
			return "", "", fmt.Errorf("decode clipboard image: %w", errDecode)
		}
	} else {
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			return "", "", err
		}
	}
	return Encode(data)
}

// Encode validates a supported image and returns its media type and base64 data URL.
func Encode(data []byte) (string, string, error) {
	if len(data) == 0 || len(data) > MaxBytes {
		return "", "", fmt.Errorf("image must be between 1 byte and %d MB", MaxBytes>>20)
	}
	mime := http.DetectContentType(data)
	switch mime {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
	default:
		return "", "", fmt.Errorf("unsupported image type %s (use PNG, JPEG, WebP or GIF)", mime)
	}
	if mime == "image/png" && !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return "", "", fmt.Errorf("invalid PNG header")
	}
	return mime, "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}
