package test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"kh/internal/memory"
)

const repo = "github.com/me/app"

func open(t *testing.T) *memory.Store {
	t.Helper()
	s, err := memory.Open(filepath.Join(t.TempDir(), "kh", "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func add(t *testing.T, s *memory.Store, n memory.Note) memory.Note {
	t.Helper()
	if n.Repo == "" {
		n.Repo = repo
	}
	if n.Source == "" {
		n.Source = "you"
	}
	_, saved, err := s.Add(context.Background(), n, memory.Any)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestNewestRowWinsAndHistoryStays(t *testing.T) {
	s, ctx := open(t), context.Background()
	first := add(t, s, memory.Note{Kind: "instruction", Key: "css-framework", Text: "Use Tailwind."})
	prev, second, err := s.Add(ctx, memory.Note{Repo: repo, Kind: "instruction", Key: "css-framework", Text: "Use plain CSS.", Source: "pre-hook"}, first.ID)
	if err != nil || prev == nil || prev.ID != first.ID || second.ID == first.ID {
		t.Fatalf("replace: %+v %+v %v", prev, second, err)
	}
	cur, err := s.Current(ctx, repo, "instruction")
	if err != nil || len(cur) != 1 || cur[0].Text != "Use plain CSS." {
		t.Fatalf("current: %+v %v", cur, err)
	}
	hist, err := s.History(ctx, repo, "css-framework")
	if err != nil || len(hist) != 2 || hist[0].ID != second.ID || !hist[0].CreatedAt.After(hist[1].CreatedAt) {
		t.Fatalf("history: %+v %v", hist, err)
	}
}

func TestStaleDecisionIsRejected(t *testing.T) {
	s, ctx := open(t), context.Background()
	add(t, s, memory.Note{Kind: "instruction", Key: "css-framework", Text: "Use Tailwind."})
	// A hook that was shown "no such topic" must not overwrite one that appeared since.
	if _, _, err := s.Add(ctx, memory.Note{Repo: repo, Kind: "instruction", Key: "css-framework", Text: "x", Source: "pre-hook"}, 0); !errors.Is(err, memory.ErrStale) {
		t.Fatalf("want ErrStale, got %v", err)
	}
}

func TestGotchaNeverReplacesInstruction(t *testing.T) {
	s, ctx := open(t), context.Background()
	in := add(t, s, memory.Note{Kind: "instruction", Key: "db", Text: "Use SQLite."})
	if _, _, err := s.Add(ctx, memory.Note{Repo: repo, Kind: "gotcha", Key: "db", Text: "DB locks.", Source: "post-hook"}, in.ID); err == nil {
		t.Fatal("a hook's gotcha replaced an instruction")
	}
	if _, _, err := s.Add(ctx, memory.Note{Repo: repo, Kind: "gotcha", Key: "db", Text: "DB locks.", Source: "you"}, in.ID); err != nil {
		t.Fatalf("the user may reclassify: %v", err)
	}
}

func TestForgetThenPurge(t *testing.T) {
	s, ctx := open(t), context.Background()
	add(t, s, memory.Note{Kind: "gotcha", Key: "test-db", Text: "Tests need the database running."})
	if ok, err := s.Forget(ctx, repo, "test-db", "", "you"); !ok || err != nil {
		t.Fatalf("forget: %v %v", ok, err)
	}
	if cur, _ := s.Current(ctx, repo, ""); len(cur) != 0 {
		t.Fatalf("forgotten note still current: %+v", cur)
	}
	if hist, _ := s.History(ctx, repo, "test-db"); len(hist) != 2 {
		t.Fatalf("forget should keep history: %+v", hist)
	}
	if ok, _ := s.Forget(ctx, repo, "test-db", "", "you"); ok {
		t.Fatal("forgot twice")
	}
	if n, err := s.Purge(ctx, repo, "test-db"); n != 2 || err != nil {
		t.Fatalf("purge: %d %v", n, err)
	}
	if hist, _ := s.History(ctx, repo, "test-db"); len(hist) != 0 {
		t.Fatalf("purge left rows: %+v", hist)
	}
	if found, _ := s.Search(ctx, repo, "gotcha", "database running", 10); len(found) != 0 {
		t.Fatalf("purged text still searchable: %+v", found)
	}
}

func TestReposAreIsolatedAndGlobalIsShared(t *testing.T) {
	s, ctx := open(t), context.Background()
	add(t, s, memory.Note{Kind: "instruction", Key: "css", Text: "Use plain CSS."})
	add(t, s, memory.Note{Repo: memory.Global, Kind: "instruction", Key: "tests", Text: "Put tests in a test folder."})
	add(t, s, memory.Note{Repo: "github.com/me/other", Kind: "instruction", Key: "css", Text: "Use Tailwind."})
	cur, err := s.Current(ctx, repo, "")
	if err != nil || len(cur) != 2 {
		t.Fatalf("current: %+v %v", cur, err)
	}
	for _, n := range cur {
		if n.Text == "Use Tailwind." {
			t.Fatal("leaked another repo's note")
		}
	}
}

func TestValidation(t *testing.T) {
	s, ctx := open(t), context.Background()
	for _, n := range []memory.Note{
		{Repo: repo, Kind: "fact", Key: "a", Text: "a", Source: "you"},
		{Repo: repo, Kind: "instruction", Key: "", Text: "a", Source: "you"},
		{Repo: repo, Kind: "instruction", Key: "summary", Text: "a", Source: "you"},
		{Repo: repo, Kind: "instruction", Key: "a", Text: strings.Repeat("x", memory.MaxText+1), Source: "you"},
		{Repo: repo, Kind: "instruction", Key: "deploy", Text: "Deploy with api_key=abcd1234efgh", Source: "you"},
		{Repo: repo, Kind: "gotcha", Key: "gh", Text: "Token ghp_" + strings.Repeat("a", 36), Source: "post-hook"},
	} {
		if _, _, err := s.Add(ctx, n, memory.Any); err == nil {
			t.Errorf("accepted %+v", n)
		}
	}
}

func TestSearchSkipsOldVersionsAndReturnsAllWhenFew(t *testing.T) {
	s, ctx := open(t), context.Background()
	add(t, s, memory.Note{Kind: "gotcha", Key: "build", Text: "The build needs Node 20 installed."})
	if found, _ := s.Search(ctx, repo, "gotcha", "unrelated words", 10); len(found) != 1 {
		t.Fatalf("few notes should all load: %+v", found)
	}
	for _, k := range []string{"a", "b", "c"} {
		add(t, s, memory.Note{Kind: "gotcha", Key: "filler-" + k, Text: "Something about " + k + " things."})
	}
	add(t, s, memory.Note{Kind: "gotcha", Key: "build", Text: "The build needs Node 22 now."})
	found, err := s.Search(ctx, repo, "gotcha", "node build", 2)
	if err != nil || len(found) != 1 || !strings.Contains(found[0].Text, "Node 22") {
		t.Fatalf("search should rank only current rows: %+v %v", found, err)
	}
	if found, _ := s.Search(ctx, repo, "gotcha", `"build" OR (*`, 2); len(found) != 1 {
		t.Fatalf("message text must not break FTS syntax: %+v", found)
	}
}

// state mimics a provider's saved chat holding a memory message.
func state(t *testing.T, texts ...string) []byte {
	b, err := json.Marshal(map[string]any{"input": texts})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRecallSendsEachNoteOncePerChat(t *testing.T) {
	s, ctx := open(t), context.Background()
	b := memory.Budget{SummaryChars: 10000, TopInstructions: 10, TopGotchas: 10}
	css := add(t, s, memory.Note{Kind: "instruction", Key: "css-framework", Text: "Use Tailwind."})
	main := add(t, s, memory.Note{Kind: "instruction", Key: "main-branch", Text: "Never commit to main."})
	sum := add(t, s, memory.Note{Kind: "summary", Key: memory.SummaryKey, Text: "A small web app.", Refs: []string{"main-branch"}, Source: "summary"})

	first, err := s.Recall(ctx, repo, "style the button", map[int64]bool{}, b)
	if err != nil || !strings.HasPrefix(first, memory.Header) {
		t.Fatalf("first recall: %q %v", first, err)
	}
	for _, want := range []string{"[memory summary #", "A small web app.", "Never commit to main.", "Use Tailwind."} {
		if !strings.Contains(first, want) {
			t.Fatalf("first recall missing %q:\n%s", want, first)
		}
	}
	if strings.Count(first, "Never commit to main.") != 1 {
		t.Fatalf("must-load note repeated:\n%s", first)
	}
	chat := state(t, "fix login", first)
	seen := memory.Seen(chat)
	if !seen[css.ID] || !seen[main.ID] || !seen[sum.ID] {
		t.Fatalf("seen: %v", seen)
	}
	if again, _ := s.Recall(ctx, repo, "style the button", seen, b); again != "" {
		t.Fatalf("resent notes already in chat:\n%s", again)
	}

	if _, _, err := s.Add(ctx, memory.Note{Repo: repo, Kind: "instruction", Key: "css-framework", Text: "Use plain CSS.", Source: "you"}, css.ID); err != nil {
		t.Fatal(err)
	}
	s.Forget(ctx, repo, "main-branch", "", "you")
	update, _ := s.Recall(ctx, repo, "anything", memory.Seen(chat), b)
	if !strings.Contains(update, "(replaces earlier note): Use plain CSS.") || !strings.Contains(update, "main-branch: no longer applies.") {
		t.Fatalf("changes to notes in chat not sent:\n%s", update)
	}

	// Ids quoted outside a memory message (a compaction summary) do not count.
	compacted := state(t, "Conversation summary: we used [memory #"+itoa(css.ID)+"].")
	if len(memory.Seen(compacted)) != 0 {
		t.Fatal("markers outside memory messages counted as sent")
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestChangedTracksSummaryAge(t *testing.T) {
	s, ctx := open(t), context.Background()
	if ch, _ := s.Changed(ctx, repo); ch {
		t.Fatal("empty repo reported changed")
	}
	add(t, s, memory.Note{Kind: "instruction", Key: "a", Text: "Rule A."})
	if ch, _ := s.Changed(ctx, repo); !ch {
		t.Fatal("new note not reported")
	}
	add(t, s, memory.Note{Kind: "summary", Key: memory.SummaryKey, Text: "Overview.", Source: "summary"})
	if ch, _ := s.Changed(ctx, repo); ch {
		t.Fatal("fresh summary reported stale")
	}
}

func TestMigratesOldTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE discovery (id INTEGER PRIMARY KEY, scope TEXT, kind TEXT, key TEXT, title TEXT, summary TEXT, keywords TEXT, updated_at TEXT)`,
		`CREATE TABLE detail (discovery_id INTEGER PRIMARY KEY, body TEXT)`,
		`INSERT INTO discovery VALUES (1, 'global', 'preference', 'brief', 'Brief answers', 'short', '', '2026-10-01 10:00:00')`,
		`INSERT INTO detail VALUES (1, 'Keep replies concise.')`,
		`INSERT INTO discovery VALUES (2, 'repo:/no/such/dir', 'fact', 'db', 'Database', 'sqlite', '', '2026-10-01 10:00:00')`,
		`INSERT INTO detail VALUES (2, 'The store is SQLite.')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	s, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	g, ok, _ := s.Latest(ctx, memory.Global, "brief")
	if !ok || g.Kind != "instruction" || g.Text != "Brief answers: Keep replies concise." {
		t.Fatalf("global preference: %+v", g)
	}
	f, ok, _ := s.Latest(ctx, "/no/such/dir", "db")
	if !ok || f.Kind != "gotcha" {
		t.Fatalf("repo fact: %+v", f)
	}
	s.Close()
	if s, err = memory.Open(path); err != nil { // reopening must not migrate twice
		t.Fatal(err)
	}
	if hist, _ := s.History(ctx, memory.Global, "brief"); len(hist) != 1 {
		t.Fatalf("migrated twice: %+v", hist)
	}
}

func TestRepoScopeUsesNormalizedRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for remote, want := range map[string]string{
		"https://user:token@GitHub.com/me/app.git": "github.com/me/app",
		"git@github.com:me/app.git":                "github.com/me/app",
		"ssh://git@github.com:2222/me/app":         "github.com/me/app",
	} {
		dir := t.TempDir()
		for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", remote}} {
			if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %s", args, out)
			}
		}
		if got := memory.RepoScope(dir); got != want {
			t.Errorf("%s: got %q want %q", remote, got, want)
		}
	}
	plain := t.TempDir()
	if got, _ := filepath.EvalSymlinks(memory.RepoScope(plain)); got != mustEval(t, plain) {
		t.Errorf("no git: got %q", got)
	}
}

func mustEval(t *testing.T, p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDatabaseIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("database permissions: %v %v", info, err)
	}
}
