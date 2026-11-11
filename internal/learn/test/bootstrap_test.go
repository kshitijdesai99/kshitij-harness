package test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kh/internal/learn"
	"kh/internal/memory"
)

// Legacy fixtures deliberately bypass Add's new-note normal default. No live
// memory database is read or modified.
func legacyStore(t *testing.T) (*memory.Store, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); s.Close() })
	return s, db
}

func seedLegacy(t *testing.T, s *memory.Store, db *sql.DB, key, source, owner, discussion string) memory.Note {
	t.Helper()
	_, n, err := s.Add(context.Background(), memory.Note{Repo: repo, Kind: "instruction", Key: key, Text: "Keep literal `check --all` for " + key + ".", Source: "import", Owner: owner, ContextSummary: discussion, StatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, memory.Any)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE notes SET importance='',source=? WHERE id=?`, source, n.ID); err != nil {
		t.Fatal(err)
	}
	n.Importance = ""
	n.Source = source
	return n
}

func classification(system string) bool {
	return strings.HasPrefix(system, "You classify the ranking of legacy")
}

func classifyAll(t *testing.T, prompt string, tier string) string {
	t.Helper()
	var notes []memory.Note
	if err := json.Unmarshal([]byte(prompt), &notes); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, n := range notes {
		out = append(out, map[string]any{"id": n.ID, "importance": tier})
	}
	return updateJSON(map[string]any{"classifications": out})
}

func TestLegacyClassificationPreservesProvenanceAndExcludesOtherOrigins(t *testing.T) {
	s, db := legacyStore(t)
	var allowed []memory.Note
	for i, source := range []string{"you", "pre-hook", "post-hook"} {
		allowed = append(allowed, seedLegacy(t, s, db, fmt.Sprintf("valid-%d", i), source, "owner", "A real recorded user discussion."))
	}
	var excluded []memory.Note
	for i, source := range []string{"import", "tool", "peer", "agents", "bash"} {
		excluded = append(excluded, seedLegacy(t, s, db, fmt.Sprintf("origin-%d", i), source, "owner", "Recorded discussion."))
	}
	excluded = append(excluded, seedLegacy(t, s, db, "missing-owner", "you", "", "Discussion."))
	excluded = append(excluded, seedLegacy(t, s, db, "missing-context", "you", "owner", ""))
	missingTime := seedLegacy(t, s, db, "missing-time", "you", "owner", "Discussion.")
	if _, err := db.Exec(`UPDATE notes SET stated_at='' WHERE id=?`, missingTime.ID); err != nil {
		t.Fatal(err)
	}
	excluded = append(excluded, missingTime)
	classifierCalls := 0
	l := learn.New(s, repo, "", model(&calls{}, func(system, prompt string) string {
		if classification(system) {
			classifierCalls++
			for _, n := range excluded {
				if strings.Contains(prompt, n.Key) {
					t.Errorf("excluded source exposed: %s", n.Key)
				}
			}
			return classifyAll(t, prompt, memory.ImportanceCritical)
		}
		return `{"overview":"Context only.","keys":[]}`
	}), 1000)
	l.Close(5 * time.Second)
	for _, old := range allowed {
		n, _, _ := s.Latest(context.Background(), repo, old.Key)
		if n.Importance != memory.ImportanceCritical || n.ID == old.ID || n.Text != old.Text || n.Owner != old.Owner || n.Source != old.Source || !n.StatedAt.Equal(old.StatedAt) || n.ContextSummary != old.ContextSummary {
			t.Fatalf("classification rewrote provenance: old=%+v new=%+v", old, n)
		}
	}
	for _, old := range excluded {
		n, _, _ := s.Latest(context.Background(), repo, old.Key)
		if n.ID != old.ID || n.Importance != "" {
			t.Fatalf("excluded source mutated: %+v", n)
		}
	}
	if classifierCalls != 1 {
		t.Fatalf("classification calls=%d", classifierCalls)
	}
	// Already-classified notes are not retiered on future starts.
	l = learn.New(s, repo, "", model(&calls{}, func(system, prompt string) string {
		if classification(system) {
			t.Error("classified note retiered")
		}
		return `{"overview":"Context.","keys":[]}`
	}), 1000)
	l.Close(5 * time.Second)
}

func TestLegacyClassificationConcurrentExplicitTierWins(t *testing.T) {
	s, db := legacyStore(t)
	old := seedLegacy(t, s, db, "race", "you", "owner", "Actual user discussion.")
	var explicit memory.Note
	l := learn.New(s, repo, "", model(&calls{}, func(system, prompt string) string {
		if classification(system) {
			var err error
			explicit, err = s.SetImportance(context.Background(), old.ID, memory.ImportanceNormal, false)
			if err != nil {
				t.Error(err)
			}
			return classifyAll(t, prompt, memory.ImportanceCritical)
		}
		return `{"overview":"Context.","keys":[]}`
	}), 1000)
	l.Close(5 * time.Second)
	n, _, _ := s.Latest(context.Background(), repo, old.Key)
	if explicit.ID == 0 || n.ID != explicit.ID || n.Importance != memory.ImportanceNormal {
		t.Fatalf("explicit user lost race: %+v", n)
	}
}

func TestInvalidClassificationDoesNotMutateOrRetry(t *testing.T) {
	for _, output := range []string{`{"classifications":[{"id":999999,"importance":"critical"}]}`, `{"classifications":[{"id":1,"importance":"urgent"}]}`, `{"classifications":[{"id":1,"importance":"critical"},{"id":1,"importance":"normal"}]}`} {
		s, db := legacyStore(t)
		old := seedLegacy(t, s, db, "invalid", "you", "owner", "Actual user discussion.")
		classified := make(chan struct{})
		var ready sync.Once
		classifierCalls := 0
		l := learn.New(s, repo, "", model(&calls{}, func(system, prompt string) string {
			if classification(system) {
				classifierCalls++
				return output
			}
			// The summary starts only after the classifier job cleared its
			// coalescing flag, so the next trigger really schedules a batch.
			ready.Do(func() { close(classified) })
			return `{"overview":"Context.","keys":[]}`
		}), 1000)
		<-classified
		// A subsequent catch-up trigger cannot repeatedly retry this revision.
		l.Finished(nil, "")
		l.Close(5 * time.Second)
		n, _, _ := s.Latest(context.Background(), repo, old.Key)
		if n.ID != old.ID || n.Importance != "" || classifierCalls != 1 {
			t.Fatalf("invalid classifier mutated/retried: %+v calls=%d", n, classifierCalls)
		}
	}
}

func TestLegacyBatchesBoundedAndProgressOnLaterStartup(t *testing.T) {
	s, db := legacyStore(t)
	for i := 0; i < 11; i++ {
		seedLegacy(t, s, db, fmt.Sprintf("batch-%02d", i), "you", "owner", "Actual user discussion.")
	}
	var sizes []int
	answer := func(system, prompt string) string {
		if classification(system) {
			var notes []memory.Note
			json.Unmarshal([]byte(prompt), &notes)
			sizes = append(sizes, len(notes))
			return classifyAll(t, prompt, memory.ImportanceCritical)
		}
		return `{"overview":"Context.","keys":[]}`
	}
	for i := 0; i < 2; i++ {
		l := learn.New(s, repo, "", model(&calls{}, answer), 1000)
		l.Close(5 * time.Second)
	}
	if len(sizes) != 2 || sizes[0] != 8 || sizes[1] != 3 {
		t.Fatalf("unbounded or starved batches: %v", sizes)
	}
	ns, _ := s.Current(context.Background(), repo, "")
	for _, n := range ns {
		if n.Importance != memory.ImportanceCritical {
			t.Fatalf("legacy never progressed: %+v", n)
		}
	}
}
