package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

// SearchOptions filters statement timestamps, inclusively. Order is relevance
// (the default), newest, or oldest. Historical versions remain in History.
type SearchOptions struct {
	Since, Until time.Time
	Order        string
}

// Search combines lexical and semantic rankings of current, scoped notes.
// An unconfigured or unavailable embedder falls back to lexical search.
func (s *Store) Search(ctx context.Context, repo, kind, query string, limit int) ([]Note, error) {
	return s.SearchWithOptions(ctx, repo, kind, query, limit, SearchOptions{})
}

// SearchTrace explains one retrieval without retaining query text or provider errors.
type SearchTrace struct {
	LexicalIDs, SemanticIDs, SelectedIDs []int64
	SemanticEnabled, SemanticFallback    bool
}

func (s *Store) SearchWithOptions(ctx context.Context, repo, kind, query string, limit int, opts SearchOptions) ([]Note, error) {
	return s.search(ctx, repo, kind, query, limit, opts, nil, nil, nil)
}

func (s *Store) SearchWithTrace(ctx context.Context, repo, kind, query string, limit int, opts SearchOptions) ([]Note, SearchTrace, error) {
	var trace SearchTrace
	notes, err := s.search(ctx, repo, kind, query, limit, opts, nil, nil, &trace)
	return notes, trace, err
}

func (s *Store) search(ctx context.Context, repo, kind, query string, limit int, opts SearchOptions, quotas map[string]int, excluded map[int64]bool, trace *SearchTrace) ([]Note, error) {
	if opts.Order != "" && opts.Order != "relevance" && opts.Order != "newest" && opts.Order != "oldest" {
		return nil, fmt.Errorf("memory: order must be relevance, newest or oldest")
	}
	if !opts.Since.IsZero() && !opts.Until.IsZero() && opts.Since.After(opts.Until) {
		return nil, fmt.Errorf("memory: since must not be after until")
	}
	if limit <= 0 || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	limit = min(limit, 100)
	candidates := min(limit*4, 400)
	// Chronological ordering and per-kind selection must happen before the
	// final limit, not after truncating by lexical/semantic relevance.
	if quotas != nil || opts.Order == "newest" || opts.Order == "oldest" {
		all, err := s.Current(ctx, repo, kind)
		if err != nil {
			return nil, err
		}
		candidates = len(all)
	}
	var lexical []Note
	if match := ftsQuery(query); match != "" {
		since, until := "", ""
		if !opts.Since.IsZero() {
			since = opts.Since.UTC().Format(timeLayout)
		}
		if !opts.Until.IsZero() {
			until = opts.Until.UTC().Format(timeLayout)
		}
		order := "bm25(notes_fts), l.stated_at DESC, l.id DESC"
		if opts.Order == "newest" {
			order = "l.stated_at DESC, l.id DESC"
		}
		if opts.Order == "oldest" {
			order = "l.stated_at ASC, l.id ASC"
		}
		rows, err := s.db.QueryContext(ctx, latest+` SELECT `+prefixed("l.")+` FROM notes_fts f JOIN latest l ON l.id = f.rowid
            WHERE notes_fts MATCH ? AND (l.kind = ? OR (? = '' AND l.kind != 'summary')) AND l.forgotten = 0
            AND (? = '' OR l.stated_at >= ?) AND (? = '' OR l.stated_at <= ?)
            ORDER BY `+order+` LIMIT ?`, repo, match, kind, kind, since, since, until, until, candidates)
		if err != nil {
			return nil, err
		}
		lexical, err = scan(rows)
		if err != nil {
			return nil, err
		}
	}
	if trace != nil {
		trace.SemanticEnabled = s.semanticConfig() != nil
		for _, n := range lexical {
			trace.LexicalIDs = append(trace.LexicalIDs, n.ID)
		}
	}
	semanticCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	semantic, err := s.SemanticSearchBetween(semanticCtx, repo, kind, query, candidates, opts.Since, opts.Until)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s.semanticNotice(err)
		if trace != nil {
			trace.SemanticFallback = true
		}
	}
	if trace != nil {
		for _, n := range semantic {
			trace.SemanticIDs = append(trace.SemanticIDs, n.ID)
		}
	}
	// Reciprocal-rank fusion avoids comparing raw BM25 and cosine scores.
	type ranked struct {
		note  Note
		score float64
	}
	merged := map[int64]*ranked{}
	for _, list := range [][]Note{lexical, semantic} {
		for i, n := range list {
			if merged[n.ID] == nil {
				merged[n.ID] = &ranked{note: n}
			}
			merged[n.ID].score += 1 / float64(60+i+1)
		}
	}
	var rankedNotes []*ranked
	for _, n := range merged {
		rankedNotes = append(rankedNotes, n)
	}
	sort.Slice(rankedNotes, func(i, j int) bool {
		a, b := rankedNotes[i], rankedNotes[j]
		if (opts.Order == "" || opts.Order == "relevance") && a.score != b.score {
			return a.score > b.score
		}
		if !a.note.StatedAt.Equal(b.note.StatedAt) {
			if opts.Order == "oldest" {
				return a.note.StatedAt.Before(b.note.StatedAt)
			}
			return a.note.StatedAt.After(b.note.StatedAt)
		}
		if opts.Order == "oldest" {
			return a.note.ID < b.note.ID
		}
		return a.note.ID > b.note.ID
	})
	// Embedding work can overlap a replacement or forget in another process.
	// Revalidate lexical candidates too, not only the semantic branch.
	live, err := s.Current(ctx, repo, kind)
	if err != nil {
		return nil, err
	}
	current := make(map[int64]bool, len(live))
	for _, n := range live {
		current[n.ID] = true
	}
	var out []Note
	for _, n := range rankedNotes {
		if !current[n.note.ID] || excluded[n.note.ID] {
			continue
		}
		if quotas != nil {
			if quotas[n.note.Kind] <= 0 {
				continue
			}
			quotas[n.note.Kind]--
		}
		out = append(out, n.note)
		if trace != nil {
			trace.SelectedIDs = append(trace.SelectedIDs, n.note.ID)
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func prefixed(p string) string {
	cols := strings.Split(columns, ", ")
	for i := range cols {
		cols[i] = p + cols[i]
	}
	return strings.Join(cols, ", ")
}

// ftsQuery ORs the message's words as quoted terms, so user text can never
// be read as FTS5 syntax.
func ftsQuery(q string) string {
	var terms []string
	for _, t := range words(q) {
		terms = append(terms, `"`+t+`"`)
	}
	return strings.Join(terms, " OR ")
}

func words(q string) []string {
	stop := map[string]bool{"the": true, "and": true, "for": true, "you": true, "can": true, "this": true, "that": true, "with": true, "from": true, "what": true, "how": true, "are": true, "please": true, "want": true, "into": true, "when": true}
	seen := map[string]bool{}
	var terms []string
	for _, t := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(t) >= 3 && !stop[t] && !seen[t] && len(terms) < 32 {
			seen[t] = true
			terms = append(terms, t)
		}
	}
	return terms
}
