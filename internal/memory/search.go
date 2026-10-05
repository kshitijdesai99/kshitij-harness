package memory

import (
	"context"
	"strings"
	"unicode"
)

// Search returns up to limit live notes of one kind that match the query
// by keyword (FTS5, ranked by bm25). When the kind has no more notes than
// the limit, all of them come back: there is nothing to choose between.
// Old versions are excluded before ranking, so they never take a place.
func (s *Store) Search(ctx context.Context, repo, kind, query string, limit int) ([]Note, error) {
	all, err := s.Current(ctx, repo, kind)
	if err != nil || len(all) <= limit {
		return all, err
	}
	match := ftsQuery(query)
	if match == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, latest+` SELECT `+prefixed("l.")+` FROM notes_fts f JOIN latest l ON l.id = f.rowid
        WHERE notes_fts MATCH ? AND l.kind = ? AND l.forgotten = 0 ORDER BY bm25(notes_fts) LIMIT ?`, repo, match, kind, limit)
	if err != nil {
		return nil, err
	}
	return scan(rows)
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
