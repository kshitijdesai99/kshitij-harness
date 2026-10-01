package test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kh/internal/memory"
)

func TestDiscoveryDetailAndScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kh", "memory.db")
	s, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	repo := "repo:/tmp/one"
	e := memory.Entry{Scope: repo, Kind: "workflow", Key: "review", Title: "Visible reviewer collaboration", Summary: "Claude and Codex collaborate in tmux", Keywords: "review claude codex tmux", Detail: "Use named panes; send by name; verify ACK."}
	id, err := s.Put(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	global := memory.Entry{Scope: "global", Kind: "preference", Key: "brief", Title: "Brief answers", Summary: "Keep replies short", Keywords: "reply answers", Detail: "Keep replies concise."}
	gid, err := s.Put(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	if id == gid {
		t.Fatal("duplicate ids")
	}
	found, err := s.Find(ctx, repo, "review in tmux", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != id || found[0].Detail != "" {
		t.Fatalf("discovery should not load detail: %+v", found)
	}
	if other, _ := s.Find(ctx, "repo:/tmp/other", "review tmux", 10); len(other) != 0 {
		t.Fatalf("leaked other repo: %+v", other)
	}
	if shared, _ := s.Find(ctx, "repo:/tmp/other", "answers", 10); len(shared) != 1 || shared[0].ID != gid {
		t.Fatalf("global not visible: %+v", shared)
	}
	if got, err := s.Relevant(ctx, repo, "please review this with Claude in tmux"); err != nil || !strings.Contains(got, "verify ACK") {
		t.Fatalf("automatic retrieval: %q %v", got, err)
	}
	e.Detail = "New procedure."
	updated, err := s.Put(ctx, e)
	if err != nil || updated != id {
		t.Fatalf("upsert: %d %v", updated, err)
	}
	if got, _ := s.Get(ctx, id); got.Detail != "New procedure." {
		t.Fatalf("detail not replaced: %+v", got)
	}
	ok, err := s.Forget(ctx, repo, e.Key)
	if err != nil || !ok {
		t.Fatalf("forget: %v %v", ok, err)
	}
	if _, err := s.Get(ctx, id); err == nil {
		t.Fatal("detail survived deletion")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("database permissions: %v %v", info, err)
	}
}

func TestScopeAndValidation(t *testing.T) {
	s, err := memory.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Put(context.Background(), memory.Entry{Scope: "global", Kind: "workflow", Key: "a", Title: "a", Summary: "a", Detail: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(context.Background(), memory.Entry{Scope: "wrong", Kind: "workflow", Key: "a", Title: "a", Summary: "a", Detail: "a"}); err == nil {
		t.Fatal("accepted bad scope")
	}
	if got, err := s.Relevant(context.Background(), "repo:/else", "nothing relevant"); err != nil || got != "" {
		t.Fatalf("irrelevant memory: %q %v", got, err)
	}
}
