package memory

import (
	"fmt"
	"strings"
)

// RecallTrace contains IDs and scores, never query text, note content or provider errors.
type RecallTrace struct {
	Search    SearchTrace
	LoadedIDs []int64
}

// Diagnostic reports search-stage state and counts without stored content,
// endpoint/model identities, or raw errors. Attempted means the pipeline entered
// a stage, not that a model necessarily received a request.
func (t SearchTrace) Diagnostic(err error) string {
	status := "ok"
	if t.SkipReason != "" {
		status = "skipped"
	}
	if err != nil {
		status = "failed"
	}
	return fmt.Sprintf("Memory search: %s; keyword: attempted=%t, complete=%t, terms=%d, candidates=%d; semantic: configured=%t, attempted=%t, complete=%t, fallback=%t, candidates=%d; rerank: configured=%t, attempted=%t, complete=%t, fallback=%t, candidates=%d, scored=%d, rejected=%d; selected=%d",
		status, t.LexicalAttempted, t.LexicalComplete, t.LexicalTerms, len(t.LexicalIDs),
		t.SemanticEnabled, t.SemanticAttempted, t.SemanticComplete, t.SemanticFallback, len(t.SemanticIDs),
		t.RerankEnabled, t.RerankAttempted, t.RerankComplete, t.RerankFallback, t.RerankCandidates, len(t.RerankScores), len(t.RejectedIDs), len(t.SelectedIDs))
}

// Diagnostic retains full candidate IDs and scores for on-demand inspection.
func (t RecallTrace) Diagnostic(err error) string { return t.diagnostic(err, true) }

// Summary bounds the per-message notice; full details remain available separately.
func (t RecallTrace) Summary(err error) string { return t.diagnostic(err, false) }

func (t RecallTrace) diagnostic(err error, detailed bool) string {
	s := t.Search
	keyword, semantic, rerank := "not run", "off", "off"
	if s.Started {
		keyword = "incomplete"
	}
	if s.LexicalComplete {
		keyword = fmt.Sprintf("ok (%d matches)", len(s.LexicalIDs))
		if detailed {
			keyword = fmt.Sprintf("ok %v", s.LexicalIDs)
		}
	}
	if s.SkipReason != "" {
		keyword = "skipped (" + s.SkipReason + ")"
		semantic, rerank = "not run", "not run"
	}
	if s.SemanticEnabled {
		semantic = "not run"
		if s.Started {
			semantic = "incomplete"
		}
		if s.SemanticComplete {
			semantic = fmt.Sprintf("ok (%d candidates)", len(s.SemanticIDs))
			if detailed {
				semantic = fmt.Sprintf("ok %v", s.SemanticIDs)
			}
		} else if s.SemanticFallback {
			semantic = "failed (keyword fallback)"
		}
	}
	if s.RerankEnabled {
		rerank = "not run"
		if s.Started && s.RerankSkipReason != "" {
			rerank = "skipped (" + s.RerankSkipReason + ")"
		}
		if s.RerankComplete {
			rerank = fmt.Sprintf("ok (%d scored, %d rejected)", len(s.RerankScores), len(s.RejectedIDs))
			if detailed {
				rerank = fmt.Sprintf("ok; scores: %v; rejected (<%.4g): %v", s.RerankScores, s.RelevanceCutoff, s.RejectedIDs)
			}
		} else if s.RerankFallback {
			rerank = "failed (keyword fallback)"
			if !s.SemanticEnabled || s.SemanticFallback {
				rerank = "skipped (embedding unavailable; keyword fallback)"
			}
		} else if err != nil && s.Started {
			rerank = "incomplete"
		}
	}
	status := "ok"
	if s.SkipReason != "" {
		status = "skipped"
	}
	loaded := "none (no new relevant memories or budget)"
	if len(t.LoadedIDs) > 0 {
		n := len(t.LoadedIDs)
		if !detailed {
			n = min(n, 5)
		}
		ids := make([]string, n)
		for i, id := range t.LoadedIDs[:n] {
			ids[i] = fmt.Sprintf("#%d", id)
		}
		loaded = strings.Join(ids, ", ")
		if n < len(t.LoadedIDs) {
			loaded += fmt.Sprintf(" (+%d more; /memory explain)", len(t.LoadedIDs)-n)
		}
	}
	if err != nil {
		status, loaded = "failed", "none (retrieval aborted)"
	}
	return strings.Join([]string{"Memory search: " + status, "keyword: " + keyword, "semantic: " + semantic, "rerank: " + rerank, "loaded: " + loaded}, "; ")
}
