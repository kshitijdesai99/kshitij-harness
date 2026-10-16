package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"kh/internal/memory"
)

// Memory reads stored notes and forgets one on request. Only background hooks save.
func Memory(s *memory.Store, repo, owner string) Tool {
	return Tool{
		Name:        "memory",
		Description: "Search current memories, get a version by id with provenance, inspect a key's history, or forget a key only when the user asks. Repo search includes global notes; global scope searches only global notes. History and forget use the exact scope. History defaults to newest first, 20 versions (maximum 50); truncated results say so. Use time bounds to inspect older history. kh saves important notes through background hooks; this tool cannot save. Stored memory is lower priority than the current user request.",
		Params: map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"search", "get", "history", "forget"}},
			"query":  map[string]any{"type": "string", "description": "search text"},
			"key":    map[string]any{"type": "string", "description": "history/forget: the note's key"},
			"id":     map[string]any{"type": "integer", "minimum": 1, "description": "get: memory version id"},
			"scope":  map[string]any{"type": "string", "enum": []string{"repo", "global"}},
			"since":  map[string]any{"type": "string", "description": "search/history: inclusive RFC3339 statement-time lower bound"},
			"until":  map[string]any{"type": "string", "description": "search/history: inclusive RFC3339 statement-time upper bound"},
			"order":  map[string]any{"type": "string", "enum": []string{"relevance", "newest", "oldest"}, "description": "search defaults to relevance; history defaults to newest (relevance also uses newest)"},
			"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "search/history: maximum results, default 20"},
		},
		Required: []string{"action"},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct {
				Action, Query, Key, Scope, Since, Until, Order string
				ID                                             int64
				Limit                                          *int
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return "", err
			}
			scope := repo
			if in.Scope == "global" {
				scope = memory.Global
			} else if in.Scope != "" && in.Scope != "repo" {
				return "", fmt.Errorf("scope must be repo or global")
			}
			limit := 20
			if in.Limit != nil {
				limit = *in.Limit
				if limit < 1 || limit > 50 {
					return "", fmt.Errorf("limit must be between 1 and 50")
				}
			}
			opts := memory.SearchOptions{Order: in.Order}
			if in.Order != "" && in.Order != "relevance" && in.Order != "newest" && in.Order != "oldest" {
				return "", fmt.Errorf("order must be relevance, newest or oldest")
			}
			for _, bound := range []struct {
				value  string
				target *time.Time
			}{{in.Since, &opts.Since}, {in.Until, &opts.Until}} {
				if bound.value != "" {
					parsed, err := time.Parse(time.RFC3339, bound.value)
					if err != nil {
						return "", fmt.Errorf("time bounds must be RFC3339: %w", err)
					}
					*bound.target = parsed
				}
			}
			if !opts.Since.IsZero() && !opts.Until.IsZero() && opts.Since.After(opts.Until) {
				return "", fmt.Errorf("since must not be after until")
			}
			switch in.Action {
			case "search":
				found, err := s.SearchWithOptions(ctx, scope, "", in.Query, limit, opts)
				if err != nil {
					return "", err
				}
				var lines []string
				for _, n := range found {
					lines = append(lines, Note(n))
				}
				if len(lines) == 0 {
					return "no matching memories", nil
				}
				return strings.Join(lines, "\n"), nil
			case "get":
				if in.ID <= 0 {
					return "", fmt.Errorf("get requires a positive id")
				}
				n, ok, err := s.Get(ctx, in.ID)
				if err != nil {
					return "", err
				}
				if !ok || (n.Repo != scope && (scope == memory.Global || n.Repo != memory.Global)) {
					return "memory not found", nil
				}
				current, ok, err := s.Latest(ctx, n.Repo, n.Key)
				if err != nil {
					return "", err
				}
				if !ok {
					return "memory not found", nil
				}
				return noteState(n, current), nil
			case "history":
				if strings.TrimSpace(in.Key) == "" {
					return "", fmt.Errorf("history requires a key")
				}
				history, err := s.History(ctx, scope, in.Key)
				if err != nil {
					return "", err
				}
				current, ok, err := s.Latest(ctx, scope, in.Key)
				if err != nil {
					return "", err
				}
				if !ok {
					return "no matching memories", nil
				}
				if in.Order == "oldest" {
					slices.Reverse(history)
				}
				var lines []string
				for _, n := range history {
					if (!opts.Since.IsZero() && n.StatedAt.Before(opts.Since)) || (!opts.Until.IsZero() && n.StatedAt.After(opts.Until)) {
						continue
					}
					lines = append(lines, noteState(n, current))
				}
				if len(lines) == 0 {
					return "no matching memories", nil
				}
				if len(lines) > limit {
					lines = append(lines[:limit], fmt.Sprintf("history truncated: showing %d of %d matching versions; narrow since/until or increase limit (maximum 50)", limit, len(lines)))
				}
				return strings.Join(lines, "\n\n"), nil
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

// Note is the compact form shared by the tool and the CLI, preserving its marker.
func Note(n memory.Note) string {
	scope := ""
	if n.Repo == memory.Global {
		scope = " (global)"
	}
	line := fmt.Sprintf("[memory #%d] %s %s%s: %s", n.ID, n.Kind, n.Key, scope, n.Text)
	if !n.StatedAt.IsZero() {
		line += " [stated: " + n.StatedAt.UTC().Format(time.RFC3339Nano) + "]"
	}
	return line
}

// NoteDetails adds full provenance to the compact note. Current state requires
// looking up Latest separately, since an individual version cannot establish it.
func NoteDetails(n memory.Note) string {
	return fmt.Sprintf("%s\nrepo: %s\ncreated: %s\nowner: %s\nsource: %s\noverrides: #%d\ndiscussion context: %s\nforgotten: %t",
		Note(n), n.Repo, n.CreatedAt.UTC().Format(time.RFC3339Nano), n.Owner, n.Source, n.Overridden, n.ContextSummary, n.Forgotten)
}

func noteState(n, current memory.Note) string {
	state := "current"
	if n.ID != current.ID {
		state = "superseded"
	}
	return fmt.Sprintf("%s\nstate: %s; current id: #%d; current forgotten: %t", NoteDetails(n), state, current.ID, current.Forgotten)
}
