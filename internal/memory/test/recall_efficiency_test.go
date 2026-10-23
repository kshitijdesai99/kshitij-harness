package test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"kh/internal/memory"
)

func TestRecallAllSeenSkipsModelsButExplicitSearchStillWorks(t *testing.T) {
	s := open(t)
	n := add(t, s, memory.Note{Kind: "fact", Key: "context", Text: "car saved preference"})
	calls := 0
	configureSemantic(t, s, &hybridTestEmbedder{fn: func(_ context.Context, texts []string) ([][]float32, error) {
		calls++
		out := make([][]float32, len(texts))
		for i := range out {
			out[i] = []float32{1, 0}
		}
		return out, nil
	}})
	if err := s.ConfigureReranker(&fakeReranker{}, nil); err != nil {
		t.Fatal(err)
	}
	text, trace, err := s.RecallWithTrace(context.Background(), repo, "car", map[int64]bool{n.ID: true}, memory.Budget{TopKnowledge: 1})
	if err != nil || text != "" || calls != 0 || trace.Search.Started || !strings.Contains(trace.Search.SkipReason, "already in chat") {
		t.Fatalf("all seen: %q %+v calls=%d err=%v", text, trace, calls, err)
	}
	// A new note or version must remain discoverable on the very next message.
	fresh := add(t, s, memory.Note{Kind: "fact", Key: n.Key, Text: "car updated preference"})
	_, trace, err = s.RecallWithTrace(context.Background(), repo, "car", map[int64]bool{n.ID: true}, memory.Budget{TopKnowledge: 1})
	if err != nil || len(trace.LoadedIDs) != 1 || trace.LoadedIDs[0] != fresh.ID {
		t.Fatalf("correction: %+v %v", trace, err)
	}
	found, err := s.Search(context.Background(), repo, "", "car", 10)
	if err != nil || len(found) != 1 || found[0].ID != fresh.ID || calls == 0 {
		t.Fatalf("explicit search: %v calls=%d err=%v", found, calls, err)
	}
}

func TestRecallCompactDiagnosticsAndFullDetails(t *testing.T) {
	var trace memory.RecallTrace
	trace.Search.Started, trace.Search.LexicalComplete = true, true
	trace.Search.SemanticEnabled, trace.Search.SemanticComplete = true, true
	trace.Search.RerankEnabled, trace.Search.RerankComplete = true, true
	trace.Search.RelevanceCutoff = .09
	for i := int64(1); i <= 100; i++ {
		trace.Search.LexicalIDs = append(trace.Search.LexicalIDs, i)
		trace.Search.SemanticIDs = append(trace.Search.SemanticIDs, i)
		trace.Search.RerankScores = append(trace.Search.RerankScores, memory.RerankScore{ID: i, Score: .9})
	}
	for i := 0; i < 10; i++ {
		trace.Loaded = append(trace.Loaded, fmt.Sprintf("#%d %s", i, strings.Repeat("key", 30)))
	}
	compact, full := trace.Summary(nil), trace.Diagnostic(nil)
	if len(compact) > 1024 || strings.Contains(compact, "scores:") || strings.Contains(compact, "[1 2 3") || !strings.Contains(compact, "/memory explain") || !strings.Contains(full, "#100=0.9") {
		t.Fatalf("compact=%q full=%q", compact, full)
	}
}
