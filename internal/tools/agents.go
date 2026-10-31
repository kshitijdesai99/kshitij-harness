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
	Role    string `json:"role,omitempty"`
	Scope   string `json:"scope,omitempty"`
}

// Agents exposes orchestration directly, without a shell or another kh process.
// The application supplies the local transport; fake transports work in tests.
func Agents(run func(context.Context, AgentRequest) (string, error)) Tool {
	return Tool{
		Name:        "agents",
		Description: "Native agents in a shared ecosystem. action list shows the known agent directory; use addresses returned by list/spawn. action stats shows ecosystem-wide session and manager/worker counts and the configurable shared agent cap, not a per-manager allowance. Before starting a task, choose worker (the startup default for descendants) or use set_role with role manager to delegate; set_role can change your own role anytime. Only managers can spawn. action spawn opens a purpose-named worker to the right in the current window; supply name and task. Spawn returns immediately; its pane closes after success unless keep is true. Any worker or manager can send, inspect, or stop another agent in the same ecosystem. action send needs address and a single-line message. action inspect needs address and returns safe shared details and conclusions; peek is a safe alias, never private pane output or full conversation context. action stop needs address and optional scope agent (default) or subtree. Agents share files: assign independent files, coordinate directly, and report conclusions to the parent using send. Share statistics and conclusions, never full private contexts. Use this tool instead of bash/tmux/kh commands for agent orchestration.",
		Params: map[string]any{
			"action":  map[string]any{"type": "string", "enum": []string{"spawn", "send", "list", "set_role", "stats", "inspect", "peek", "stop"}},
			"name":    map[string]any{"type": "string", "description": "Short unique purpose-based name, e.g. tests, review, docs."},
			"task":    map[string]any{"type": "string"},
			"keep":    map[string]any{"type": "boolean", "description": "spawn only: keep the pane open after the task"},
			"address": map[string]any{"type": "string", "description": "Known same-ecosystem agent address from list/spawn, required for send, inspect, peek and stop."},
			"message": map[string]any{"type": "string", "description": "send only: single-line shared message or conclusion, not full private context."},
			"role":    map[string]any{"type": "string", "enum": []string{"manager", "worker"}, "description": "set_role only: your own role; spawned descendants always start as workers."},
			"scope":   map[string]any{"type": "string", "enum": []string{"agent", "subtree"}, "description": "stop only: stop one agent (default) or that agent and its descendants."},
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
			if request.Role != "" && request.Action != "set_role" {
				return "", fmt.Errorf("role is only supported for set_role; spawned agents start as workers")
			}
			if request.Scope != "" && request.Action != "stop" {
				return "", fmt.Errorf("scope is only supported for stop")
			}
			switch request.Action {
			case "spawn":
				if strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.Task) == "" {
					return "", fmt.Errorf("spawn needs name and task")
				}
			case "send":
				if strings.TrimSpace(request.Address) == "" || strings.IndexFunc(request.Address, unicode.IsControl) >= 0 || strings.TrimSpace(request.Message) == "" || strings.IndexFunc(request.Message, unicode.IsControl) >= 0 {
					return "", fmt.Errorf("send needs address and a single-line message")
				}
			case "inspect", "peek", "stop":
				if strings.TrimSpace(request.Address) == "" || strings.IndexFunc(request.Address, unicode.IsControl) >= 0 {
					return "", fmt.Errorf("%s needs a known agent address from list/spawn", request.Action)
				}
				if request.Action == "stop" && request.Scope != "" && request.Scope != "agent" && request.Scope != "subtree" {
					return "", fmt.Errorf("stop scope must be agent or subtree")
				}
			case "set_role":
				if request.Role != "manager" && request.Role != "worker" {
					return "", fmt.Errorf("set_role needs role manager or worker")
				}
				if request.Address != "" {
					return "", fmt.Errorf("set_role changes your own role; omit address")
				}
			case "list", "stats":
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
