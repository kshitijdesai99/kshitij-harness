package test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kh/internal/memory"
	"kh/internal/tools"
)

func TestMemoryProvenanceActions(t *testing.T) {
	ctx := context.Background()
	s, err := memory.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const repo = "github.com/me/app"
	base := time.Date(2026, 1, 1, 12, 0, 0, 123456789, time.UTC)
	add := func(scope, kind, key, text string, offset int) memory.Note {
		t.Helper()
		_, n, err := s.Add(ctx, memory.Note{Repo: scope, Kind: kind, Key: key, Text: text,
			Source: "you", Owner: "owner", ContextSummary: "Discussed review layout.", StatedAt: base.Add(time.Duration(offset) * time.Hour)}, memory.Any)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	old := add(repo, "instruction", "layout", "reviewer older layout", 0)
	current := add(repo, "instruction", "layout", "reviewer current layout", 1)
	global := add(memory.Global, "fact", "shared", "reviewer global fact", 2)
	decision := add(repo, "decision", "decision", "reviewer local decision", 3)
	add(repo, "summary", memory.SummaryKey, "reviewer hidden summary", 4)
	foreign := add("github.com/other/private", "instruction", "private", "reviewer secret", 5)
	tool := tools.Memory(s, repo, "owner")
	call := func(in map[string]any) (string, error) {
		t.Helper()
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		return tool.Run(ctx, b)
	}
	must := func(in map[string]any, fragments ...string) string {
		t.Helper()
		out, err := call(in)
		if err != nil {
			t.Fatal(err)
		}
		for _, fragment := range fragments {
			if !strings.Contains(out, fragment) {
				t.Errorf("missing %q in %q", fragment, out)
			}
		}
		return out
	}
	t.Run("search scopes and all non-summary kinds", func(t *testing.T) {
		out := must(map[string]any{"action": "search", "query": "reviewer"}, "reviewer current layout", "reviewer global fact", "reviewer local decision", "stated:")
		for _, hidden := range []string{"older layout", "hidden summary", "secret"} {
			if strings.Contains(out, hidden) {
				t.Errorf("search leaked %q: %s", hidden, out)
			}
		}
		out = must(map[string]any{"action": "search", "scope": "global", "query": "reviewer"}, "reviewer global fact")
		if strings.Contains(out, "layout") || strings.Contains(out, "decision") {
			t.Fatalf("global search included repo notes: %s", out)
		}
	})
	t.Run("inclusive time bounds and order", func(t *testing.T) {
		stamp := global.StatedAt.Format(time.RFC3339Nano)
		out := must(map[string]any{"action": "search", "query": "reviewer", "since": stamp, "until": stamp}, "reviewer global fact")
		if strings.Contains(out, "layout") || strings.Contains(out, "decision") {
			t.Fatal(out)
		}
		for _, order := range []string{"oldest", "newest"} {
			out = must(map[string]any{"action": "search", "query": "reviewer", "order": order})
			first, last := fmt.Sprintf("[memory #%d]", current.ID), fmt.Sprintf("[memory #%d]", decision.ID)
			if order == "newest" {
				first, last = last, first
			}
			if strings.Index(out, first) >= strings.Index(out, last) {
				t.Fatalf("%s ordering: %s", order, out)
			}
		}
	})
	t.Run("get provenance and current state", func(t *testing.T) {
		must(map[string]any{"action": "get", "id": old.ID}, "reviewer older layout", "state: superseded", fmt.Sprintf("current id: #%d", current.ID), "discussion context: Discussed review layout.", "created:", "owner: owner", "source: you", "forgotten: false")
		must(map[string]any{"action": "get", "id": current.ID}, "state: current", fmt.Sprintf("overrides: #%d", old.ID))
		must(map[string]any{"action": "get", "id": global.ID}, "reviewer global fact")
		for _, input := range []map[string]any{
			{"action": "get", "id": foreign.ID},
			{"action": "get", "id": current.ID, "scope": "global"},
			{"action": "get", "id": 99999},
		} {
			if out := must(input); out != "memory not found" {
				t.Fatalf("unauthorized or missing get: %q", out)
			}
		}
	})
	t.Run("history and forgotten state", func(t *testing.T) {
		out := must(map[string]any{"action": "history", "key": "layout"}, "state: superseded", "state: current")
		if strings.Index(out, "current layout") >= strings.Index(out, "older layout") {
			t.Fatal(out)
		}
		stamp := old.StatedAt.Format(time.RFC3339Nano)
		out = must(map[string]any{"action": "history", "key": "layout", "since": stamp, "until": stamp}, "older layout", "state: superseded")
		if strings.Contains(out, "current layout") {
			t.Fatal(out)
		}
		must(map[string]any{"action": "history", "key": "shared"}, "no matching memories")
		must(map[string]any{"action": "history", "key": "shared", "scope": "global"}, "reviewer global fact")
		must(map[string]any{"action": "forget", "key": "layout"}, "forgot layout")
		must(map[string]any{"action": "get", "id": old.ID}, "state: superseded", "current forgotten: true")
		latest, ok, err := s.Latest(ctx, repo, "layout")
		if err != nil || !ok {
			t.Fatalf("latest: %v %v", ok, err)
		}
		must(map[string]any{"action": "get", "id": latest.ID}, "state: current", "forgotten: true")
	})
	t.Run("invalid inputs", func(t *testing.T) {
		for _, input := range []map[string]any{
			{"action": "search", "scope": "other"},
			{"action": "get", "id": foreign.ID, "scope": "github.com/other/private"},
			{"action": "get"}, {"action": "get", "id": -1}, {"action": "get", "id": 1.5},
			{"action": "history"}, {"action": "remember"},
			{"action": "search", "since": "yesterday"},
			{"action": "search", "until": "2026-01-01"},
			{"action": "search", "since": "2026-02-01T00:00:00Z", "until": "2026-01-01T00:00:00Z"},
			{"action": "search", "order": "random"},
			{"action": "history", "key": "layout", "limit": 0},
			{"action": "history", "key": "layout", "limit": 51},
		} {
			if out, err := call(input); err == nil {
				t.Errorf("accepted invalid input %v: %q", input, out)
			}
		}
	})
}

func TestMemoryHistoryTruncation(t *testing.T) {
	ctx := context.Background()
	s, err := memory.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 55; i++ {
		_, _, err := s.Add(ctx, memory.Note{Repo: "repo", Kind: "fact", Key: "topic", Text: fmt.Sprintf("version %02d", i), Source: "you", StatedAt: base.Add(time.Duration(i) * time.Second)}, memory.Any)
		if err != nil {
			t.Fatal(err)
		}
	}
	tool := tools.Memory(s, "repo", "owner")
	for _, tc := range []struct {
		input                   string
		count                   int
		first, last, truncation string
	}{
		{`{"action":"history","key":"topic"}`, 20, "version 54", "version 35", "showing 20 of 55"},
		{`{"action":"history","key":"topic","limit":50,"order":"oldest"}`, 50, "version 00", "version 49", "showing 50 of 55"},
		{`{"action":"history","key":"topic","since":"2026-01-01T00:00:00Z","until":"2026-01-01T00:00:04Z","order":"oldest"}`, 5, "version 00", "version 04", ""},
	} {
		out, err := tool.Run(ctx, json.RawMessage(tc.input))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(out, "[memory #") != tc.count || !strings.Contains(out, tc.first) || !strings.Contains(out, tc.last) || strings.Index(out, tc.first) > strings.Index(out, tc.last) {
			t.Fatalf("unexpected bounded history: %s", out)
		}
		if tc.truncation != "" && !strings.Contains(out, tc.truncation) || tc.truncation == "" && strings.Contains(out, "truncated") {
			t.Fatalf("unexpected truncation message: %s", out)
		}
	}
}
