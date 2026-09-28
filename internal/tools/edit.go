package tools

import (
	"context"
	"encoding/json"
	"fmt"
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
		if rel, err := filepath.Rel(".", in.Path); err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return "", fmt.Errorf("path must be inside the project")
		}
		fmt.Fprintln(os.Stderr, "edit", in.Path)

		if in.Old == "" {
			os.MkdirAll(filepath.Dir(in.Path), 0o755)
			f, err := os.OpenFile(in.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				return "", err // includes "file exists"
			}
			defer f.Close()
			_, err = f.WriteString(in.New)
			return "ok", err
		}

		b, err := os.ReadFile(in.Path)
		if err != nil {
			return "", err
		}
		if n := strings.Count(string(b), in.Old); n != 1 {
			return "", fmt.Errorf("old appears %d times, need exactly 1", n)
		}
		info, _ := os.Stat(in.Path)
		return "ok", os.WriteFile(in.Path, []byte(strings.Replace(string(b), in.Old, in.New, 1)), info.Mode())
	},
}
