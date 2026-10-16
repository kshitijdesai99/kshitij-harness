package test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"kh/internal/memory"
)

func TestRecallKindQuotasBeforeLimit(t *testing.T) {
	s := open(t)
	for i := 0; i < 8; i++ {
		add(t, s, memory.Note{Kind: "instruction", Key: fmt.Sprintf("choice-%d", i), Text: "build build build"})
	}
	gotcha := add(t, s, memory.Note{Kind: "gotcha", Key: "prerequisite", Text: "Start the database before the build."})
	b := memory.Budget{TopInstructions: 1, TopGotchas: 1, TotalChars: 4000}
	text, err := s.Recall(context.Background(), repo, "build", nil, b)
	if err != nil || !strings.Contains(text, gotcha.Text) || strings.Count(text, "[memory #") != 2 {
		t.Fatalf("recall quotas: %s %v", text, err)
	}
	// Previously seen top-ranked instructions must not crowd out new notes.
	seen := memory.Seen(state(t, text))
	next, err := s.Recall(context.Background(), repo, "build", seen, b)
	if err != nil || !strings.Contains(next, "build build build") {
		t.Fatalf("unseen recall: %s %v", next, err)
	}
}

func TestSearchTraceReportsCandidatesWithoutProviderErrors(t *testing.T) {
	s := open(t)
	n := add(t, s, memory.Note{Kind: "fact", Key: "transport", Text: "automobile"})
	configureSemantic(t, s, &hybridTestEmbedder{})
	_, trace, err := s.SearchWithTrace(context.Background(), repo, "", "car", 10, memory.SearchOptions{})
	if err != nil || !trace.SemanticEnabled || trace.SemanticFallback || len(trace.LexicalIDs) != 0 || len(trace.SemanticIDs) != 1 || trace.SemanticIDs[0] != n.ID || len(trace.SelectedIDs) != 1 {
		t.Fatalf("semantic trace: %+v %v", trace, err)
	}
}

func TestRecallFullSummaryPayloadFitsBudget(t *testing.T) {
	s := open(t)
	add(t, s, memory.Note{Kind: "summary", Key: memory.SummaryKey, Text: strings.Repeat("s", 100), Source: "summary"})
	text, err := s.Recall(context.Background(), repo, "query", nil, memory.Budget{SummaryChars: 100, TotalChars: 1000})
	if err != nil || !strings.Contains(text, strings.Repeat("s", 100)) {
		t.Fatalf("full summary: %q %v", text, err)
	}
}

func TestSemanticChronologyBeforeLimit(t *testing.T) {
	s := open(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		add(t, s, memory.Note{Kind: "fact", Key: fmt.Sprintf("old-%d", i), Text: "older automobile", StatedAt: base.Add(time.Duration(i) * time.Hour)})
	}
	newest := add(t, s, memory.Note{Kind: "fact", Key: "newest", Text: "newest record", StatedAt: base.Add(10 * time.Hour)})
	configureSemantic(t, s, &hybridTestEmbedder{fn: func(_ context.Context, texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i, s := range texts {
			out[i] = []float32{1, 0}
			if strings.Contains(s, "newest") {
				out[i] = []float32{0.5, 0.8660254}
			}
		}
		return out, nil
	}})
	notes, err := s.SearchWithOptions(context.Background(), repo, "fact", "car", 1, memory.SearchOptions{Order: "newest"})
	if err != nil || len(notes) != 1 || notes[0].ID != newest.ID {
		t.Fatalf("chronology: %+v %v", notes, err)
	}
}
