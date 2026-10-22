package test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"kh/internal/memory"
)

func TestRecallDiagnostics(t *testing.T) {
	for _, mode := range []string{"keyword", "semantic", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			s := open(t)
			n := add(t, s, memory.Note{Kind: "fact", Key: "transport", Text: "car automobile"})
			if mode != "keyword" {
				e := &hybridTestEmbedder{}
				if mode == "fallback" {
					e.fn = func(context.Context, []string) ([][]float32, error) {
						return nil, errors.New("private provider details")
					}
				}
				configureSemantic(t, s, e)
			}
			b := memory.Budget{TopKnowledge: 1, TotalChars: 2000}
			text, trace, err := s.RecallWithTrace(context.Background(), repo, "car", nil, b)
			if err != nil || len(trace.LoadedIDs) != 1 || trace.LoadedIDs[0] != n.ID || !strings.Contains(text, n.Text) {
				t.Fatalf("recall: %q %+v %v", text, trace, err)
			}
			want := "semantic: off"
			if mode == "semantic" {
				want = fmt.Sprintf("semantic: ok [%d]", n.ID)
			} else if mode == "fallback" {
				want = "semantic: failed (keyword fallback)"
			}
			diagnostic := trace.Diagnostic(err)
			if !strings.Contains(diagnostic, want) || !strings.Contains(diagnostic, fmt.Sprintf("keyword: ok [%d]", n.ID)) || strings.Contains(diagnostic, "private") {
				t.Fatal(diagnostic)
			}
			// Search can find candidates while injecting nothing already seen.
			again, next, err := s.RecallWithTrace(context.Background(), repo, "car", memory.Seen(state(t, text)), b)
			if err != nil || again != "" || len(next.LoadedIDs) != 0 || len(next.Search.LexicalIDs) != 1 {
				t.Fatalf("seen: %q %+v %v", again, next, err)
			}
			// Selected candidates that do not fit must not be reported as loaded.
			_, tiny, err := s.RecallWithTrace(context.Background(), repo, "car", nil, memory.Budget{TopKnowledge: 1, TotalChars: 1})
			if err != nil || len(tiny.LoadedIDs) != 0 {
				t.Fatalf("budget: %+v %v", tiny, err)
			}
		})
	}
}

func TestRecallDiagnosticsSanitizeStoredKeys(t *testing.T) {
	s := open(t)
	add(t, s, memory.Note{Kind: "fact", Key: "unsafe\x1b[31m\x07", Text: "car"})
	_, trace, err := s.RecallWithTrace(context.Background(), repo, "car", nil, memory.Budget{TopKnowledge: 1})
	if err != nil || len(trace.LoadedIDs) != 1 || strings.ContainsAny(trace.Diagnostic(err), "\x1b\x07") {
		t.Fatalf("unsafe diagnostic: %q %v", trace.Diagnostic(err), err)
	}
}

func TestRecallDiagnosticsConfiguredButNotRun(t *testing.T) {
	s := open(t)
	configureRerank(t, s, &fakeReranker{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, trace, err := s.RecallWithTrace(ctx, repo, "car", nil, memory.Budget{TopKnowledge: 1})
	if err == nil || !strings.Contains(trace.Diagnostic(err), "semantic: not run; rerank: not run") {
		t.Fatalf("cancelled before search: %+v %v", trace, err)
	}
	_, trace, err = s.RecallWithTrace(context.Background(), repo, "car", nil, memory.Budget{})
	if err != nil || !strings.Contains(trace.Diagnostic(err), "semantic: not run; rerank: not run") || trace.Search.SkipReason == "" {
		t.Fatalf("zero budget: %+v %v", trace, err)
	}
}

func TestRecallDiagnosticsSkippedAndFailed(t *testing.T) {
	s := open(t)
	_, trace, err := s.RecallWithTrace(context.Background(), repo, "", nil, memory.Budget{})
	if err != nil || !strings.Contains(trace.Diagnostic(err), "keyword: skipped (no topic)") {
		t.Fatalf("skipped: %+v %v", trace, err)
	}
	s.Close()
	_, trace, err = s.RecallWithTrace(context.Background(), repo, "car", nil, memory.Budget{TopKnowledge: 1})
	if err == nil || !strings.Contains(trace.Diagnostic(err), "Memory search: failed") || !strings.Contains(trace.Diagnostic(err), "retrieval aborted") {
		t.Fatalf("failed: %+v %v", trace, err)
	}
}
