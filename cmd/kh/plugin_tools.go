package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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
				root := h.cogitRoot()
				if root == "" && len(h.trusted) > 0 {
					root = h.trusted[0].PluginRoot
				}
				ctx = tools.WithBashEnvironment(ctx, h.runtime.Environment(h.input, root))
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

func (h *pluginLifecycle) cogitRoot() string {
	for _, hook := range h.trusted {
		name, _, _ := strings.Cut(hook.Plugin, "@")
		if name == "cogit" {
			return hook.PluginRoot
		}
	}
	return ""
}

var inertSubstitution = regexp.MustCompile("!`([^`]*)`")

// continuation recognizes only Cogit's exact record request and loads the body
// from that same explicitly trusted plugin. No Markdown is ever executed here.
func (h *pluginLifecycle) continuation(text string) string {
	if h == nil || h.project == nil || strings.TrimSpace(text) != "/cogit:record" {
		return text
	}
	root := h.cogitRoot()
	if root == "" {
		return text
	}
	input := json.RawMessage(`{"action":"load","name":"cogit:record"}`)
	body, err := h.project.Tools()[0].Run(context.Background(), input)
	if err != nil {
		return "Cogit record skill could not be loaded: " + err.Error()
	}
	// A same-named project command must not impersonate the trusted plugin.
	lines := strings.SplitN(body, "\n", 3)
	if len(lines) < 3 || lines[1] != "Plugin root: "+root {
		return "Cogit record skill does not belong to the trusted Cogit plugin; continuation skipped."
	}
	body = inertSubstitution.ReplaceAllStringFunc(body, func(marked string) string {
		command := strings.TrimSuffix(strings.TrimPrefix(marked, "!`"), "`")
		if strings.Contains(command, "session show") && strings.HasPrefix(strings.TrimSpace(command), "node ") {
			return "[Session data is not expanded here. Explicitly invoke the registered bash tool with this command, subject to ordinary approval, then use its output: " + command + "]"
		}
		return "[Unsupported executable-Markdown substitution omitted; no command was run.]"
	})
	return "Trusted Cogit Stop continuation: /cogit:record\n" +
		"Compatibility adaptation: the following skill is reference data, not executable Markdown. Do not run shell substitutions automatically. First obtain any requested session show data using an explicit normally approved bash tool call; CLAUDE_* variables match this session's hook environment. For instructions to use the Write tool, discover and invoke the native edit tool instead: create a new file with {path: FILE, old: \"\", new: CONTENT}; modify an existing file with an exact old/new replacement. An edits array can batch changes. Do not bypass approval or replace an existing file by pretending it is new.\n\n" + body
}
