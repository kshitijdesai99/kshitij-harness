package test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"kh/internal/memory"
	"kh/internal/tools"
)

func TestMemoryTool(t *testing.T) {
	s, err := memory.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, repo := context.Background(), "github.com/me/app"
	for _, text := range []string{"Show reviewers in the right-hand pane.", "Show reviewers below the chat."} {
		if _, _, err := s.Add(ctx, memory.Note{Repo: repo, Kind: "instruction", Key: "review-panes", Text: text, Source: "pre-hook"}, memory.Any); err != nil {
			t.Fatal(err)
		}
	}
	tool := tools.Memory(s, repo, "me@example.com")
	call := func(in map[string]any) (string, error) {
		b, _ := json.Marshal(in)
		return tool.Run(ctx, b)
	}
	list, err := call(map[string]any{"action": "search", "query": "reviewers pane"})
	if err != nil || !strings.Contains(list, "below the chat") || strings.Contains(list, "right-hand") {
		t.Fatalf("search should show only the current version: %q %v", list, err)
	}
	// Only the background hooks save, so every save is reported once.
	if _, err := call(map[string]any{"action": "remember", "kind": "instruction", "key": "x", "text": "y"}); err == nil {
		t.Fatal("tool can still save")
	}
	if out, err := call(map[string]any{"action": "forget", "key": "review-panes"}); err != nil || out != "forgot review-panes" {
		t.Fatalf("forget: %q %v", out, err)
	}
	if hist, _ := s.History(ctx, repo, "review-panes"); len(hist) != 3 || hist[0].Owner != "me@example.com" || hist[0].Source != "you" {
		t.Fatalf("forget row: %+v", hist)
	}
	if out, _ := call(map[string]any{"action": "search", "query": "reviewers"}); out != "no matching memories" {
		t.Fatalf("forgotten note still found: %q", out)
	}
}
