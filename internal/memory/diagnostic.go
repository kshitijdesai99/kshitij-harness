package memory

import (
	"fmt"
	"strings"
)

// RecallTrace contains IDs and note keys: no query, note bodies, or provider errors.
type RecallTrace struct {
	Search    SearchTrace
	LoadedIDs []int64
	Loaded    []string
}

// Diagnostic summarizes actual stage outcomes, not merely configured services.
func (t RecallTrace) Diagnostic(err error) string {
	s := t.Search
	keyword, semantic, rerank := "not run", "off", "off"
	if s.Started {
		keyword = "incomplete"
	}
	if s.LexicalComplete {
		keyword = fmt.Sprintf("ok %v", s.LexicalIDs)
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
			semantic = fmt.Sprintf("ok %v", s.SemanticIDs)
		} else if s.SemanticFallback {
			semantic = "failed (keyword fallback)"
		}
	}
	if s.RerankEnabled {
		rerank = "not run"
		if s.Started {
			rerank = "skipped (no candidates)"
		}
		if s.RerankComplete {
			rerank = fmt.Sprintf("ok; scores: %v; rejected (<%.4g): %v", s.RerankScores, s.RelevanceCutoff, s.RejectedIDs)
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
	if len(t.Loaded) > 0 {
		loaded = strings.Join(t.Loaded, ", ")
	}
	if err != nil {
		status, loaded = "failed", "none (retrieval aborted)"
	}
	return strings.Join([]string{"Memory search: " + status, "keyword: " + keyword, "semantic: " + semantic, "rerank: " + rerank, "loaded: " + loaded}, "; ")
}
