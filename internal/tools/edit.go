package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Edit replaces text that appears exactly once, or creates a new file when old is empty.
// Find-and-replace keeps output tokens small: the model never rewrites whole files.
var Edit = Tool{
	Name:        "edit",
	Description: "Replace old with new in a file; old must appear exactly once. Empty old creates a new file with new as its content.",
	Params: map[string]any{
		"path": map[string]any{"type": "string"},
		"old":  map[string]any{"type": "string"},
		"new":  map[string]any{"type": "string"},
	},
	Required: []string{"path", "old", "new"},
	Run: func(ctx context.Context, input json.RawMessage) (string, error) {
		var in struct{ Path, Old, New string }
		if json.Unmarshal(input, &in) != nil || in.Path == "" {
			return "", fmt.Errorf("need path, old, new")
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		abs, err := filepath.Abs(in.Path)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(cwd, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return "", fmt.Errorf("path must be inside the project")
		}
		root, err := os.OpenRoot(cwd)
		if err != nil {
			return "", err
		}
		defer root.Close()
		fmt.Fprintln(os.Stderr, "edit", in.Path)

		if in.Old == "" {
			// Root.MkdirAll is newer than Go 1.24. Each prefix is instead
			// resolved through Root.Mkdir/Open, never through an unchecked path.
			parent := filepath.Dir(rel)
			prefix := ""
			for _, part := range strings.Split(parent, string(filepath.Separator)) {
				prefix = filepath.Join(prefix, part)
				if err := root.Mkdir(prefix, 0o755); err != nil {
					dir, openErr := root.Open(prefix)
					if openErr != nil {
						return "", openErr
					}
					info, statErr := dir.Stat()
					dir.Close()
					if statErr != nil {
						return "", statErr
					}
					if !info.IsDir() {
						return "", err
					}
				}
			}
			f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				return "", err
			}
			_, err = f.WriteString(in.New)
			closeErr := f.Close()
			if err != nil {
				return "", err
			}
			return "ok", closeErr
		}

		// Read and write the same opened file; a renamed/replaced path cannot
		// redirect the write, and no unchecked second Stat is required.
		f, err := root.OpenFile(rel, os.O_RDWR, 0)
		if err != nil {
			return "", err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("path must be a regular file")
		}
		b, err := io.ReadAll(f)
		if err != nil {
			return "", err
		}
		if n := strings.Count(string(b), in.Old); n != 1 {
			return "", fmt.Errorf("old appears %d times, need exactly 1", n)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		replacement := strings.Replace(string(b), in.Old, in.New, 1)
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		if _, err := f.WriteString(replacement); err != nil {
			return "", err
		}
		if err := f.Truncate(int64(len(replacement))); err != nil {
			return "", err
		}
		return "ok", f.Close()
	},
}
