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

func TestMemoryExplainContentFreeAndScoped(t *testing.T) {
	s, err := memory.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	const repo = "github.com/private-owner/private-project"
	for _, scope := range []string{repo, memory.Global, "github.com/private-owner/foreign"} {
		if _, _, err := s.Add(ctx, memory.Note{Repo: scope, Kind: "instruction", Key: "private-key", Text: "confirmation private-note-body", Source: "you"}, memory.Any); err != nil {
			t.Fatal(err)
		}
	}
	tool := tools.Memory(s, repo, "private-owner")
	for _, tc := range []struct {
		scope, query, label, selected string
	}{{"repo", "confirmation private-query-marker", "repo+global", "2"}, {"global", "confirmation private-query-marker", "global", "1"}, {"repo", "approval", "repo+global", "0"}} {
		input, _ := json.Marshal(map[string]any{"action": "explain", "scope": tc.scope, "query": tc.query})
		out, err := tool.Run(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"scope: " + tc.label, "semantic: configured=false, attempted=false", "rerank: configured=false, attempted=false", "selected=" + tc.selected} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing %q: %q", want, out)
			}
		}
		for _, private := range []string{"private", "confirmation", "approval", repo, "[memory #"} {
			if strings.Contains(out, private) {
				t.Fatalf("explain leaked %q: %q", private, out)
			}
		}
	}
	// The diagnostic action does not alter ordinary search output.
	out, err := tool.Run(ctx, json.RawMessage(`{"action":"search","query":"approval"}`))
	if err != nil || out != "no matching memories" {
		t.Fatalf("search changed: %q %v", out, err)
	}
	// Operational errors are represented by stage state, never raw error text.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	out, err = tool.Run(ctx, json.RawMessage(`{"action":"explain","query":"confirmation"}`))
	if err != nil || !strings.Contains(out, "Memory search: failed") || strings.Contains(out, "database is closed") {
		t.Fatalf("failed explain: %q %v", out, err)
	}
}
