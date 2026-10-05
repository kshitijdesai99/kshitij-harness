package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// AgentRequest is independent of the tmux transport and the command-line UI.
type AgentRequest struct {
	Action  string `json:"action"`
	Name    string `json:"name,omitempty"`
	Task    string `json:"task,omitempty"`
	Keep    bool   `json:"keep,omitempty"`
	Address string `json:"address,omitempty"`
	Message string `json:"message,omitempty"`
}

// Agents exposes orchestration directly, without a shell or another kh process.
// The application supplies the local transport; fake transports work in tests.
func Agents(run func(context.Context, AgentRequest) (string, error)) Tool {
	return Tool{
		Name:        "agents",
		Description: "Native tmux agents. action spawn opens a purpose-named agent to the right in the current window; supply name and task. action send delivers a single-line message directly to any address (kh:main or kh:<name>), including peers; supply address and message. action peek reads recent output at address. action list shows addresses and states. Spawn returns immediately, not when the task finishes. A spawned agent's pane closes when its task is done (it stays open on failure); set keep to leave it open for follow-up messages. Agents share files: assign independent files, coordinate directly, and report results to the parent using send. Use this tool instead of bash/tmux/kh commands for agent orchestration.",
		Params: map[string]any{
			"action":  map[string]any{"type": "string", "enum": []string{"spawn", "send", "peek", "list"}},
			"name":    map[string]any{"type": "string", "description": "Short unique purpose-based name, e.g. tests, review, docs."},
			"task":    map[string]any{"type": "string"},
			"keep":    map[string]any{"type": "boolean", "description": "spawn only: keep the pane open after the task"},
			"address": map[string]any{"type": "string", "description": "Agent address from list/spawn, e.g. kh:review."},
			"message": map[string]any{"type": "string"},
		},
		Required: []string{"action"},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var request AgentRequest
			if err := json.Unmarshal(input, &request); err != nil {
				return "", fmt.Errorf("invalid agent request: %w", err)
			}
			switch request.Action {
			case "spawn":
				if strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.Task) == "" {
					return "", fmt.Errorf("spawn needs name and task")
				}
			case "send":
				if request.Address == "" || strings.TrimSpace(request.Message) == "" || strings.IndexFunc(request.Message, unicode.IsControl) >= 0 {
					return "", fmt.Errorf("send needs address and a single-line message")
				}
			case "peek":
				if request.Address == "" {
					return "", fmt.Errorf("peek needs address")
				}
			case "list":
			default:
				return "", fmt.Errorf("unknown agent action: %q", request.Action)
			}
			if run == nil {
				return "", fmt.Errorf("agent transport unavailable")
			}
			return run(ctx, request)
		},
	}
}
