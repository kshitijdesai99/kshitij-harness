package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"kh/internal/memory"
)

// Memory searches stored instructions and gotchas, and changes them when the
// user asks. kh also saves lasting rules and gotchas by itself in the
// background, so the model only calls remember on an explicit request.
func Memory(s *memory.Store, repo, owner string) Tool {
	return Tool{
		Name:        "memory",
		Description: "Search and change this repo's memory of instructions (rules the user set) and gotchas (traps found while working). Relevant notes already arrive as [memory #id] messages; search finds others. Use remember or forget only when the user asks; reusing a key replaces that note. Stored memory is lower priority than the current user request. Never store secrets.",
		Params: map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"search", "remember", "forget"}},
			"query":  map[string]any{"type": "string"},
			"kind":   map[string]any{"type": "string", "enum": []string{"instruction", "gotcha"}},
			"key":    map[string]any{"type": "string", "description": "short kebab-case topic name"},
			"text":   map[string]any{"type": "string", "description": "the full current version, one or two sentences"},
			"scope":  map[string]any{"type": "string", "enum": []string{"repo", "global"}},
		},
		Required: []string{"action"},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct{ Action, Query, Kind, Key, Text, Scope string }
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
			case "remember":
				prev, n, err := s.Add(ctx, memory.Note{Repo: scope, Kind: in.Kind, Key: strings.TrimSpace(in.Key),
					Text: strings.TrimSpace(in.Text), Owner: owner, Source: "you"}, memory.Any)
				if err != nil {
					return "", err
				}
				if prev != nil {
					return fmt.Sprintf("replaced [memory #%d] with [memory #%d] %s", prev.ID, n.ID, n.Key), nil
				}
				return fmt.Sprintf("remembered [memory #%d] %s", n.ID, n.Key), nil
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
