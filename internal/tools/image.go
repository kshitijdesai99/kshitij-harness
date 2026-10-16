package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"kh/internal/image"
	"kh/internal/provider"
)

// ViewImage loads a local image into the model's visual context. Its action
// callback receives only a path label, never the encoded image contents.
func ViewImage(notify func(string)) Tool {
	return Tool{
		Name:        "view_image",
		Description: "Load a local PNG, JPEG, WebP or GIF image (up to 10 MB) into your visual context for inspection. Sends actual image contents to the active model, not just metadata. Accepts repository-relative, absolute or ~/ paths, including files outside the repository. No URLs or clipboard reads.",
		Params:      map[string]any{"path": map[string]any{"type": "string", "description": "Local image file path"}},
		Required:    []string{"path"},
		RunResult: func(ctx context.Context, input json.RawMessage) (provider.Result, error) {
			if err := ctx.Err(); err != nil {
				return provider.Result{}, err
			}
			var args struct{ Path string }
			if err := json.Unmarshal(input, &args); err != nil {
				return provider.Result{}, fmt.Errorf("invalid view_image input: %w", err)
			}
			if strings.TrimSpace(args.Path) == "" {
				return provider.Result{}, fmt.Errorf("view_image requires a nonblank path")
			}
			// Bare local names can contain colons (build-10:30.png). Only
			// reject explicit URL/data syntax, never infer a scheme from that.
			if strings.Contains(args.Path, "://") || strings.HasPrefix(strings.ToLower(args.Path), "data:") {
				return provider.Result{}, fmt.Errorf("view_image requires a local file path, not a URL")
			}
			path := args.Path
			if strings.HasPrefix(path, "~/") {
				home, err := os.UserHomeDir()
				if err != nil {
					return provider.Result{}, err
				}
				path = filepath.Join(home, path[2:])
			}
			path, err := filepath.Abs(path)
			if err != nil {
				return provider.Result{}, err
			}
			if notify != nil {
				notify("view_image " + path)
			}
			dataURL, err := image.Read(path)
			if err != nil {
				return provider.Result{}, err
			}
			if err := ctx.Err(); err != nil {
				return provider.Result{}, err
			}
			mime, _, _ := strings.Cut(strings.TrimPrefix(dataURL, "data:"), ";")
			return provider.Result{Output: fmt.Sprintf("Loaded image %q (%s).", path, mime), Images: []string{dataURL}}, nil
		},
	}
}
