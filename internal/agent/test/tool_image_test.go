package test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kh/internal/agent"
	"kh/internal/provider"
	"kh/internal/tools"
)

func TestViewImageReachesNextModelStep(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lZkAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "picture.png")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"path": path})
	f := &fake{step: func(_ context.Context, n int, results []provider.Result) ([]provider.Call, error) {
		if n == 1 {
			return []provider.Call{{ID: "image", Name: "view_image", Input: input}, {ID: "text", Name: "noop"}}, nil
		}
		if len(results) != 2 || results[0].ID != "image" || len(results[0].Images) != 1 || !strings.HasPrefix(results[0].Images[0], "data:image/png;base64,") || results[0].IsError || results[1].ID != "text" || results[1].Output != "ok" || len(results[1].Images) != 0 {
			t.Errorf("multimodal tool results: %+v", results)
			return nil, nil
		}
		if strings.Contains(results[0].Output, "base64") {
			t.Error("image bytes leaked to text")
		}
		return nil, nil
	}}
	if err := (agent.Runner{Model: f, Tools: []tools.Tool{tools.ViewImage(nil), noop}}).Run(context.Background(), "Inspect the local image."); err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 2 || f.got[1] != "" {
		t.Fatalf("tool result required an extra user message: %v", f.got)
	}
}

func TestRichToolErrorsDiscardImagesAndKeepCallID(t *testing.T) {
	for _, explicitError := range []bool{false, true} {
		t.Run(map[bool]string{true: "returned error", false: "error result"}[explicitError], func(t *testing.T) {
			tool := tools.Tool{Name: "rich", RunResult: func(context.Context, json.RawMessage) (provider.Result, error) {
				r := provider.Result{ID: "wrong", Output: "failed", IsError: !explicitError, Images: []string{"data:image/png;base64,DO_NOT_SEND"}}
				if explicitError {
					return r, errors.New("failed")
				}
				return r, nil
			}}
			f := &fake{step: func(_ context.Context, n int, results []provider.Result) ([]provider.Call, error) {
				if n == 1 {
					return []provider.Call{{ID: "actual", Name: "rich"}}, nil
				}
				if len(results) != 1 || results[0].ID != "actual" || !results[0].IsError || len(results[0].Images) != 0 || results[0].Output != "failed" {
					t.Errorf("unsafe error result: %+v", results)
				}
				return nil, nil
			}}
			if err := (agent.Runner{Model: f, Tools: []tools.Tool{tool}}).Run(context.Background(), "task"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestImageToolResultsSurviveCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tool := tools.Tool{Name: "rich", RunResult: func(context.Context, json.RawMessage) (provider.Result, error) {
		cancel()
		return provider.Result{Output: "loaded", Images: []string{"data:image/png;base64,test"}}, nil
	}}
	f := &fake{step: func(ctx context.Context, n int, results []provider.Result) ([]provider.Call, error) {
		if n == 1 {
			return []provider.Call{{ID: "image", Name: "rich"}}, nil
		}
		if ctx.Err() == nil || len(results) != 1 || results[0].ID != "image" || len(results[0].Images) != 1 {
			t.Errorf("image outputs lost on cancellation: %+v", results)
		}
		return nil, ctx.Err()
	}}
	if err := (agent.Runner{Model: f, Tools: []tools.Tool{tool}}).Run(ctx, "inspect"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error: %v", err)
	}
}
