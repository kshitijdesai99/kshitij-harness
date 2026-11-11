package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	ImportanceNormal   = "normal"
	ImportanceCritical = "critical"
)

// Active reports eligibility at now, not whether this is the latest version.
// Expiry is exclusive: a note expiring exactly at now is inactive.
func (n Note) Active(now time.Time) bool {
	return !n.Forgotten && (n.ExpiresAt.IsZero() || n.ExpiresAt.After(now))
}

func expiryText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}

// migrateHierarchy is additive; old rows intentionally remain unclassified.
func (s *Store) migrateHierarchy() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE notes SET id=id WHERE 0`); err != nil {
		return err
	}
	rows, err := tx.Query(`PRAGMA table_info(notes)`)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def sql.NullString
		if err = rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		present[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, name := range []string{"importance", "expires_at", "source_revision"} {
		if !present[name] {
			if _, err = tx.Exec(`ALTER TABLE notes ADD COLUMN ` + name + ` TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
		}
	}
	// A durable high-water mark prevents purged IDs from being reused. It
	// contains no note content. The trigger observes inserts by legacy writers,
	// though those older binaries still cannot themselves guarantee no reuse.
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS notes_sequence (singleton INTEGER PRIMARY KEY CHECK(singleton=1), high_water INTEGER NOT NULL CHECK(high_water>=0))`,
		`INSERT INTO notes_sequence(singleton,high_water) SELECT 1,coalesce(max(id),0) FROM notes WHERE true ON CONFLICT(singleton) DO UPDATE SET high_water=max(high_water,excluded.high_water)`,
		`CREATE TRIGGER IF NOT EXISTS notes_sequence_ai AFTER INSERT ON notes BEGIN UPDATE notes_sequence SET high_water=max(high_water,new.id) WHERE singleton=1; END`,
	} {
		if _, err = tx.Exec(stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// allocateNoteID reserves the writer before topic reads as well as allocating
// durable identity, so concurrent stores wait rather than racing lock upgrades.
func allocateNoteID(ctx context.Context, tx *sql.Tx) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `UPDATE notes_sequence SET high_water=max(high_water,(SELECT coalesce(max(id),0) FROM notes))+1 WHERE singleton=1 RETURNING high_water`).Scan(&id)
	return id, err
}

// currentSummaries routes generic current/search queries through the same
// freshness gate as recall. Raw Latest/Get/History remain available to inspect.
func (s *Store) currentSummaries(ctx context.Context, repo string) ([]Note, error) {
	scopes := []string{repo}
	if repo != Global {
		scopes = append(scopes, Global)
	}
	var notes []Note
	for _, scope := range scopes {
		n, ok, err := s.Summary(ctx, scope)
		if err != nil {
			return nil, err
		}
		if ok {
			notes = append(notes, n)
		}
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].ID < notes[j].ID })
	return notes, nil
}

// SetImportance appends a metadata-only version if id is still current. It
// preserves statement provenance rather than pretending classification is a new
// user statement. onlyUnclassified is the conservative automatic-bootstrap gate.
func (s *Store) SetImportance(ctx context.Context, id int64, tier string, onlyUnclassified bool) (Note, error) {
	if tier != ImportanceNormal && tier != ImportanceCritical {
		return Note{}, fmt.Errorf("memory: importance must be normal or critical")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Note{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE notes SET id=id WHERE 0`); err != nil {
		return Note{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+columns+` FROM notes WHERE id=?`, id)
	if err != nil {
		return Note{}, err
	}
	notes, err := scan(rows)
	if err != nil {
		return Note{}, err
	}
	if len(notes) != 1 {
		return Note{}, ErrStale
	}
	n := notes[0]
	if n.Forgotten || n.Kind == "reference" || n.Kind == "summary" {
		return Note{}, ErrStale
	}
	if onlyUnclassified && (!n.Active(time.Now()) || n.Importance != "" || !one(n.Source, []string{"you", "pre-hook", "post-hook"}) || strings.TrimSpace(n.Owner) == "" || n.StatedAt.IsZero() || strings.TrimSpace(n.ContextSummary) == "") {
		return Note{}, ErrStale
	}
	n.Importance = tier
	_, saved, err := s.addTx(ctx, tx, n, id)
	if err != nil {
		return Note{}, err
	}
	if err = tx.Commit(); err != nil {
		return Note{}, err
	}
	return saved, nil
}

// Snapshot is an atomic view of all active canonical source notes in scope.
// SummaryID is the latest summary revision even if that summary is stale.
type Snapshot struct {
	Notes     []Note
	Revision  string
	SummaryID int64
}

// SourceSnapshot includes repo and global, or only global for Global. Derived
// summaries never enter their own source set; canonical references do.
func (s *Store) SourceSnapshot(ctx context.Context, repo string) (Snapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Snapshot{}, err
	}
	defer tx.Rollback()
	snap, err := sourceSnapshotTx(ctx, tx, repo, time.Now())
	if err != nil {
		return Snapshot{}, err
	}
	if err = tx.Commit(); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

func sourceSnapshotTx(ctx context.Context, tx *sql.Tx, repo string, now time.Time) (Snapshot, error) {
	rows, err := tx.QueryContext(ctx, latest+` SELECT `+columns+` FROM latest WHERE kind!='summary' AND forgotten=0 AND (expires_at='' OR expires_at>?) ORDER BY repo, id`, repo, expiryText(now))
	if err != nil {
		return Snapshot{}, err
	}
	notes, err := scan(rows)
	if err != nil {
		return Snapshot{}, err
	}
	scopes := []string{Global}
	if repo != Global {
		scopes = append(scopes, repo)
	}
	sort.Strings(scopes)
	h := sha256.New()
	for _, scope := range scopes {
		fmt.Fprintf(h, "scope:%d:%s\n", len(scope), scope)
	}
	for _, n := range notes {
		// CreatedAt disambiguates SQLite row IDs reused after a purge. Every
		// canonical version participates, not just selected inputs or max(id).
		fmt.Fprintf(h, "note:%d:%s:%d:%s\n", len(n.Repo), n.Repo, n.ID, expiryText(n.CreatedAt))
	}
	snap := Snapshot{Notes: notes, Revision: "p1:" + hex.EncodeToString(h.Sum(nil))}
	err = tx.QueryRowContext(ctx, `SELECT id FROM notes WHERE repo=? AND key=? ORDER BY created_at DESC,id DESC LIMIT 1`, repo, SummaryKey).Scan(&snap.SummaryID)
	if err != nil && err != sql.ErrNoRows {
		return Snapshot{}, err
	}
	return snap, nil
}

// SourceRevision fingerprints the complete active canonical source set.
func (s *Store) SourceRevision(ctx context.Context, repo string) (string, error) {
	snap, err := s.SourceSnapshot(ctx, repo)
	return snap.Revision, err
}

// SaveSummary reserves the writer before comparing sources and appending the
// summary. A concurrent edit, expiry, purge or competing summary yields ErrStale.
func (s *Store) SaveSummary(ctx context.Context, n Note, expectedSummaryID int64, sourceRevision string) (Note, error) {
	if n.Kind != "summary" || n.Key != SummaryKey || sourceRevision == "" {
		return Note{}, fmt.Errorf("memory: summary and source revision are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Note{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE notes SET id=id WHERE 0`); err != nil {
		return Note{}, err
	}
	snap, err := sourceSnapshotTx(ctx, tx, n.Repo, time.Now())
	if err != nil {
		return Note{}, err
	}
	if sourceRevision != snap.Revision || expectedSummaryID != snap.SummaryID {
		return Note{}, ErrStale
	}
	n.SourceRevision = sourceRevision
	_, saved, err := s.addTx(ctx, tx, n, expectedSummaryID)
	if err != nil {
		return Note{}, err
	}
	if err = tx.Commit(); err != nil {
		return Note{}, err
	}
	return saved, nil
}

// Summary never serves a stale source-backed gist. Unverified legacy generated
// summaries are hidden; explicit human-authored legacy summaries remain readable.
func (s *Store) Summary(ctx context.Context, repo string) (Note, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Note{}, false, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+columns+` FROM notes WHERE repo=? AND key=? ORDER BY created_at DESC,id DESC LIMIT 1`, repo, SummaryKey)
	if err != nil {
		return Note{}, false, err
	}
	notes, err := scan(rows)
	if err != nil || len(notes) == 0 {
		return Note{}, false, err
	}
	n := notes[0]
	now := time.Now()
	if !n.Active(now) || (n.SourceRevision == "" && n.Source != "you") {
		return Note{}, false, nil
	}
	if n.SourceRevision != "" {
		snap, err := sourceSnapshotTx(ctx, tx, repo, now)
		if err != nil {
			return Note{}, false, err
		}
		if snap.Revision != n.SourceRevision {
			return Note{}, false, nil
		}
	}
	return n, true, nil
}

// Changed compares the complete active source set, not insertion order. Legacy
// source-less summaries always require a rebuild.
func (s *Store) Changed(ctx context.Context, repo string) (bool, error) {
	snap, err := s.SourceSnapshot(ctx, repo)
	if err != nil {
		return false, err
	}
	if snap.SummaryID == 0 {
		return len(snap.Notes) > 0, nil
	}
	n, ok, err := s.Get(ctx, snap.SummaryID)
	if err != nil {
		return false, err
	}
	return !ok || !n.Active(time.Now()) || n.SourceRevision == "" || n.SourceRevision != snap.Revision, nil
}
