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
	Started, LexicalComplete, SemanticComplete, RerankComplete bool
	LexicalIDs, SemanticIDs, SelectedIDs                       []int64
	SemanticEnabled, SemanticFallback                          bool
	RerankEnabled, RerankFallback                              bool
	RerankedIDs                                                []int64
	RerankScores                                               []RerankScore
	RejectedIDs                                                []int64
	RelevanceCutoff                                            float64
	SkipReason, RerankSkipReason                               string
}

// RerankScore retains relevance evidence without retaining query or note text.
type RerankScore struct {
	ID    int64
	Score float64
}

func (s RerankScore) String() string {
	return fmt.Sprintf("#%d=%.4g", s.ID, s.Score)
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
	reranker := s.rerankConfig()
	if trace != nil {
		trace.SemanticEnabled = s.semanticConfig() != nil
		trace.RerankEnabled = reranker != nil
	}
	if opts.Order != "" && opts.Order != "relevance" && opts.Order != "newest" && opts.Order != "oldest" {
		return nil, fmt.Errorf("memory: order must be relevance, newest or oldest")
	}
	if !opts.Since.IsZero() && !opts.Until.IsZero() && opts.Since.After(opts.Until) {
		return nil, fmt.Errorf("memory: since must not be after until")
	}
	if limit <= 0 || strings.TrimSpace(query) == "" {
		if trace != nil {
			trace.SkipReason = "zero search budget or empty query"
		}
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
		if quotas != nil {
			requested, unseen := 0, 0
			for _, n := range all {
				if quotas[n.Kind] > 0 {
					requested++
					if !excluded[n.ID] {
						unseen++
					}
				}
			}
			if unseen == 0 {
				if trace != nil {
					trace.SkipReason = "no memories in requested kinds"
					if requested > 0 {
						trace.SkipReason = fmt.Sprintf("all %d requested memories already in chat", requested)
					}
				}
				return nil, nil
			}
		}
	}
	if trace != nil {
		trace.Started = true
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
		trace.LexicalComplete = true
		for _, n := range lexical {
			trace.LexicalIDs = append(trace.LexicalIDs, n.ID)
		}
	}
	minSimilarity := 0.35
	if reranker != nil && (opts.Order == "" || opts.Order == "relevance") {
		// Similarity orders candidates; the bounded reranker decides relevance.
		// A useful task instruction can fall below the standalone cosine floor.
		minSimilarity = -1
	}
	semanticCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	semantic, err := s.semanticSearchBetween(semanticCtx, repo, kind, query, candidates, opts.Since, opts.Until, minSimilarity)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s.semanticNotice(err)
	}
	if trace != nil {
		trace.SemanticFallback = err != nil
		trace.SemanticComplete = trace.SemanticEnabled && err == nil
	}
	hybridFailed := reranker != nil && (err != nil || s.semanticConfig() == nil)
	if hybridFailed {
		semantic = nil
		reranker.failed("embedding")
		if trace != nil {
			trace.RerankFallback = true
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
	var relevanceCutoff float64
	// Never send excluded, secret-bearing, or stale records to the reranker.
	// Revalidate again below after the network call before returning results.
	if reranker != nil && !hybridFailed && (opts.Order == "" || opts.Order == "relevance") {
		live, err := s.Current(ctx, repo, kind)
		if err != nil {
			return nil, err
		}
		eligible := make(map[int64]bool, len(live))
		for _, n := range live {
			eligible[n.ID] = semanticEligible(n, opts.Since, opts.Until)
		}
		// Reduce interactive scoring without truncating reservations that fit
		// the existing hard ceiling (35 slots with the default quotas).
		batchLimit := 64
		if quotas != nil {
			reserved := 0
			for _, quota := range quotas {
				reserved += max(0, quota)
			}
			batchLimit = min(64, max(32, reserved))
		}
		var batch []Note
		chosen := map[int64]bool{}
		counts := map[string]int{}
		// Reserve room for each requested kind before filling the candidate
		// budget, so frequent instructions cannot crowd out facts or gotchas.
		appendCandidates := func(reserve bool) {
			for _, n := range rankedNotes {
				if len(batch) == batchLimit {
					break
				}
				if !eligible[n.note.ID] || excluded[n.note.ID] || chosen[n.note.ID] || (quotas != nil && quotas[n.note.Kind] <= 0) {
					continue
				}
				if reserve && counts[n.note.Kind] >= quotas[n.note.Kind] {
					continue
				}
				batch = append(batch, n.note)
				chosen[n.note.ID] = true
				counts[n.note.Kind]++
			}
		}
		if quotas != nil {
			appendCandidates(true)
		}
		appendCandidates(false)
		if len(batch) > 0 {
			rctx, stop := context.WithTimeout(ctx, rerankTimeout)
			scores, rerankErr := rerankCandidates(rctx, reranker, query, batch)
			stop()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if rerankErr != nil {
				reranker.failed("reranking")
				if trace != nil {
					trace.RerankFallback = true
				}
				rankedNotes = nil
				for _, n := range lexical {
					rankedNotes = append(rankedNotes, &ranked{note: n})
				}
			} else {
				reranker.succeeded()
				relevanceCutoff = minRerankRelevance
				rankedNotes = nil
				for i, n := range batch {
					rankedNotes = append(rankedNotes, &ranked{note: n, score: scores[i]})
				}
				sort.SliceStable(rankedNotes, func(i, j int) bool {
					a, b := rankedNotes[i], rankedNotes[j]
					if a.score != b.score {
						return a.score > b.score
					}
					return semanticNewer(a.note, b.note)
				})
				if trace != nil {
					trace.RerankComplete = true
					trace.RelevanceCutoff = minRerankRelevance
					for _, n := range rankedNotes {
						trace.RerankedIDs = append(trace.RerankedIDs, n.note.ID)
						trace.RerankScores = append(trace.RerankScores, RerankScore{ID: n.note.ID, Score: n.score})
					}
				}
			}
		} else {
			reranker.succeeded()
			if trace != nil {
				seen := 0
				for _, n := range rankedNotes {
					if excluded[n.note.ID] {
						seen++
					}
				}
				trace.RerankSkipReason = "no matching memories"
				if len(rankedNotes) > 0 {
					trace.RerankSkipReason = fmt.Sprintf("no eligible matches; %d already in chat, %d filtered", seen, len(rankedNotes)-seen)
				}
			}
		}
	}
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
	if relevanceCutoff > 0 {
		// Stale, seen, and unrequested kinds must not raise the cutoff for
		// eligible notes. Revalidate before using the top score, too.
		for _, n := range rankedNotes {
			if current[n.note.ID] && !excluded[n.note.ID] && (quotas == nil || quotas[n.note.Kind] > 0) {
				relevanceCutoff = max(relevanceCutoff, n.score*minRerankFraction)
			}
		}
		if trace != nil {
			trace.RelevanceCutoff = relevanceCutoff
		}
	}
	var out []Note
	for _, n := range rankedNotes {
		if !current[n.note.ID] || excluded[n.note.ID] || (quotas != nil && quotas[n.note.Kind] <= 0) {
			continue
		}
		// Reject before quotas/limits; rejected notes remain unseen.
		if relevanceCutoff > 0 && n.score < relevanceCutoff {
			if trace != nil {
				trace.RejectedIDs = append(trace.RejectedIDs, n.note.ID)
			}
			continue
		}
		if quotas != nil {
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
