package test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kh/internal/memory"
)

func TestProvenanceRoundTripAndOverrides(t *testing.T) {
	s, ctx := open(t), context.Background()
	stated := time.Date(2025, 1, 2, 3, 4, 5, 123, time.FixedZone("offset", 3600))
	first := add(t, s, memory.Note{Kind: "fact", Key: "database", Text: "The database is SQLite.", ContextSummary: "The user described the storage architecture.", StatedAt: stated, Overridden: 999})
	if first.Overridden != 0 || !first.StatedAt.Equal(stated) || first.StatedAt.Location() != time.UTC || !first.CreatedAt.After(stated) {
		t.Fatalf("first provenance: %+v", first)
	}
	second := add(t, s, memory.Note{Kind: "decision", Key: first.Key, Text: "Use PostgreSQL instead.", ContextSummary: "The user changed the storage plan.", StatedAt: stated.Add(time.Hour), Overridden: 999})
	if second.Overridden != first.ID {
		t.Fatalf("override = %d, want %d", second.Overridden, first.ID)
	}
	got, ok, err := s.Get(ctx, second.ID)
	if err != nil || !ok || got.ContextSummary != second.ContextSummary || !got.StatedAt.Equal(second.StatedAt) || got.Overridden != first.ID || !got.CreatedAt.Equal(second.CreatedAt) {
		t.Fatalf("get current: %+v %v %v", got, ok, err)
	}
	got, ok, err = s.Get(ctx, got.Overridden)
	if err != nil || !ok || got.ID != first.ID || got.ContextSummary != first.ContextSummary {
		t.Fatalf("get previous: %+v %v %v", got, ok, err)
	}
	if _, ok, err := s.Get(ctx, 999999); ok || err != nil {
		t.Fatalf("missing get: %v %v", ok, err)
	}
	if ok, err := s.Forget(ctx, repo, first.Key, "", "you"); !ok || err != nil {
		t.Fatalf("forget: %v %v", ok, err)
	}
	forgotten, _, err := s.Latest(ctx, repo, first.Key)
	if err != nil || !forgotten.Forgotten || forgotten.Overridden != second.ID || forgotten.StatedAt.IsZero() {
		t.Fatalf("forget provenance: %+v %v", forgotten, err)
	}
	restored := add(t, s, memory.Note{Kind: "fact", Key: first.Key, Text: "SQLite again."})
	if restored.Overridden != forgotten.ID || restored.StatedAt.IsZero() {
		t.Fatalf("restore provenance: %+v", restored)
	}
}

func TestProvenanceRejectsOlderStatementAndPreservesCAS(t *testing.T) {
	s, ctx := open(t), context.Background()
	at := time.Now().UTC().Add(-time.Hour)
	first := add(t, s, memory.Note{Kind: "instruction", Key: "db", Text: "Use SQLite.", StatedAt: at})
	for _, expect := range []int64{memory.Any, first.ID} {
		_, _, err := s.Add(ctx, memory.Note{Repo: repo, Kind: first.Kind, Key: first.Key, Text: "An older plan.", Source: "you", StatedAt: at.Add(-time.Nanosecond)}, expect)
		if !errors.Is(err, memory.ErrStale) {
			t.Fatalf("older statement with expect %d: %v", expect, err)
		}
	}
	equal := add(t, s, memory.Note{Kind: first.Kind, Key: first.Key, Text: "Clarification at the same time.", StatedAt: at})
	if equal.Overridden != first.ID {
		t.Fatalf("equal statement should append: %+v", equal)
	}
	if _, _, err := s.Add(ctx, memory.Note{Repo: repo, Kind: "decision", Key: first.Key, Text: "Use something else.", Source: "post-hook", StatedAt: at.Add(time.Second)}, memory.Any); err == nil {
		t.Fatal("hook replaced an instruction")
	}
	if _, _, err := s.Add(ctx, memory.Note{Repo: repo, Kind: first.Kind, Key: first.Key, Text: "Newer decision on stale revision.", Source: "you", StatedAt: at.Add(time.Second)}, first.ID); !errors.Is(err, memory.ErrStale) {
		t.Fatalf("CAS: %v", err)
	}
	history, err := s.History(ctx, repo, first.Key)
	if err != nil || len(history) != 2 || history[0].ID != equal.ID || history[1].ID != first.ID {
		t.Fatalf("history after stale writes: %+v %v", history, err)
	}
}

func TestProvenanceContextValidation(t *testing.T) {
	s, ctx := open(t), context.Background()
	for _, summary := range []string{strings.Repeat("界", memory.MaxText+1), "User supplied api_key=abcd1234efgh", "Token ghp_" + strings.Repeat("a", 36)} {
		if _, _, err := s.Add(ctx, memory.Note{Repo: repo, Kind: "fact", Key: "context", Text: "A safe fact.", Source: "you", ContextSummary: summary}, memory.Any); err == nil {
			t.Fatal("accepted invalid context summary")
		}
	}
	n := add(t, s, memory.Note{Kind: "fact", Key: "context", Text: "A safe fact.", ContextSummary: strings.Repeat("界", memory.MaxText)})
	if len([]rune(n.ContextSummary)) != memory.MaxText {
		t.Fatal("valid context was changed")
	}
}

func TestProvenanceLegacyImportKeepsStatementTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE discovery (id INTEGER PRIMARY KEY, scope TEXT, kind TEXT, key TEXT, title TEXT, summary TEXT, keywords TEXT, updated_at TEXT)`,
		`CREATE TABLE detail (discovery_id INTEGER PRIMARY KEY, body TEXT)`,
		`INSERT INTO discovery VALUES (1, 'global', 'preference', 'brief', 'Brief answers', 'short', '', '2025-01-02 03:04:05')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	before := time.Now().UTC()
	s, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, ok, err := s.Latest(context.Background(), memory.Global, "brief")
	stated := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	if err != nil || !ok || !n.StatedAt.Equal(stated) || n.CreatedAt.Before(before) || n.Overridden != 0 {
		t.Fatalf("legacy import provenance: %+v %v %v", n, ok, err)
	}
}

func TestProvenanceMigratesExistingNotes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	const older = "2025-01-01T00:00:00.000000000Z"
	const newer = "2025-01-02T00:00:00.000000000Z"
	for _, stmt := range []string{
		`CREATE TABLE notes (id INTEGER PRIMARY KEY, repo TEXT NOT NULL, kind TEXT NOT NULL, key TEXT NOT NULL, text TEXT NOT NULL, refs TEXT NOT NULL DEFAULT '', owner TEXT NOT NULL DEFAULT '', source TEXT NOT NULL, forgotten INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL)`,
		`INSERT INTO notes(id,repo,kind,key,text,source,created_at) VALUES (1,'repo','instruction','db','Newer','you','` + newer + `'),(2,'repo','instruction','db','Older','you','` + older + `'),(3,'repo','instruction','db','Tie','you','` + newer + `'),(4,'other','instruction','db','Other repo','you','` + newer + `')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	ctx := context.Background()
	for attempt := 0; attempt < 2; attempt++ {
		s, err := memory.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		history, err := s.History(ctx, "repo", "db")
		if err != nil || len(history) != 3 {
			t.Fatalf("history: %+v %v", history, err)
		}
		for i, want := range []struct{ id, previous int64 }{{3, 1}, {1, 2}, {2, 0}} {
			n := history[i]
			if n.ID != want.id || n.Overridden != want.previous || !n.StatedAt.Equal(n.CreatedAt) || n.ContextSummary != "" {
				t.Fatalf("migrated row: %+v, want %+v", n, want)
			}
		}
		other, _, err := s.Get(ctx, 4)
		if err != nil || other.Overridden != 0 {
			t.Fatalf("cross-repo override: %+v %v", other, err)
		}
		// Statement chronology is independent of insertion order. Simulate old
		// imported metadata whose statement times differ from insertion times.
		if attempt == 1 {
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE notes SET stated_at = '2024-01-01T00:00:00.000000000Z' WHERE id = 3`); err != nil {
				t.Fatal(err)
			}
			db.Close()
			history, err = s.History(ctx, "repo", "db")
			latest, _, latestErr := s.Latest(ctx, "repo", "db")
			if err != nil || latestErr != nil || history[0].ID != 1 || history[2].ID != 3 || latest.ID != 3 {
				t.Fatalf("statement chronology vs latest: %+v %+v %v %v", history, latest, err, latestErr)
			}
		}
		s.Close()
	}
}
