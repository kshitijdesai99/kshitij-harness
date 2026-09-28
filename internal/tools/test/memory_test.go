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
	tool := tools.Memory(s, "repo:/project")
	call := func(in map[string]any) (string, error) {
		b, _ := json.Marshal(in)
		return tool.Run(context.Background(), b)
	}
	if _, err := call(map[string]any{"action": "remember", "scope": "repo", "kind": "preference", "key": "tmux", "title": "Visible tmux reviews", "summary": "Show reviews on right", "keywords": "claude codex review panes", "detail": "Put Claude above Codex."}); err != nil {
		t.Fatal(err)
	}
	list, err := call(map[string]any{"action": "search", "query": "codex review"})
	if err != nil || !strings.Contains(list, "Visible tmux reviews") || strings.Contains(list, "Put Claude above Codex") {
		t.Fatalf("discovery: %q %v", list, err)
	}
	entries, err := s.Find(context.Background(), "repo:/project", "codex", 1)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := call(map[string]any{"action": "get", "id": entries[0].ID})
	if err != nil || !strings.Contains(detail, "Put Claude above Codex") {
		t.Fatalf("get: %q %v", detail, err)
	}
	if _, err := call(map[string]any{"action": "forget", "scope": "repo", "key": "tmux"}); err != nil {
		t.Fatal(err)
	}
	if _, err := call(map[string]any{"action": "get", "id": entries[0].ID}); err == nil {
		t.Fatal("forgotten memory still loads")
	}
}
