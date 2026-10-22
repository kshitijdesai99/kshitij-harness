package test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kh/internal/config"
	"kh/internal/memory"
	"kh/internal/tools"
)

func TestReferenceToolLoadsFullInertScopedPayload(t *testing.T) {
	s, err := memory.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	const repo = "example/project"
	marker := filepath.Join(t.TempDir(), "executed")
	r, err := s.PutReference(ctx, memory.Reference{Note: memory.Note{Repo: repo, Key: "victory-chime", Text: "Celebrate successful work.", Source: "you"}, Aliases: []string{"cool sound"}, Procedure: strings.Repeat("Full procedure step.\n", 80), Assets: []memory.Asset{{Name: "play.sh", Interpreter: "sh", Source: "printf success > '" + marker + "'"}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	call := func(repo string, in map[string]any) (string, error) {
		b, _ := json.Marshal(in)
		return tools.Memory(s, repo, "").Run(ctx, b)
	}
	text, err := call(repo, map[string]any{"action": "load", "key": "cool sound"})
	if err != nil || !strings.Contains(text, r.Procedure) || !strings.Contains(text, r.Assets[0].Source) || !strings.Contains(text, "current") || !strings.Contains(text, "inert") {
		t.Fatalf("full load: %s %v", text, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("loading executed an asset")
	}
	if out, err := call("elsewhere", map[string]any{"action": "load", "id": r.Note.ID}); err != nil || strings.Contains(out, "Full procedure") {
		t.Fatalf("scope leak: %s %v", out, err)
	}
	if out, err := call(repo, map[string]any{"action": "search", "query": "cool sound"}); err != nil || strings.Contains(out, r.Procedure) {
		t.Fatalf("search loads full body: %s %v", out, err)
	}
	c := config.Defaults
	c.Auto = false
	c.Sandbox = false
	b, _ := json.Marshal(map[string]any{"command": r.Assets[0].Source})
	if _, err := tools.Bash(c, nil).Run(ctx, b); err == nil {
		t.Fatal("asset bypassed command approval")
	}
	c.Auto = true
	if out, err := tools.Bash(c, nil).Run(ctx, b); err != nil {
		t.Fatalf("explicit execution: %s %v", out, err)
	}
	if body, err := os.ReadFile(marker); err != nil || string(body) != "success" {
		t.Fatalf("mock audio execution: %s %v", body, err)
	}
	next := r
	next.Procedure = "Updated procedure"
	next, err = s.PutReference(ctx, next, r.Note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := call(repo, map[string]any{"action": "get", "id": r.Note.ID}); err != nil || !strings.Contains(out, "superseded") || !strings.Contains(out, r.Procedure) {
		t.Fatalf("historical full get: %s %v", out, err)
	}
	if _, err := s.Forget(ctx, repo, r.Note.Key, "", "you"); err != nil {
		t.Fatal(err)
	}
	if out, err := call(repo, map[string]any{"action": "load", "key": "cool sound"}); err != nil || strings.Contains(out, "Updated procedure") {
		t.Fatalf("forgotten alias: %s %v", out, err)
	}
	if out, err := call(repo, map[string]any{"action": "load", "id": next.Note.ID}); err != nil || !strings.Contains(out, "forgotten: true") {
		t.Fatalf("forgotten history: %s %v", out, err)
	}
}
