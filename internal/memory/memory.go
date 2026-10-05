// Package memory keeps instructions, gotchas and a summary per repo in one
// append-only SQLite table. A change adds a row; the newest row for a
// repo+key is the current version and older rows are history.
package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const (
	Global     = "global"                         // repo value for rules that apply everywhere
	SummaryKey = "summary"                        // the one summary topic per repo
	MaxText    = 800                              // notes stay short so loading them stays cheap
	timeLayout = "2006-01-02T15:04:05.000000000Z" // fixed width, so text order is time order
)

var (
	Kinds   = []string{"instruction", "gotcha", "summary"}
	Sources = []string{"you", "pre-hook", "post-hook", "summary", "import"}
	// ErrStale means the topic changed after a hook read it; the hook's
	// decision was made on old state, so it is dropped rather than saved.
	ErrStale = errors.New("memory: topic changed since it was read")
)

// Any skips the expected-revision check in Add.
const Any int64 = -1

type Note struct {
	ID        int64
	Repo      string // Global, a normalized Git remote, or an absolute path
	Kind      string // instruction, gotcha, summary
	Key       string
	Text      string
	Refs      []string // summary only: keys that are always loaded
	Owner     string
	Source    string
	Forgotten bool
	CreatedAt time.Time
}

type Store struct{ db *sql.DB }

func Path() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kh", "memory.db")
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer per process; other kh processes wait on busy_timeout
	for _, stmt := range []string{
		"PRAGMA busy_timeout=5000",
		"PRAGMA journal_mode=WAL", // spawned agents read while another writes
		"PRAGMA secure_delete=ON", // purge overwrites deleted text instead of leaving it in free pages
		`CREATE TABLE IF NOT EXISTS notes (
            id INTEGER PRIMARY KEY,
            repo TEXT NOT NULL, kind TEXT NOT NULL, key TEXT NOT NULL, text TEXT NOT NULL,
            refs TEXT NOT NULL DEFAULT '', owner TEXT NOT NULL DEFAULT '', source TEXT NOT NULL,
            forgotten INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS notes_topic ON notes(repo, key, created_at, id)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(key, text, content='notes', content_rowid='id')`,
		// Rows are never updated, so insert and delete (purge) keep the index in step.
		`CREATE TRIGGER IF NOT EXISTS notes_ai AFTER INSERT ON notes BEGIN
            INSERT INTO notes_fts(rowid, key, text) VALUES (new.id, new.key, new.text); END`,
		`CREATE TRIGGER IF NOT EXISTS notes_ad AFTER DELETE ON notes BEGIN
            INSERT INTO notes_fts(notes_fts, rowid, key, text) VALUES ('delete', old.id, old.key, old.text); END`,
	} {
		if _, err = db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("memory: %w", err)
		}
	}
	s := &Store{db}
	if err = s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: migrate: %w", err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// latest selects the newest row per topic for one repo plus global.
const latest = `WITH latest AS (
    SELECT n.* FROM notes n WHERE n.repo IN (?, 'global') AND n.id = (
        SELECT m.id FROM notes m WHERE m.repo = n.repo AND m.key = n.key
        ORDER BY m.created_at DESC, m.id DESC LIMIT 1))`

const columns = `id, repo, kind, key, text, refs, owner, source, forgotten, created_at`

func scan(rows *sql.Rows) ([]Note, error) {
	defer rows.Close()
	var out []Note
	for rows.Next() {
		var n Note
		var refs, at string
		if err := rows.Scan(&n.ID, &n.Repo, &n.Kind, &n.Key, &n.Text, &refs, &n.Owner, &n.Source, &n.Forgotten, &at); err != nil {
			return nil, err
		}
		if refs != "" {
			n.Refs = strings.Split(refs, "\n")
		}
		n.CreatedAt, _ = time.Parse(timeLayout, at)
		out = append(out, n)
	}
	return out, rows.Err()
}

func valid(n Note) error {
	if n.Repo == "" || !one(n.Kind, Kinds) || !one(n.Source, Sources) {
		return fmt.Errorf("memory: repo, kind (instruction|gotcha|summary) and source are required")
	}
	if strings.TrimSpace(n.Key) == "" || len(n.Key) > 100 || strings.ContainsAny(n.Key, "\n\r") {
		return fmt.Errorf("memory: key must be one short line")
	}
	if (n.Kind == "summary") != (n.Key == SummaryKey) {
		return fmt.Errorf("memory: only the summary uses the %q key", SummaryKey)
	}
	if !n.Forgotten && strings.TrimSpace(n.Text) == "" {
		return fmt.Errorf("memory: text is required")
	}
	if n.Kind != "summary" && utf8.RuneCountInString(n.Text) > MaxText {
		return fmt.Errorf("memory: keep notes under %d characters", MaxText)
	}
	if Secret(n.Text) || Secret(n.Key) {
		return fmt.Errorf("memory: refusing to store what looks like a secret")
	}
	return nil
}

func one(v string, set []string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

// Add appends a new version of a topic and returns the version it replaced,
// if any. expect is the current row id the caller decided against (0 = the
// topic did not exist); a mismatch returns ErrStale. Only the user may move
// a topic to a different kind, so a gotcha can never overwrite an instruction.
func (s *Store) Add(ctx context.Context, n Note, expect int64) (prev *Note, saved Note, err error) {
	if err = valid(n); err != nil {
		return nil, n, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, n, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+columns+` FROM notes WHERE repo = ? AND key = ?
        ORDER BY created_at DESC, id DESC LIMIT 1`, n.Repo, n.Key)
	if err != nil {
		return nil, n, err
	}
	cur, err := scan(rows)
	if err != nil {
		return nil, n, err
	}
	var curID int64
	if len(cur) == 1 {
		curID = cur[0].ID
		if !cur[0].Forgotten {
			prev = &cur[0]
		}
	}
	if expect != Any && expect != curID {
		return nil, n, ErrStale
	}
	if prev != nil && prev.Kind != n.Kind && n.Source != "you" {
		return nil, n, fmt.Errorf("memory: %s %q cannot be replaced by a %s", prev.Kind, n.Key, n.Kind)
	}
	now := time.Now().UTC()
	// Clocks can tie or step back; never order a new version before the old one.
	if len(cur) == 1 && !now.After(cur[0].CreatedAt) {
		now = cur[0].CreatedAt.Add(time.Nanosecond)
	}
	n.CreatedAt = now
	err = tx.QueryRowContext(ctx, `INSERT INTO notes(repo, kind, key, text, refs, owner, source, forgotten, created_at)
        VALUES (?,?,?,?,?,?,?,?,?) RETURNING id`, n.Repo, n.Kind, n.Key, n.Text, strings.Join(n.Refs, "\n"),
		n.Owner, n.Source, n.Forgotten, now.Format(timeLayout)).Scan(&n.ID)
	if err != nil {
		return nil, n, err
	}
	return prev, n, tx.Commit()
}

// Forget appends a row that removes the topic; history stays until Purge.
func (s *Store) Forget(ctx context.Context, repo, key, owner, source string) (bool, error) {
	cur, ok, err := s.Latest(ctx, repo, key)
	if err != nil || !ok || cur.Forgotten {
		return false, err
	}
	_, _, err = s.Add(ctx, Note{Repo: repo, Kind: cur.Kind, Key: key, Owner: owner, Source: source, Forgotten: true}, cur.ID)
	return err == nil, err
}

// Purge deletes every version of a topic, for things that must not be kept.
func (s *Store) Purge(ctx context.Context, repo, key string) (int64, error) {
	r, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE repo = ? AND key = ?`, repo, key)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

// Latest is the newest row of one topic, forgotten or not.
func (s *Store) Latest(ctx context.Context, repo, key string) (Note, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM notes WHERE repo = ? AND key = ?
        ORDER BY created_at DESC, id DESC LIMIT 1`, repo, key)
	if err != nil {
		return Note{}, false, err
	}
	ns, err := scan(rows)
	if err != nil || len(ns) == 0 {
		return Note{}, false, err
	}
	return ns[0], true, nil
}

// Current lists live notes of one kind ("" = instructions and gotchas) for a
// repo and global, oldest first.
func (s *Store) Current(ctx context.Context, repo, kind string) ([]Note, error) {
	rows, err := s.db.QueryContext(ctx, latest+` SELECT `+columns+` FROM latest
        WHERE forgotten = 0 AND (kind = ? OR (? = '' AND kind != 'summary')) ORDER BY id`, repo, kind, kind)
	if err != nil {
		return nil, err
	}
	return scan(rows)
}

// History lists every version of a topic, newest first.
func (s *Store) History(ctx context.Context, repo, key string) ([]Note, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM notes WHERE repo = ? AND key = ?
        ORDER BY created_at DESC, id DESC`, repo, key)
	if err != nil {
		return nil, err
	}
	return scan(rows)
}

// Summary is this repo's current summary row, if one was written.
func (s *Store) Summary(ctx context.Context, repo string) (Note, bool, error) {
	n, ok, err := s.Latest(ctx, repo, SummaryKey)
	if n.Forgotten {
		return Note{}, false, err
	}
	return n, ok, err
}

// Changed reports whether any instruction or gotcha for the repo is newer
// than its summary, so the summary needs rebuilding.
func (s *Store) Changed(ctx context.Context, repo string) (bool, error) {
	sum, _, err := s.Summary(ctx, repo)
	if err != nil {
		return false, err
	}
	var newest sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT max(id) FROM notes WHERE repo IN (?, 'global') AND kind != 'summary'`, repo).Scan(&newest)
	return newest.Valid && newest.Int64 > sum.ID, err
}

func (s *Store) byIDs(ctx context.Context, ids []int64) ([]Note, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM notes WHERE id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	return scan(rows)
}

// migrate moves records from the earlier discovery/detail tables into notes
// once, then drops the old tables. Preferences and workflows were stated by
// the user, so they become instructions; facts become gotchas.
func (s *Store) migrate() error {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'discovery'`).Scan(&n); err != nil || n == 0 {
		return err
	}
	rows, err := s.db.Query(`SELECT d.scope, d.kind, d.key, d.title, coalesce(t.body, d.summary), d.updated_at
        FROM discovery d LEFT JOIN detail t ON t.discovery_id = d.id ORDER BY d.id`)
	if err != nil {
		return err
	}
	type old struct{ scope, kind, key, title, body, at string }
	var olds []old
	for rows.Next() {
		var o old
		if err := rows.Scan(&o.scope, &o.kind, &o.key, &o.title, &o.body, &o.at); err != nil {
			rows.Close()
			return err
		}
		olds = append(olds, o)
	}
	rows.Close()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, o := range olds {
		repo := Global
		if path, ok := strings.CutPrefix(o.scope, "repo:"); ok {
			repo = path
			if _, err := os.Stat(path); err == nil {
				repo = RepoScope(path)
			}
		}
		kind := "instruction"
		if o.kind == "fact" {
			kind = "gotcha"
		}
		text := truncate(o.title+": "+o.body, MaxText)
		if Secret(text) {
			continue
		}
		at, err := time.Parse("2006-01-02 15:04:05", o.at)
		if err != nil {
			at = time.Now().UTC()
		}
		if _, err := tx.Exec(`INSERT INTO notes(repo, kind, key, text, source, created_at) VALUES (?,?,?,?,?,?)`,
			repo, kind, o.key, text, "you", at.UTC().Format(timeLayout)); err != nil {
			return err
		}
	}
	for _, stmt := range []string{`DROP TABLE detail`, `DROP TABLE discovery`} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
