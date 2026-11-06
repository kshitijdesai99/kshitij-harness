package tools

import (
	"context"
	"encoding/json"
)

// EditOperation describes an operation from a fully preflighted edit request.
// An empty Old creates a new file; Batch preserves the caller's input form.
type EditOperation struct {
	Path string
	Old  string
	New  string
}

// EditHook runs after complete preflight and before any write. An error aborts
// the whole request. It may request approval before invoking external hooks.
type EditHook func(context.Context, []EditOperation, bool) error

type editHookKey struct{}

// WithEditHook decorates the native edit tool without changing its schema. The
// callback is reached only by the editor's validated write path, including when
// a registry facade dispatches to the decorated tool.
func WithEditHook(tool Tool, hook EditHook) Tool {
	original := tool.Run
	tool.Run = func(ctx context.Context, input json.RawMessage) (string, error) {
		return original(context.WithValue(ctx, editHookKey{}, hook), input)
	}
	return tool
}
