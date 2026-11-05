package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"kh/internal/delegation"
)

// AgentRequest is independent of the tmux transport and the command-line UI.
type AgentRequest struct {
	Action  string              `json:"action"`
	Name    string              `json:"name,omitempty"`
	Task    string              `json:"task,omitempty"`
	Keep    bool                `json:"keep,omitempty"`
	Reuse   bool                `json:"reuse,omitempty"`
	Handoff *delegation.Handoff `json:"handoff,omitempty"`
	Address string              `json:"address,omitempty"`
	Message string              `json:"message,omitempty"`
	Role    string              `json:"role,omitempty"`
	Scope   string              `json:"scope,omitempty"`
}

// Agents exposes orchestration directly, without a shell or another kh process.
// The application supplies the local transport; fake transports work in tests.
func Agents(run func(context.Context, AgentRequest) (string, error)) Tool {
	return Tool{
		Name:        "agents",
		Description: "Native agents in a shared ecosystem. action list shows the known agent directory; use addresses returned by list/spawn. action stats shows ecosystem-wide session and manager/worker counts and the configurable shared agent cap, not a per-manager allowance. Before starting a task, workers execute their assigned scope; descendants start as workers and may use set_role with role manager to take on coordination. Every manager delegates implementation and integration edits, routine tests, builds, installation and restart execution to workers, then coordinates and assesses evidence. On promotion, hand existing execution tasks to workers before continuing. Do not switch roles to bypass delegation or become a worker while managing live descendants. No active workers or full capacity is not permission for a manager to execute the work; retain orchestration and arrange worker execution when capacity permits. Managers delegate substantial investigation and source/document exploration. Once delegated, do not independently repeat broad exploration; use concise worker conclusions and targeted follow-ups first. Managers may answer ordinary informational questions directly, do initial scoping, and make narrow reads for specific missing evidence, conflicts or verification. Workers return conclusions, exact references, checks and uncertainty, not raw history or bulk source. Only managers can spawn. action spawn opens a purpose-named worker to the right in the current window; supply name and task. Spawn returns immediately; its pane closes after success unless keep or reuse is true. Prefer spawn reuse:true with a compact structured handoff for short follow-up and routine tasks. action assign needs address and handoff; it starts a fresh isolated task on an idle reusable worker, never inheriting old or parent conversation. send remains same-task steering, not a fresh assignment. Any worker or manager can send, inspect, or stop another agent in the same ecosystem. action send needs address and a single-line message. action inspect needs address and returns safe shared details and conclusions; peek is a safe alias, never private pane output or full conversation context. action stop needs address and optional scope agent (default) or subtree. Agents share files: assign independent files, coordinate directly, and report conclusions to the parent using send. Share statistics and conclusions, never full private contexts. Use this tool instead of bash/tmux/kh commands for agent orchestration.",
		Params: map[string]any{
			"action":  map[string]any{"type": "string", "enum": []string{"spawn", "assign", "send", "list", "set_role", "stats", "inspect", "peek", "stop"}},
			"name":    map[string]any{"type": "string", "description": "Short unique purpose-based name, e.g. tests, review, docs."},
			"task":    map[string]any{"type": "string"},
			"reuse":   map[string]any{"type": "boolean", "description": "spawn only: retain an idle task-fresh worker; prefer for short follow-ups and routine work. Default false."},
			"handoff": map[string]any{"type": "object", "description": "Compact explicit task handoff: version:1, task, canonical absolute directory, optional scope/constraints/expected_output/verification/foundation_id/workflow. No parent conversation."},
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
			if request.Reuse && request.Action != "spawn" {
				return "", fmt.Errorf("reuse is only supported for spawn")
			}
			if request.Handoff != nil {
				if request.Action != "spawn" && request.Action != "assign" {
					return "", fmt.Errorf("handoff is only supported for spawn or assign")
				}
				if err := request.Handoff.Validate(); err != nil {
					return "", err
				}
			}
			if request.Role != "" && request.Action != "set_role" {
				return "", fmt.Errorf("role is only supported for set_role; spawned agents start as workers")
			}
			if request.Scope != "" && request.Action != "stop" {
				return "", fmt.Errorf("scope is only supported for stop")
			}
			switch request.Action {
			case "spawn":
				if request.Keep && request.Handoff != nil {
					return "", fmt.Errorf("keep with structured handoff is unsupported; use reuse for task-fresh follow-ups")
				}
				if request.Keep && request.Reuse {
					return "", fmt.Errorf("keep and reuse are mutually exclusive")
				}
				if strings.TrimSpace(request.Name) == "" || (strings.TrimSpace(request.Task) == "" && request.Handoff == nil) {
					return "", fmt.Errorf("spawn needs name and task")
				}
			case "assign":
				if strings.TrimSpace(request.Address) == "" || strings.IndexFunc(request.Address, unicode.IsControl) >= 0 || request.Handoff == nil {
					return "", fmt.Errorf("assign needs address and structured handoff")
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
