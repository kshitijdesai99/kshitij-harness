package test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"kh/internal/memory"
)

func TestHierarchyVersionMetadataAndValidation(t *testing.T) {
	s, ctx := open(t), context.Background()
	first := add(t, s, memory.Note{Kind: "instruction", Key: "priority", Text: "Keep safe."})
	if first.Importance != memory.ImportanceNormal {
		t.Fatalf("new default: %+v", first)
	}
	critical, err := s.SetImportance(ctx, first.ID, memory.ImportanceCritical, false)
	if err != nil || critical.Overridden != first.ID || critical.Text != first.Text || critical.Source != first.Source || !critical.StatedAt.Equal(first.StatedAt) {
		t.Fatalf("metadata version: %+v %v", critical, err)
	}
	if _, err = s.SetImportance(ctx, first.ID, memory.ImportanceNormal, false); !errors.Is(err, memory.ErrStale) {
		t.Fatalf("stale classification: %v", err)
	}
	next := add(t, s, memory.Note{Kind: "instruction", Key: first.Key, Text: "Keep safer."})
	if next.Importance != memory.ImportanceCritical {
		t.Fatalf("ordinary update demoted critical: %+v", next)
	}
	demoted := add(t, s, memory.Note{Kind: "instruction", Key: first.Key, Text: "Ordinary now.", Importance: memory.ImportanceNormal, ExpiresAt: time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)})
	if demoted.Importance != memory.ImportanceNormal || demoted.ExpiresAt.IsZero() {
		t.Fatalf("explicit metadata: %+v", demoted)
	}
	if changed, err := s.SetImportance(ctx, demoted.ID, memory.ImportanceCritical, false); err != nil || !changed.ExpiresAt.Equal(demoted.ExpiresAt) || !changed.StatedAt.Equal(demoted.StatedAt) {
		t.Fatalf("metadata-only update changed expiry/provenance: %+v %v", changed, err)
	}
	cleared := add(t, s, memory.Note{Kind: "instruction", Key: first.Key, Text: "Permanent now."})
	if !cleared.ExpiresAt.IsZero() {
		t.Fatal("full-version zero expiry did not clear")
	}
	for _, n := range []memory.Note{
		{Importance: "core"}, {Importance: "Critical"}, {ExpiresAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}, {ExpiresAt: time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)}, {ExpiresAt: time.Date(9999, 12, 31, 23, 0, 0, 0, time.FixedZone("west", -3600))},
	} {
		n.Repo, n.Kind, n.Key, n.Text, n.Source = repo, "fact", "invalid", "Invalid metadata.", "you"
		if _, _, err = s.Add(ctx, n, 0); err == nil {
			t.Fatalf("accepted invalid metadata: %+v", n)
		}
	}
	if _, err = s.SetImportance(ctx, cleared.ID, "", false); err == nil {
		t.Fatal("accepted empty explicit classification")
	}
}

func TestHierarchyExpiryDoesNotResurrectHistory(t *testing.T) {
	s, ctx := open(t), context.Background()
	at := time.Date(2020, 1, 2, 3, 4, 5, 123, time.UTC)
	n := memory.Note{ExpiresAt: at}
	if !n.Active(at.Add(-time.Nanosecond)) || n.Active(at) || n.Active(at.Add(time.Nanosecond)) {
		t.Fatal("expiry boundary must be exclusive")
	}
	n.Forgotten = true
	if n.Active(at.Add(-time.Hour)) {
		t.Fatal("forgotten note active")
	}
	old := add(t, s, memory.Note{Kind: "instruction", Key: "transport", Text: "Choose an automobile."})
	expired := add(t, s, memory.Note{Kind: "instruction", Key: old.Key, Text: "Choose an automobile temporarily.", ExpiresAt: at, Importance: memory.ImportanceCritical})
	if ns, err := s.Current(ctx, repo, ""); err != nil || len(ns) != 0 {
		t.Fatalf("current resurrected old: %+v %v", ns, err)
	}
	if ns, err := s.Search(ctx, repo, "", "automobile", 10); err != nil || len(ns) != 0 {
		t.Fatalf("search resurrected old: %+v %v", ns, err)
	}
	configureSemantic(t, s, &fakeEmbedder{model: "expiry-fixture"})
	if ns := semanticSearch(t, s, "car"); len(ns) != 0 {
		t.Fatalf("semantic resurrected old: %+v", ns)
	}
	if latest, ok, err := s.Latest(ctx, repo, old.Key); err != nil || !ok || latest.ID != expired.ID {
		t.Fatalf("expired provenance unavailable: %+v %v %v", latest, ok, err)
	}
	if got, ok, err := s.Get(ctx, expired.ID); err != nil || !ok || !got.ExpiresAt.Equal(at) {
		t.Fatalf("expiry roundtrip: %+v %v", got, err)
	}
	if hist, err := s.History(ctx, repo, old.Key); err != nil || len(hist) != 2 {
		t.Fatalf("history: %+v %v", hist, err)
	}
	r := reference("expired-procedure")
	r.Note.ExpiresAt = at
	saved, err := s.PutReference(ctx, r, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.LatestReference(ctx, repo, r.Note.Key); err != nil || ok {
		t.Fatalf("expired procedure name resolved: %v %v", ok, err)
	}
	if _, ok, err := s.Reference(ctx, repo, saved.Note.ID); err != nil || !ok {
		t.Fatalf("expired historical procedure missing: %v %v", ok, err)
	}
}

func TestHierarchyMigrationAndBootstrapCAS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	const at = "2020-01-02T03:04:05.000000123Z"
	_, err = db.Exec(`CREATE TABLE notes(id INTEGER PRIMARY KEY,repo TEXT NOT NULL,kind TEXT NOT NULL,key TEXT NOT NULL,text TEXT NOT NULL,refs TEXT NOT NULL DEFAULT '',owner TEXT NOT NULL DEFAULT '',source TEXT NOT NULL,forgotten INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL,context_summary TEXT DEFAULT '',stated_at TEXT DEFAULT '',overridden INTEGER DEFAULT 0)`)
	if err != nil {
		t.Fatal(err)
	}
	for i, source := range []string{"you", "pre-hook", "post-hook", "import", "summary", "you"} {
		contextText := "User described safe execution during setup."
		if i == 5 {
			contextText = ""
		}
		_, err = db.Exec(`INSERT INTO notes(id,repo,kind,key,text,owner,source,created_at,context_summary,stated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, i+1, repo, "instruction", string(rune('a'+i)), "Keep execution safe.", "session-fixture", source, at, contextText, at)
		if err != nil {
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
	for id := int64(1); id <= 6; id++ {
		old, ok, err := s.Get(ctx, id)
		if err != nil || !ok || old.Importance != "" || !old.ExpiresAt.IsZero() || old.SourceRevision != "" {
			t.Fatalf("migration rewrote legacy: %+v %v", old, err)
		}
		got, err := s.SetImportance(ctx, id, memory.ImportanceCritical, true)
		if id > 3 {
			if !errors.Is(err, memory.ErrStale) {
				t.Fatalf("unverified legacy %d classified: %+v %v", id, got, err)
			}
			continue
		}
		if err != nil || got.Overridden != id || got.Importance != memory.ImportanceCritical || got.Key != old.Key || got.Text != old.Text || got.Kind != old.Kind || got.Repo != old.Repo || got.ContextSummary != old.ContextSummary || got.Source != old.Source || got.Owner != old.Owner || !got.StatedAt.Equal(old.StatedAt) || !reflect.DeepEqual(got.Refs, old.Refs) {
			t.Fatalf("bootstrap did not clone provenance: %+v %+v %v", old, got, err)
		}
		if _, err = s.SetImportance(ctx, got.ID, memory.ImportanceNormal, true); !errors.Is(err, memory.ErrStale) {
			t.Fatalf("classified row reclassified automatically: %v", err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, _, err := s.Latest(ctx, repo, "a"); err != nil || got.Importance != memory.ImportanceCritical || got.Overridden != 1 {
		t.Fatalf("reopen lost metadata: %+v %v", got, err)
	}
}

func snapshot(t *testing.T, s *memory.Store, scope string) memory.Snapshot {
	t.Helper()
	x, err := s.SourceSnapshot(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(x.Revision, "p1:") || len(x.Revision) != 67 {
		t.Fatalf("fingerprint: %q", x.Revision)
	}
	return x
}

func saveSnapshotSummary(t *testing.T, s *memory.Store, x memory.Snapshot) memory.Note {
	t.Helper()
	n, err := s.SaveSummary(context.Background(), memory.Note{Repo: repo, Kind: "summary", Key: memory.SummaryKey, Text: "Overview from the snapshot.", Source: "summary"}, x.SummaryID, x.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestHierarchySummaryFingerprintAndCAS(t *testing.T) {
	s, ctx := open(t), context.Background()
	global := add(t, s, memory.Note{Repo: memory.Global, Kind: "instruction", Key: "global", Text: "Global safe rule."})
	low := add(t, s, memory.Note{Kind: "instruction", Key: "low", Text: "Earlier rule."})
	add(t, s, memory.Note{Kind: "fact", Key: "high", Text: "Latest rule."})
	add(t, s, memory.Note{Repo: "unrelated", Kind: "fact", Key: "other", Text: "Other repository."})
	x := snapshot(t, s, repo)
	if len(x.Notes) != 3 {
		t.Fatalf("scoped sources: %+v", x.Notes)
	}
	if g := snapshot(t, s, memory.Global); len(g.Notes) != 1 || g.Notes[0].ID != global.ID || g.Revision == x.Revision {
		t.Fatalf("global scope: %+v", g)
	}
	sum := saveSnapshotSummary(t, s, x)
	if got, ok, err := s.Summary(ctx, repo); err != nil || !ok || got.ID != sum.ID {
		t.Fatalf("fresh summary hidden: %+v %v %v", got, ok, err)
	}
	if now := snapshot(t, s, repo); now.Revision != x.Revision || now.SummaryID != sum.ID {
		t.Fatalf("summary affected source digest: %+v", now)
	}
	if changed, err := s.Changed(ctx, repo); err != nil || changed {
		t.Fatalf("fresh changed: %v %v", changed, err)
	}
	if _, err := s.SaveSummary(ctx, sum, x.SummaryID, x.Revision); !errors.Is(err, memory.ErrStale) {
		t.Fatalf("competing summary accepted: %v", err)
	}
	if _, err := s.Purge(ctx, repo, low.Key); err != nil {
		t.Fatal(err)
	}
	if now := snapshot(t, s, repo); now.Revision == x.Revision {
		t.Fatal("nonmax purge left digest unchanged")
	}
	if _, ok, err := s.Summary(ctx, repo); err != nil || ok {
		t.Fatalf("stale gist returned: %v %v", ok, err)
	}
	if notes, err := s.Current(ctx, repo, "summary"); err != nil || len(notes) != 0 {
		t.Fatalf("Current bypassed summary freshness: %+v %v", notes, err)
	}
	if notes, err := s.Search(ctx, repo, "summary", "Overview", 10); err != nil || len(notes) != 0 {
		t.Fatalf("Search bypassed summary freshness: %+v %v", notes, err)
	}
	if got, ok, err := s.Get(ctx, sum.ID); err != nil || !ok || got.SourceRevision != x.Revision {
		t.Fatalf("stale provenance lost: %+v %v", got, err)
	}
	if _, err := s.SaveSummary(ctx, sum, sum.ID, x.Revision); !errors.Is(err, memory.ErrStale) {
		t.Fatalf("stale sources committed: %v", err)
	}
	before := snapshot(t, s, repo)
	r, err := s.PutReference(ctx, reference("canonical-procedure"), 0)
	if err != nil {
		t.Fatal(err)
	}
	withReference := snapshot(t, s, repo)
	if before.Revision == withReference.Revision {
		t.Fatal("reference omitted from fingerprint")
	}
	r.Procedure = "Changed full payload."
	if _, err = s.PutReference(ctx, r, r.Note.ID); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(t, s, repo); after.Revision == withReference.Revision {
		t.Fatal("procedure update omitted from fingerprint")
	}
	before = snapshot(t, s, repo)
	if _, err = s.Forget(ctx, memory.Global, global.Key, "", "you"); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(t, s, repo); after.Revision == before.Revision {
		t.Fatal("global forget omitted from fingerprint")
	}
}

func TestHierarchyPurgeMustNotReuseNoteIdentity(t *testing.T) {
	s, ctx := open(t), context.Background()
	old := add(t, s, memory.Note{Kind: "instruction", Key: "purged", Text: "Old note identity."})
	if _, err := s.Purge(ctx, repo, old.Key); err != nil {
		t.Fatal(err)
	}
	next := add(t, s, memory.Note{Kind: "instruction", Key: "unrelated", Text: "New unrelated note."})
	if next.ID <= old.ID {
		t.Fatalf("purge reused note identity: old=%d new=%d; Seen and provenance cannot distinguish these notes", old.ID, next.ID)
	}
}

func TestHierarchyIdentityPersistsAcrossReopenAndLegacyInserts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.db")
	s, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first := add(t, s, memory.Note{Kind: "fact", Key: "removed", Text: "A historical identity."})
	if _, err = s.Purge(ctx, repo, first.Key); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err := s.PutReference(ctx, reference("identity-reference"), 0)
	if err != nil || r.Note.ID <= first.ID {
		t.Fatalf("reference reused purged identity after reopen: %+v %v", r, err)
	}
	if _, err = s.Purge(ctx, repo, r.Note.Key); err != nil {
		t.Fatal(err)
	}
	next, err := s.PutReference(ctx, reference("next-reference"), 0)
	if err != nil || next.Note.ID <= r.Note.ID {
		t.Fatalf("reference identity reused: %+v %v", next, err)
	}
	// An older binary can insert without the hierarchy columns or allocator.
	// The watermark trigger must still remember that observed identity after
	// its row is purged. Old binaries themselves cannot guarantee non-reuse.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO notes(id,repo,kind,key,text,source,created_at) VALUES(1000,?,'fact','legacy-insert','Observed legacy identity.','you',?)`, repo, time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Purge(ctx, repo, "legacy-insert"); err != nil {
		t.Fatal(err)
	}
	last := add(t, s, memory.Note{Kind: "fact", Key: "after-legacy", Text: "No reuse after observed legacy insertion."})
	if last.ID <= 1000 {
		t.Fatalf("legacy watermark lost: %d", last.ID)
	}
}

func TestHierarchyConcurrentStoresAllocateDistinctIdentities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	a, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	type result struct {
		note memory.Note
		err  error
	}
	results := make(chan result, 32)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			store := a
			if i%2 == 1 {
				store = b
			}
			_, saved, err := store.Add(context.Background(), memory.Note{Repo: repo, Kind: "fact", Key: "concurrent-" + itoa(int64(i)), Text: "Independent concurrent source.", Source: "you"}, 0)
			results <- result{saved, err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	ids := map[int64]bool{}
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent allocation: %v", result.err)
		}
		if ids[result.note.ID] {
			t.Fatalf("duplicate identity %d", result.note.ID)
		}
		ids[result.note.ID] = true
	}
	if len(ids) != 32 {
		t.Fatalf("allocated %d identities", len(ids))
	}
}

func TestHierarchySummaryExpiryAndLegacy(t *testing.T) {
	s, ctx := open(t), context.Background()
	// Use a generous boundary and poll the clock rather than assuming scheduling
	// can complete a write in a tiny fixed interval.
	expires := time.Now().Add(time.Second)
	add(t, s, memory.Note{Kind: "fact", Key: "temporary", Text: "Temporary fact.", ExpiresAt: expires})
	before := snapshot(t, s, repo)
	sum := saveSnapshotSummary(t, s, before)
	for time.Now().Before(expires) {
		time.Sleep(time.Millisecond)
	}
	after := snapshot(t, s, repo)
	if before.Revision == after.Revision || len(after.Notes) != 0 {
		t.Fatal("clock-only expiry did not invalidate fingerprint")
	}
	if _, ok, err := s.Summary(ctx, repo); err != nil || ok {
		t.Fatalf("expired sources gist served: %v %v", ok, err)
	}
	if _, err := s.SaveSummary(ctx, sum, sum.ID, before.Revision); !errors.Is(err, memory.ErrStale) {
		t.Fatalf("expired snapshot saved: %v", err)
	}
	if changed, err := s.Changed(ctx, repo); err != nil || !changed {
		t.Fatalf("expiry change not detected: %v %v", changed, err)
	}
	legacy := add(t, s, memory.Note{Kind: "summary", Key: memory.SummaryKey, Text: "Legacy generated gist.", Source: "summary"})
	if _, ok, err := s.Summary(ctx, repo); err != nil || ok {
		t.Fatalf("unverified legacy gist served: %v %v", ok, err)
	}
	if changed, err := s.Changed(ctx, repo); err != nil || !changed {
		t.Fatalf("legacy not scheduled for rebuild: %v %v", changed, err)
	}
	if latest, ok, err := s.Latest(ctx, repo, memory.SummaryKey); err != nil || !ok || latest.ID != legacy.ID {
		t.Fatalf("legacy provenance lost: %+v %v", latest, err)
	}
	human := add(t, s, memory.Note{Kind: "summary", Key: memory.SummaryKey, Text: "Explicit human summary.", Source: "you"})
	if got, ok, err := s.Summary(ctx, repo); err != nil || !ok || got.ID != human.ID {
		t.Fatalf("explicit human summary hidden: %+v %v %v", got, ok, err)
	}
}
