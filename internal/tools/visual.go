package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"

	"kh/internal/config"
	"kh/internal/visual"
)

// Visual exposes no shell, arbitrary executable, or caller-controlled environment.
// The same session access object is shared with edit and bash.
func Visual(manager *visual.Manager, cfg config.Config, approval Approver, access *WriteAccess) Tool {
	return Tool{
		Name:        "visual",
		Description: fmt.Sprintf("Create/start/list/stop session-owned marimo notebooks. create makes a skeleton; edit its returned path, then start. The rendered app runs immediately and watches notebook edits. name: 1-64 letters/digits/_/-. start optional port (1024-65535); default free localhost port, no browser opens. Only this process's servers are listed/stopped; servers survive turn cancellation and tmux detach, stop on kh process exit. Artifacts persist at %s; shared dependencies/cache %s. Keep generated files here, not the repo. uv must be installed on PATH. Dependencies go in notebook PEP 723 script metadata; uv provisions them before marimo starts. Normal execution/folder approval and shell sandbox apply. Never start unmanaged servers through bash.", manager.Root(), manager.Cache()),
		Params: map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"create", "start", "list", "stop"}},
			"name":   map[string]any{"type": "string", "description": "Notebook name, required except for list."},
			"port":   map[string]any{"type": "integer", "description": "Optional localhost port for start."},
		}, Required: []string{"action"},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			var request struct {
				Action string `json:"action"`
				Name   string `json:"name"`
				Port   int    `json:"port"`
			}
			if err := json.Unmarshal(input, &request); err != nil {
				return "", err
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if request.Action != "start" && request.Port != 0 {
				return "", fmt.Errorf("port is only valid for start")
			}
			var value any
			switch request.Action {
			case "list":
				value = map[string]any{"artifacts": manager.Root(), "cache": manager.Cache(), "servers": manager.List()}
			case "create", "start":
				if access == nil {
					return "", fmt.Errorf("visual requires session write access")
				}
				dirs := []string{manager.Root()}
				if request.Action == "start" {
					dirs = append(dirs, manager.Cache())
				}
				if err := access.Require(ctx, dirs); err != nil {
					return "", err
				}
				if request.Action == "create" {
					server, err := manager.Create(request.Name)
					if err != nil {
						return "", err
					}
					value = server
					break
				}
				allowed := access.Auto()
				if approval != nil {
					var err error
					allowed, err = approval.Approve(ctx, fmt.Sprintf("Start session-owned marimo notebook %q (localhost port %d, 0=automatic)", request.Name, request.Port), allowed)
					if err != nil {
						return "", err
					}
				}
				if err := ctx.Err(); err != nil {
					return "", err
				}
				if !allowed {
					return "", fmt.Errorf("user said no")
				}
				server, err := manager.Start(ctx, request.Name, request.Port, func(argv []string) ([]string, error) {
					if !cfg.Sandbox || runtime.GOOS != "darwin" {
						return argv, nil
					}
					dirs, err := access.SandboxDirectories()
					if err != nil {
						return nil, err
					}
					return append(sandboxProfile(dirs, false), argv...), nil
				})
				if err != nil {
					return "", err
				}
				value = server
			case "stop":
				if err := manager.Stop(request.Name); err != nil {
					return "", err
				}
				value = map[string]any{"stopped": request.Name, "artifacts": manager.Root()}
			default:
				return "", fmt.Errorf("visual action must be create, start, list, or stop")
			}
			data, err := json.Marshal(value)
			return string(data), err
		},
	}
}
