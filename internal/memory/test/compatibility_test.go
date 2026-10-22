package test

import (
	"context"
	"database/sql"
	"errors"
	"os"
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
	concurrentOpen(t)
}

func TestProvenanceConcurrentOpenStress(t *testing.T) {
	for i := 0; i < 40; i++ {
		t.Run("open", concurrentOpen)
	}
}

func TestOpenBusyDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE held (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM held`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	// A rollback-journal reader prevents WAL conversion until it releases
	// its lock. Open must stop retrying, not hang waiting for this reader.
	start := time.Now()
	s, err := memory.Open(path)
	if s != nil {
		s.Close()
	}
	if err == nil {
		t.Fatal("Open succeeded while WAL conversion was blocked")
	}
	if elapsed := time.Since(start); elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf("Open retry took %v; want the five-second budget: %v", elapsed, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	s, err = memory.Open(path)
	if err != nil {
		t.Fatalf("Open after releasing reader: %v", err)
	}
	s.Close()
}

func TestOpenNonBusyError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	if err := os.WriteFile(path, []byte("not a SQLite database"), 0600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	s, err := memory.Open(path)
	if s != nil {
		s.Close()
	}
	var sqliteErr interface{ Code() int }
	if !errors.As(err, &sqliteErr) || sqliteErr.Code()&0xff == 5 {
		t.Fatalf("want the original non-busy SQLite error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("non-busy failure was retried for %v: %v", elapsed, err)
	}
}

func concurrentOpen(t *testing.T) {
	t.Helper()
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
