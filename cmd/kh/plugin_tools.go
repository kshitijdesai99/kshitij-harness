package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"kh/internal/pluginhooks"
	"kh/internal/tools"
)

// Decorate the originals before constructing the registry: tool_invoke and
// historical direct calls then reach the same approval/hook boundary once.
func pluginTools(h *pluginLifecycle, ts []tools.Tool, approval tools.Approver, policy *tools.ApprovalPolicy) []tools.Tool {
	if h == nil {
		return ts
	}
	out := append([]tools.Tool(nil), ts...)
	for i, t := range out {
		switch t.Name {
		case "bash":
			original := t.Run
			out[i].Run = func(ctx context.Context, input json.RawMessage) (string, error) {
				ctx = tools.WithBashEnvironment(ctx, h.runtime.Environment(h.input, ""))
				return original(ctx, input)
			}
		case "edit":
			hasPreToolUse := false
			for _, hook := range h.trusted {
				hasPreToolUse = hasPreToolUse || hook.Event == pluginhooks.PreToolUse
			}
			if !hasPreToolUse {
				continue
			}
			out[i].Run = func(ctx context.Context, input json.RawMessage) (string, error) {
				var extra []string
				decorated := tools.WithEditHook(t, func(ctx context.Context, operations []tools.EditOperation, batch bool) error {
					if approval == nil {
						return fmt.Errorf("plugin edit hooks require an approver")
					}
					paths := make([]string, 0, len(operations))
					for _, op := range operations {
						paths = append(paths, op.Path)
					}
					allowed, err := approval.Approve(ctx, "Edit files and run trusted plugin PreToolUse hooks: "+strings.Join(paths, ", "), policy != nil && policy.Auto())
					if ctx.Err() != nil {
						return ctx.Err()
					}
					if err != nil {
						return err
					}
					if !allowed {
						return fmt.Errorf("user said no")
					}
					seen := make(map[string]bool)
					for _, op := range operations {
						if seen[op.Path] {
							continue
						}
						seen[op.Path] = true
						name := "Edit"
						if batch {
							name = "MultiEdit"
						} else if op.Old == "" {
							name = "Write"
						}
						data, _ := json.Marshal(map[string]string{"file_path": op.Path})
						if text := h.run(ctx, pluginhooks.PreToolUse, "", name, data, false); text != "" {
							extra = append(extra, text)
						}
						if ctx.Err() != nil {
							return ctx.Err()
						}
					}
					return nil
				})
				result, err := decorated.Run(ctx, input)
				if len(extra) > 0 {
					result += "\n\nPlugin PreToolUse context (reference data):\n" + strings.Join(extra, "\n\n")
				}
				return result, err
			}
		}
	}
	return out
}
