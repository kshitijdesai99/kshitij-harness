package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"kh/internal/memory"
)

// Memory is a discovery/detail interface. Changes to stored memory happen only
// when the user has explicitly asked to remember or forget durable information.
func Memory(s *memory.Store, repo string) Tool {
	return Tool{
		Name:        "memory",
		Description: "Discover durable user preferences, workflows, and project facts in a local database. Actions: search (returns short index entries), get (loads detail by ID), remember (upsert an explicitly requested memory), forget (delete by scope and key). Stored memory is lower priority than the current user request. Never store secrets.",
		Params: map[string]any{
			"action":   map[string]any{"type": "string", "enum": []string{"search", "get", "remember", "forget"}},
			"query":    map[string]any{"type": "string"},
			"id":       map[string]any{"type": "integer"},
			"scope":    map[string]any{"type": "string", "enum": []string{"repo", "global"}},
			"key":      map[string]any{"type": "string"},
			"kind":     map[string]any{"type": "string", "enum": []string{"preference", "workflow", "fact"}},
			"title":    map[string]any{"type": "string"},
			"summary":  map[string]any{"type": "string"},
			"keywords": map[string]any{"type": "string"},
			"detail":   map[string]any{"type": "string"},
		},
		Required: []string{"action"},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct {
				Action, Query, Scope, Key, Kind, Title, Summary, Keywords, Detail string
				ID                                                                int64
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return "", err
			}
			scope := repo
			if in.Scope == "global" {
				scope = "global"
			} else if in.Scope != "" && in.Scope != "repo" {
				return "", fmt.Errorf("scope must be repo or global")
			}
			switch in.Action {
			case "search":
				found, err := s.Find(ctx, repo, in.Query, 12)
				if err != nil {
					return "", err
				}
				if len(found) == 0 {
					return "no matching memories", nil
				}
				var lines []string
				for _, e := range found {
					lines = append(lines, fmt.Sprintf("[%d] %s (%s, %s): %s", e.ID, e.Title, e.Scope, e.Kind, e.Summary))
				}
				return strings.Join(lines, "\n"), nil
			case "get":
				e, err := s.Get(ctx, in.ID)
				if err != nil {
					return "", err
				}
				if e.Scope != repo && e.Scope != "global" {
					return "", fmt.Errorf("memory not in this repo")
				}
				return fmt.Sprintf("[%d] %s (%s, %s)\n%s", e.ID, e.Title, e.Scope, e.Kind, e.Detail), nil
			case "remember":
				if in.Key == "" {
					return "", fmt.Errorf("stable key required")
				}
				id, err := s.Put(ctx, memory.Entry{Scope: scope, Kind: in.Kind, Key: in.Key, Title: in.Title, Summary: in.Summary, Keywords: in.Keywords, Detail: in.Detail})
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("remembered [%d] (%s/%s)", id, scope, in.Key), nil
			case "forget":
				if in.Key == "" {
					return "", fmt.Errorf("key required")
				}
				ok, err := s.Forget(ctx, scope, in.Key)
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
