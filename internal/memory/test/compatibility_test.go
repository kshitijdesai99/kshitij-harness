package test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"kh/internal/memory"
)

func TestProvenanceLegacyWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first := add(t, s, memory.Note{Kind: "fact", Key: "storage", Text: "Use SQLite."})
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stamp := time.Now().Add(time.Second).UTC().Format("2006-01-02T15:04:05.000000000Z")
	// Simulate the INSERT used by a still-running pre-migration binary.
	if _, err := db.Exec(`INSERT INTO notes(repo,kind,key,text,source,created_at) VALUES (?,?,?,?,?,?)`, repo, "fact", "storage", "Use PostgreSQL.", "you", stamp); err != nil {
		t.Fatal(err)
	}
	current, ok, err := s.Latest(context.Background(), repo, "storage")
	if err != nil || !ok || current.StatedAt.IsZero() || current.Overridden != first.ID || !current.StatedAt.Equal(current.CreatedAt) {
		t.Fatalf("legacy provenance: %+v %v %v", current, ok, err)
	}
}

func TestProvenanceConcurrentOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	// Existing base schema, before either process sees the metadata columns.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, repo TEXT NOT NULL, kind TEXT NOT NULL, key TEXT NOT NULL, text TEXT NOT NULL, refs TEXT NOT NULL DEFAULT '', owner TEXT NOT NULL DEFAULT '', source TEXT NOT NULL, forgotten INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL)`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			s, err := memory.Open(path)
			if err == nil {
				err = s.Close()
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
