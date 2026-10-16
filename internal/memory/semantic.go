package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Embedder supplies vectors in input order. Model must identify both the model
// and its endpoint so caches cannot mix vectors from different providers.
// Implementations must honor ctx and apply a bounded provider timeout.
type Embedder interface {
	Model() string
	Embed(context.Context, []string) ([][]float32, error)
}

type semanticState struct {
	embedder Embedder
	model    string
	notice   func(string)
	gate     chan struct{}
	mu       sync.Mutex
	warned   bool
}

// ConfigureSemantic enables lazy semantic retrieval. A nil embedder disables it.
// Vectors are independent of note history and are deleted when notes are purged.
func (s *Store) ConfigureSemantic(embedder Embedder, notice func(string)) error {
	s.semanticMu.Lock()
	defer s.semanticMu.Unlock()
	if embedder == nil {
		s.semantic = nil
		return nil
	}
	model := strings.TrimSpace(embedder.Model())
	if model == "" {
		return fmt.Errorf("memory: embedding model identity is empty")
	}
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS notes_vectors (
            note_id INTEGER NOT NULL, model TEXT NOT NULL, content_hash TEXT NOT NULL,
            vector BLOB NOT NULL, PRIMARY KEY(note_id, model))`,
		`CREATE TRIGGER IF NOT EXISTS notes_vectors_ad AFTER DELETE ON notes BEGIN
            DELETE FROM notes_vectors WHERE note_id = old.id; END`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("memory: configure semantic retrieval: %w", err)
		}
	}
	s.semantic = &semanticState{embedder: embedder, model: model, notice: notice, gate: make(chan struct{}, 1)}
	return nil
}

func (s *Store) semanticConfig() *semanticState {
	s.semanticMu.Lock()
	defer s.semanticMu.Unlock()
	return s.semantic
}

// semanticNotice never includes provider errors, which may contain request text
// or credentials. Warn only once until retrieval succeeds again.
func (s *Store) semanticNotice(_ error) {
	state := s.semanticConfig()
	if state == nil {
		return
	}
	state.mu.Lock()
	notify := !state.warned && state.notice != nil
	state.warned = true
	state.mu.Unlock()
	if notify {
		state.notice("Semantic retrieval unavailable; using keyword search.")
	}
}

// SemanticSearch returns live, non-summary notes from repo and Global. Cosine
// similarity must be at least 0.35, a conservative floor against unrelated notes.
// Each search backfills at most 64 missing vectors, in batches of at most 16;
// subsequent searches gradually backfill larger stores without a network burst.
func (s *Store) SemanticSearch(ctx context.Context, repo, kind, query string, limit int) ([]Note, error) {
	return s.SemanticSearchBetween(ctx, repo, kind, query, limit, time.Time{}, time.Time{})
}

// SemanticSearchBetween applies inclusive stated-at bounds before embedding or
// ranking. Zero bounds are unbounded; superseded and forgotten versions never
// participate.
func (s *Store) SemanticSearchBetween(ctx context.Context, repo, kind, query string, limit int, since, until time.Time) ([]Note, error) {
	if limit <= 0 || strings.TrimSpace(query) == "" || kind == "summary" {
		return nil, nil
	}
	state := s.semanticConfig()
	if state == nil {
		return nil, nil
	}
	if Secret(query) {
		return nil, fmt.Errorf("memory: refusing to embed what looks like a secret")
	}
	select {
	case state.gate <- struct{}{}:
		defer func() { <-state.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	notes, err := s.Current(ctx, repo, kind)
	if err != nil {
		return nil, err
	}
	eligible := notes[:0]
	for _, n := range notes {
		if semanticEligible(n, since, until) {
			eligible = append(eligible, n)
		}
	}
	if len(eligible) == 0 {
		return nil, nil
	}
	// Prefer recent notes for the bounded lazy backfill. This also makes cache
	// warming deterministic independently of database query order.
	sort.Slice(eligible, func(i, j int) bool { return semanticNewer(eligible[i], eligible[j]) })
	vectors, err := s.semanticVectors(ctx, state.model, repo, kind, eligible)
	if err != nil {
		return nil, err
	}
	queryVectors, err := state.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("memory: embed query: %w", err)
	}
	if len(queryVectors) != 1 || !validVector(queryVectors[0], 0) {
		return nil, fmt.Errorf("memory: invalid query embedding")
	}
	queryVector := queryVectors[0]
	dim := len(queryVector)
	var missing []Note
	for _, n := range eligible {
		cached, ok := vectors[n.ID]
		if !ok || cached.hash != semanticHash(n) || !validVector(cached.vector, dim) {
			if len(missing) < 64 {
				missing = append(missing, n)
			}
			delete(vectors, n.ID)
			continue
		}
	}
	for start := 0; start < len(missing); start += 16 {
		end := min(start+16, len(missing))
		batch := missing[start:end]
		texts := make([]string, len(batch))
		for i, n := range batch {
			texts[i] = semanticText(n)
		}
		embedded, err := state.embedder.Embed(ctx, texts)
		if err != nil {
			return nil, fmt.Errorf("memory: embed notes: %w", err)
		}
		if len(embedded) != len(batch) {
			return nil, fmt.Errorf("memory: embedding count mismatch")
		}
		for _, v := range embedded {
			if !validVector(v, dim) {
				return nil, fmt.Errorf("memory: invalid note embedding")
			}
		}
		for i, n := range batch {
			encoded, err := json.Marshal(embedded[i])
			if err != nil {
				return nil, err
			}
			hash := semanticHash(n)
			// INSERT SELECT prevents a purge racing network work from leaving an
			// orphan vector. No rows or database locks are held during Embed.
			_, err = s.db.ExecContext(ctx, `INSERT INTO notes_vectors(note_id,model,content_hash,vector)
                SELECT id, ?, ?, ? FROM notes WHERE id = ?
                ON CONFLICT(note_id,model) DO UPDATE SET content_hash=excluded.content_hash,vector=excluded.vector`, state.model, hash, encoded, n.ID)
			if err != nil {
				return nil, err
			}
			vectors[n.ID] = semanticVector{hash, embedded[i]}
		}
	}
	// Network work may have overlapped a replacement, forget, or purge. Read
	// live versions again so those notes cannot enter the ranked result.
	live, err := s.Current(ctx, repo, kind)
	if err != nil {
		return nil, err
	}
	type match struct {
		note  Note
		score float64
	}
	var matches []match
	for _, n := range live {
		v, ok := vectors[n.ID]
		if !ok || v.hash != semanticHash(n) || !semanticEligible(n, since, until) {
			continue
		}
		if score := cosine(queryVector, v.vector); score >= 0.35 {
			matches = append(matches, match{n, score})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return semanticNewer(matches[i].note, matches[j].note)
	})
	out := make([]Note, 0, min(limit, len(matches)))
	for _, m := range matches[:min(limit, len(matches))] {
		out = append(out, m.note)
	}
	state.mu.Lock()
	state.warned = false
	state.mu.Unlock()
	return out, nil
}

func semanticEligible(n Note, since, until time.Time) bool {
	return !n.Forgotten && n.Kind != "summary" && !Secret(n.Key) && !Secret(n.Text) && !Secret(n.ContextSummary) &&
		(since.IsZero() || !n.StatedAt.Before(since)) && (until.IsZero() || !n.StatedAt.After(until))
}

func semanticNewer(a, b Note) bool {
	if !a.StatedAt.Equal(b.StatedAt) {
		return a.StatedAt.After(b.StatedAt)
	}
	return a.ID > b.ID
}

func semanticText(n Note) string {
	return n.Key + "\n" + n.Text + "\n" + n.ContextSummary
}

func semanticHash(n Note) string {
	h := sha256.Sum256([]byte(semanticText(n)))
	return hex.EncodeToString(h[:])
}

type semanticVector struct {
	hash   string
	vector []float32
}

func (s *Store) semanticVectors(ctx context.Context, model, repo, kind string, eligible []Note) (map[int64]semanticVector, error) {
	allowed := make(map[int64]bool, len(eligible))
	for _, n := range eligible {
		allowed[n.ID] = true
	}
	rows, err := s.db.QueryContext(ctx, latest+` SELECT v.note_id,v.content_hash,v.vector
        FROM notes_vectors v JOIN latest l ON l.id=v.note_id
        WHERE v.model=? AND l.forgotten=0 AND (l.kind=? OR (?='' AND l.kind!='summary'))`, repo, model, kind, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]semanticVector)
	for rows.Next() {
		var id int64
		var v semanticVector
		var encoded []byte
		if err := rows.Scan(&id, &v.hash, &encoded); err != nil {
			return nil, err
		}
		if !allowed[id] {
			continue
		}
		if err := json.Unmarshal(encoded, &v.vector); err != nil {
			continue
		} // rebuild corrupt cache entries
		out[id] = v
	}
	return out, rows.Err()
}

func validVector(v []float32, dim int) bool {
	if len(v) == 0 || (dim != 0 && len(v) != dim) {
		return false
	}
	var norm float64
	for _, x := range v {
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return false
		}
		norm += f * f
	}
	return norm > 0
}

func cosine(a, b []float32) float64 {
	var dot, aa, bb float64
	for i, x := range a {
		y := float64(b[i])
		xx := float64(x)
		dot += xx * y
		aa += xx * xx
		bb += y * y
	}
	return dot / math.Sqrt(aa*bb)
}
