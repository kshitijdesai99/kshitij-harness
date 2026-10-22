package test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"kh/internal/memory"
)

func reference(name string) memory.Reference {
	return memory.Reference{Note: memory.Note{Repo: repo, Kind: "reference", Key: name, Text: "Repair safely", Source: "you"}, Aliases: []string{"washer repair"}, Procedure: strings.Repeat("Step safely.\n", 200), Assets: []memory.Asset{{Name: "sound.sh", Interpreter: "sh", Source: "printf cool"}}}
}

func TestReferenceDescriptionIsNotTruncatedByAliasLabel(t *testing.T) {
	s, ctx := open(t), context.Background()
	r := reference("preserve-description")
	r.Note.Text = "Repair guide\nAliases: washer maintenance\nUnplug before inspecting."
	r.Aliases = nil
	saved, err := s.PutReference(ctx, r, 0)
	if err != nil || saved.Note.Text != r.Note.Text {
		t.Fatalf("description changed: %q %v", saved.Note.Text, err)
	}
	r = saved
	r.Aliases = []string{"first alias"}
	next, err := s.PutReference(ctx, r, saved.Note.ID)
	if err != nil || next.Note.Text != saved.Note.Text+"\nAliases: first alias" {
		t.Fatalf("description changed: %q %v", next.Note.Text, err)
	}
	r = next
	r.Aliases = []string{"second alias"}
	final, err := s.PutReference(ctx, r, next.Note.ID)
	if err != nil || final.Note.Text != saved.Note.Text+"\nAliases: second alias" {
		t.Fatalf("update changed description: %q %v", final.Note.Text, err)
	}
}

func TestReferenceRoundTripVersionsAndScope(t *testing.T) {
	s, ctx := open(t), context.Background()
	first, err := s.PutReference(ctx, reference("repair"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.ReferenceID == "" || first.Version != 1 || !strings.Contains(first.Note.Text, "washer repair") || len(first.Note.Text) > 800 {
		t.Fatalf("metadata: %+v", first)
	}
	got, ok, err := s.Reference(ctx, repo, first.Note.ID)
	if err != nil || !ok || got.Procedure != first.Procedure || len(got.Assets) != 1 || got.Assets[0] != first.Assets[0] {
		t.Fatalf("roundtrip: %+v %v %v", got, ok, err)
	}
	if _, ok, err := s.Reference(ctx, "elsewhere", first.Note.ID); err != nil || ok {
		t.Fatalf("scope: %v %v", ok, err)
	}
	matches, err := s.Search(ctx, repo, "reference", "washer", 10)
	if err != nil || len(matches) != 1 {
		t.Fatalf("alias index: %+v %v", matches, err)
	}
	next := first
	next.Procedure = "Replacement"
	next.Aliases = []string{"new alias"}
	second, err := s.PutReference(ctx, next, first.Note.ID)
	if err != nil || second.ReferenceID != first.ReferenceID || second.Version != 2 || second.Note.Overridden != first.Note.ID {
		t.Fatalf("version: %+v %v", second, err)
	}
	if _, err = s.PutReference(ctx, next, first.Note.ID); !errors.Is(err, memory.ErrStale) {
		t.Fatalf("CAS: %v", err)
	}
	old, ok, err := s.Reference(ctx, repo, first.Note.ID)
	if err != nil || !ok || old.Procedure != first.Procedure {
		t.Fatalf("history: %+v %v", old, err)
	}
	if _, ok, err = s.LatestReference(ctx, repo, "washer repair"); err != nil || ok {
		t.Fatalf("superseded alias resolved: %v %v", ok, err)
	}
	current, ok, err := s.LatestReference(ctx, repo, "new alias")
	if err != nil || !ok || current.Note.ID != second.Note.ID {
		t.Fatalf("latest alias: %+v %v", current, err)
	}
	if _, _, err = s.Add(ctx, reference("bare").Note, 0); err == nil {
		t.Fatal("bare reference accepted")
	}
	bareTombstone := reference("bare").Note
	bareTombstone.Forgotten = true
	if _, _, err = s.Add(ctx, bareTombstone, 0); err == nil {
		t.Fatal("bare reference tombstone accepted outside Forget")
	}
	history, _ := s.History(ctx, repo, "repair")
	if len(history) != 2 {
		t.Fatalf("failed writes left rows: %d", len(history))
	}
}

func TestReferenceResolutionForgetAndPurge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	global := reference("repair")
	global.Note.Repo = memory.Global
	g, err := s.PutReference(ctx, global, 0)
	if err != nil {
		t.Fatal(err)
	}
	local, err := s.PutReference(ctx, reference("repair"), 0)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.LatestReference(ctx, repo, "washer repair")
	if err != nil || !ok || got.Note.ID != local.Note.ID {
		t.Fatalf("precedence: %+v %v", got, err)
	}
	other := reference("other")
	if _, err = s.PutReference(ctx, other, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.LatestReference(ctx, repo, "washer repair"); err == nil {
		t.Fatal("ambiguous alias accepted")
	}
	if ok, err = s.Forget(ctx, repo, "repair", "", "you"); err != nil || !ok {
		t.Fatalf("forget: %v %v", ok, err)
	}
	if got, ok, err = s.LatestReference(ctx, repo, "repair"); err != nil || !ok || got.Note.ID != g.Note.ID {
		t.Fatalf("global fallback: %+v %v", got, err)
	}
	if _, ok, err = s.Reference(ctx, repo, local.Note.ID); err != nil || !ok {
		t.Fatalf("forgot history: %v %v", ok, err)
	}
	forgotten, _, err := s.Latest(ctx, repo, "repair")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := s.PutReference(ctx, reference("repair"), forgotten.ID)
	if err != nil || restored.ReferenceID != local.ReferenceID || restored.Version != 2 || restored.Note.Overridden != forgotten.ID {
		t.Fatalf("restore after forget: %+v %v", restored, err)
	}
	if _, err = s.Purge(ctx, repo, "repair"); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	err = db.QueryRow(`SELECT count(*) FROM reference_payloads WHERE note_id=?`, local.Note.ID).Scan(&count)
	if err != nil || count != 0 {
		t.Fatalf("orphan payload: %d %v", count, err)
	}
	if _, ok, err = s.Reference(ctx, memory.Global, g.Note.ID); err != nil || !ok {
		t.Fatalf("global scope: %v %v", ok, err)
	}
}

func TestReferenceValidationAndPortableIdentity(t *testing.T) {
	s, ctx := open(t), context.Background()
	cases := []func(*memory.Reference){
		func(r *memory.Reference) { r.Procedure = strings.Repeat("x", 128*1024+1) },
		func(r *memory.Reference) { r.Procedure = "api_key=abcdef123456" },
		func(r *memory.Reference) { r.Assets[0].Name = "../escape.sh" },
		func(r *memory.Reference) { r.Assets[0].Name = "/escape.sh" },
		func(r *memory.Reference) { r.Assets[0].Name = "a\\escape.sh" },
		func(r *memory.Reference) { r.Assets[0].Interpreter = "sh -c" },
		func(r *memory.Reference) { r.Assets[0].Source = "password=abcdef123456" },
		func(r *memory.Reference) { r.Assets[0].Source = strings.Repeat("x", 128*1024+1) },
		func(r *memory.Reference) { r.Aliases = []string{"api_key=abcdef123456"} },
		func(r *memory.Reference) { r.Aliases = []string{strings.Repeat("x", 801)} },
		func(r *memory.Reference) { r.ReferenceID = "not-uuid" },
		func(r *memory.Reference) { r.Assets = append(r.Assets, r.Assets[0]) },
		func(r *memory.Reference) { r.Assets = make([]memory.Asset, memory.MaxAssets+1) },
		func(r *memory.Reference) { r.Note.Text = strings.Repeat("x", memory.MaxText) },
	}
	for i, change := range cases {
		r := reference("invalid")
		change(&r)
		if _, err := s.PutReference(ctx, r, 0); err == nil {
			t.Errorf("accepted invalid case %d", i)
		}
	}
	if notes, err := s.Current(ctx, repo, "reference"); err != nil || len(notes) != 0 {
		t.Fatalf("invalid writes leaked: %+v %v", notes, err)
	}
	r := reference("portable")
	r.ReferenceID = "4fb198b4-b2b1-4c20-8619-78b147c2aec3"
	r.Procedure = strings.Repeat("x", 128*1024)
	first, err := s.PutReference(ctx, r, 0)
	if err != nil {
		t.Fatal(err)
	}
	copy := first
	copy.Note.Repo = "another"
	copy.Note.ID = 0
	copy.Note.StatedAt = first.Note.StatedAt
	imported, err := s.PutReference(ctx, copy, 0)
	if err != nil || imported.ReferenceID != first.ReferenceID {
		t.Fatalf("portable import: %+v %v", imported, err)
	}
	changed := first
	changed.ReferenceID = "ee30acb0-7722-4b09-aee4-5d900403d046"
	if _, err = s.PutReference(ctx, changed, first.Note.ID); err == nil {
		t.Fatal("changed immutable identity")
	}
}

func TestReferenceEncodingAndVersionOverflow(t *testing.T) {
	s, ctx := open(t), context.Background()
	for _, r := range []memory.Reference{
		{Note: reference("encoding").Note, Procedure: "safe", Aliases: []string{"x\xff"}},
		{Note: reference("encoding-assets").Note, Procedure: "safe", Assets: []memory.Asset{{Name: "x\xff.sh", Interpreter: "sh", Source: "echo safe"}, {Name: "x\xfe.sh", Interpreter: "sh", Source: "echo safe"}}},
	} {
		if _, err := s.PutReference(ctx, r, 0); err == nil {
			t.Error("accepted invalid UTF-8 metadata")
		}
	}
	r := reference("overflow")
	r.Note.Source = "import"
	r.Version = int(^uint(0) >> 1)
	first, err := s.PutReference(ctx, r, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PutReference(ctx, first, first.Note.ID); err == nil {
		t.Fatal("accepted overflowing version")
	}
	history, err := s.History(ctx, repo, "overflow")
	if err != nil || len(history) != 1 {
		t.Fatalf("overflow leaked note: %+v %v", history, err)
	}
}

func TestReferenceOriginRoundTripAndReimport(t *testing.T) {
	s, ctx := open(t), context.Background()
	origin := reference("original").Note
	origin.ID, origin.Overridden = 82, 70
	origin.Owner = "original owner"
	origin.ContextSummary = strings.Repeat("x", memory.MaxText)
	origin.CreatedAt = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	origin.StatedAt = origin.CreatedAt.Add(-time.Hour)
	r := reference("imported")
	r.Note.Source, r.Note.ContextSummary = "import", origin.ContextSummary
	r.Origin, r.Version = &origin, 7
	first, err := s.PutReference(ctx, r, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Reference(ctx, repo, first.Note.ID)
	if err != nil || !ok || !reflect.DeepEqual(got.Origin, &origin) || got.Note.Source != "import" || got.Note.ContextSummary != origin.ContextSummary || got.Version != 7 {
		t.Fatalf("origin roundtrip: %+v %v %v", got, ok, err)
	}
	// Export/import preserves the first origin independently of new local metadata.
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var imported memory.Reference
	if err = json.Unmarshal(data, &imported); err != nil {
		t.Fatal(err)
	}
	imported.Note.ID, imported.Note.Repo, imported.Note.Owner = 0, "second repo", "second owner"
	imported, err = s.PutReference(ctx, imported, 0)
	if err != nil {
		t.Fatal(err)
	}
	again, ok, err := s.Reference(ctx, "second repo", imported.Note.ID)
	if err != nil || !ok || !reflect.DeepEqual(again.Origin, &origin) || again.Note.ContextSummary != origin.ContextSummary {
		t.Fatalf("reimport: %+v %v %v", again, ok, err)
	}
}

func TestReferenceOriginValidation(t *testing.T) {
	s, ctx := open(t), context.Background()
	for _, change := range []func(*memory.Note){
		func(n *memory.Note) { n.Source = "unknown" },
		func(n *memory.Note) { n.ContextSummary = strings.Repeat("x", 801) },
		func(n *memory.Note) { n.Text = "bad\xff" },
		func(n *memory.Note) { n.Owner = "bad\xff" },
		func(n *memory.Note) { n.Refs = []string{"bad\xff"} },
		func(n *memory.Note) { n.Owner = "api_key=abcdef123456" },
		func(n *memory.Note) { n.Repo = "password=abcdef123456" },
		func(n *memory.Note) { n.Refs = []string{"password=abcdef123456"} },
		func(n *memory.Note) { n.Owner = strings.Repeat("x", 100000) },
		func(n *memory.Note) { n.Owner = strings.Repeat("\x00", 3000) }, // JSON escaping exceeds the serialized limit
		func(n *memory.Note) { n.Refs = make([]string, 100000) },
	} {
		r := reference("invalid-origin")
		n := r.Note
		change(&n)
		r.Origin = &n
		if _, err := s.PutReference(ctx, r, 0); err == nil {
			t.Error("accepted invalid origin")
		}
	}
	if ns, err := s.Current(ctx, repo, "reference"); err != nil || len(ns) != 0 {
		t.Fatalf("invalid origin leaked: %+v %v", ns, err)
	}
}

func TestReferenceCreationVersionAndIdentityCollision(t *testing.T) {
	s, ctx := open(t), context.Background()
	r := reference("identity-owner")
	r.Version = 7
	first, err := s.PutReference(ctx, r, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 {
		t.Errorf("ordinary initial version = %d, want 1", first.Version)
	}
	r = reference("different-key")
	r.ReferenceID = first.ReferenceID
	if _, err = s.PutReference(ctx, r, 0); err == nil {
		t.Fatal("same-scope identity collision accepted")
	}
	if ns, err := s.History(ctx, repo, r.Note.Key); err != nil || len(ns) != 0 {
		t.Fatalf("collision orphan: %+v %v", ns, err)
	}
	// Historical IDs remain claimed even after forgetting a topic.
	if _, err = s.Forget(ctx, repo, first.Note.Key, "", "you"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PutReference(ctx, r, 0); err == nil {
		t.Fatal("forgotten identity collision accepted")
	}
	r.Note.Repo = memory.Global
	if _, err = s.PutReference(ctx, r, 0); err != nil {
		t.Fatalf("cross-scope identity rejected: %v", err)
	}
}

func TestReferenceOldPayloadSchemaUpgrade(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.PutReference(ctx, reference("legacy"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the already-deployed table shape without deleting its payloads.
	_, err = db.Exec(`CREATE TABLE old_payloads (note_id INTEGER PRIMARY KEY, reference_id TEXT NOT NULL, version INTEGER NOT NULL, aliases TEXT NOT NULL, procedure TEXT NOT NULL, assets TEXT NOT NULL);
	INSERT INTO old_payloads SELECT note_id,reference_id,version,aliases,procedure,assets FROM reference_payloads;
	DROP TRIGGER notes_reference_ad;
	DROP TABLE reference_payloads;
	ALTER TABLE old_payloads RENAME TO reference_payloads;`)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s, err = memory.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.Reference(ctx, repo, first.Note.ID)
		if err != nil || !ok || got.Origin != nil || got.Procedure != first.Procedure || !reflect.DeepEqual(got.Assets, first.Assets) {
			t.Fatalf("migration lost payload: %+v %v %v", got, ok, err)
		}
		if i == 1 {
			latest, ok, err := s.LatestReference(ctx, repo, first.Note.Key)
			if err != nil || !ok || !reflect.DeepEqual(latest.Origin, &first.Note) {
				t.Fatalf("reopen lost migrated origin: %+v %v %v", latest, ok, err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got.Origin = &first.Note
		next, err := s.PutReference(ctx, got, got.Note.ID)
		if err != nil {
			t.Fatal(err)
		}
		saved, ok, err := s.Reference(ctx, repo, next.Note.ID)
		if err != nil || !ok || !reflect.DeepEqual(saved.Origin, got.Origin) {
			t.Fatalf("migrated origin: %+v %v %v", saved, ok, err)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferencePayloadFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	add(t, s, memory.Note{Kind: "fact", Key: "preserved", Text: "Existing note"})
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TRIGGER reject_reference BEFORE INSERT ON reference_payloads BEGIN SELECT RAISE(ABORT, 'test payload failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PutReference(ctx, reference("rollback"), 0); err == nil {
		t.Fatal("injected failure succeeded")
	}
	history, err := s.History(ctx, repo, "rollback")
	if err != nil || len(history) != 0 {
		t.Fatalf("orphan note: %+v %v", history, err)
	}
	var count int
	err = db.QueryRow(`SELECT count(*) FROM reference_payloads`).Scan(&count)
	if err != nil || count != 0 {
		t.Fatalf("orphan payload: %d %v", count, err)
	}
	if _, ok, err := s.Latest(ctx, repo, "preserved"); err != nil || !ok {
		t.Fatalf("existing data lost: %v %v", ok, err)
	}
}
