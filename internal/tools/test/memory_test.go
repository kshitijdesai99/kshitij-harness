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
	tool := tools.Memory(s, "github.com/me/app", "me@example.com")
	call := func(in map[string]any) (string, error) {
		b, _ := json.Marshal(in)
		return tool.Run(context.Background(), b)
	}
	if out, err := call(map[string]any{"action": "remember", "kind": "instruction", "key": "review-panes", "text": "Show reviewers in the right-hand pane."}); err != nil || !strings.Contains(out, "remembered [memory #") {
		t.Fatalf("remember: %q %v", out, err)
	}
	if out, err := call(map[string]any{"action": "remember", "kind": "instruction", "key": "review-panes", "text": "Show reviewers below the chat."}); err != nil || !strings.Contains(out, "replaced") {
		t.Fatalf("replace: %q %v", out, err)
	}
	list, err := call(map[string]any{"action": "search", "query": "reviewers pane"})
	if err != nil || !strings.Contains(list, "below the chat") || strings.Contains(list, "right-hand") {
		t.Fatalf("search should show only the current version: %q %v", list, err)
	}
	cur, _ := s.Current(context.Background(), "github.com/me/app", "")
	if len(cur) != 1 || cur[0].Owner != "me@example.com" || cur[0].Source != "you" {
		t.Fatalf("stored: %+v", cur)
	}
	if _, err := call(map[string]any{"action": "remember", "kind": "instruction", "key": "deploy", "text": "Deploy with password: hunter22"}); err == nil {
		t.Fatal("stored a secret")
	}
	if out, err := call(map[string]any{"action": "forget", "key": "review-panes"}); err != nil || out != "forgot review-panes" {
		t.Fatalf("forget: %q %v", out, err)
	}
	if out, _ := call(map[string]any{"action": "search", "query": "reviewers"}); out != "no matching memories" {
		t.Fatalf("forgotten note still found: %q", out)
	}
}
