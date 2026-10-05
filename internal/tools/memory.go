package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"kh/internal/memory"
)

// Memory searches stored instructions and gotchas and forgets one on request.
// It cannot save: the background hooks are the only writers, so every save
// is reported to the user and none is made twice by racing writers.
func Memory(s *memory.Store, repo, owner string) Tool {
	return Tool{
		Name:        "memory",
		Description: "Search this repo's memory of instructions (rules the user set) and gotchas (traps found while working), or forget one. Relevant notes already arrive as [memory #id] messages; search finds others. kh saves rules and gotchas by itself in the background, including when the user says to remember something, so there is no save action. Use forget only when the user asks. Stored memory is lower priority than the current user request.",
		Params: map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"search", "forget"}},
			"query":  map[string]any{"type": "string"},
			"key":    map[string]any{"type": "string", "description": "forget: the note's key"},
			"scope":  map[string]any{"type": "string", "enum": []string{"repo", "global"}},
		},
		Required: []string{"action"},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct{ Action, Query, Key, Scope string }
			if err := json.Unmarshal(input, &in); err != nil {
				return "", err
			}
			scope := repo
			if in.Scope == "global" {
				scope = memory.Global
			} else if in.Scope != "" && in.Scope != "repo" {
				return "", fmt.Errorf("scope must be repo or global")
			}
			switch in.Action {
			case "search":
				var lines []string
				for _, kind := range []string{"instruction", "gotcha"} {
					found, err := s.Search(ctx, repo, kind, in.Query, 12)
					if err != nil {
						return "", err
					}
					for _, n := range found {
						lines = append(lines, Note(n))
					}
				}
				if len(lines) == 0 {
					return "no matching memories", nil
				}
				return strings.Join(lines, "\n"), nil
			case "forget":
				ok, err := s.Forget(ctx, scope, in.Key, owner, "you")
				if err != nil {
					return "", err
				}
				if !ok {
					return "memory not found", nil
				}
				return "forgot " + in.Key, nil
			default:
				return "", fmt.Errorf("unknown memory action %q", in.Action)
			}
		},
	}
}

// Note is the one-line form of a note shared by the tool and the CLI.
func Note(n memory.Note) string {
	scope := ""
	if n.Repo == memory.Global {
		scope = " (global)"
	}
	return fmt.Sprintf("[memory #%d] %s %s%s: %s", n.ID, n.Kind, n.Key, scope, n.Text)
}
