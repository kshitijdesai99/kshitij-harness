package test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"kh/internal/memory"
)

func TestSearchDiagnosticStageCounts(t *testing.T) {
	for _, mode := range []string{"keyword-miss", "keyword-hit", "semantic-only", "embedding-failure", "rerank-failure", "rerank-rejection"} {
		t.Run(mode, func(t *testing.T) {
			s := open(t)
			text := "automobile private-note-body"
			if mode == "keyword-hit" {
				text = "car private-note-body"
			}
			add(t, s, memory.Note{Kind: "instruction", Key: "private-note-key", Text: text, ContextSummary: "private-discussion"})
			hybrid := mode != "keyword-miss" && mode != "keyword-hit"
			if hybrid {
				e := &fakeEmbedder{model: "private-endpoint/private-model"}
				if mode == "embedding-failure" {
					e.fn = func(context.Context, []string) ([][]float32, error) {
						return nil, errors.New("private-provider-error")
					}
				}
				configureSemantic(t, s, e)
				if err := s.ConfigureReranker(&fakeReranker{fn: func(_ context.Context, _ string, notes []memory.Note) ([]float64, error) {
					if mode == "rerank-failure" {
						return nil, errors.New("private-provider-error")
					}
					scores := make([]float64, len(notes))
					for i := range scores {
						scores[i] = .9
						if mode == "rerank-rejection" {
							scores[i] = .0001
						}
					}
					return scores, nil
				}}, nil); err != nil {
					t.Fatal(err)
				}
			}
			notes, trace, err := s.SearchWithTrace(context.Background(), repo, "", "car secretquery", 10, memory.SearchOptions{})
			if err != nil {
				t.Fatal(err)
			}
			wantSelected := 0
			if mode == "keyword-hit" || mode == "semantic-only" {
				wantSelected = 1
			}
			wantRerank := hybrid && mode != "embedding-failure"
			if !trace.LexicalAttempted || !trace.LexicalComplete || trace.LexicalTerms != 2 || trace.SemanticEnabled != hybrid || trace.SemanticAttempted != hybrid || trace.RerankAttempted != wantRerank || len(notes) != wantSelected || len(trace.SelectedIDs) != wantSelected {
				t.Fatalf("stage trace: %+v; selected=%d", trace, len(notes))
			}
			if trace.SemanticFallback != (mode == "embedding-failure") || trace.RerankFallback != (mode == "embedding-failure" || mode == "rerank-failure") {
				t.Fatalf("fallback trace: %+v", trace)
			}
			if wantRerank && trace.RerankCandidates != 1 {
				t.Fatalf("rerank input count: %+v", trace)
			}
			if mode == "rerank-rejection" && (len(trace.RerankScores) != 1 || len(trace.RejectedIDs) != 1) {
				t.Fatalf("rejection trace: %+v", trace)
			}
			diagnostic := trace.Diagnostic(err)
			for _, private := range []string{"private", "automobile", repo, "car", "#1"} {
				if strings.Contains(diagnostic, private) {
					t.Fatalf("diagnostic leaked %q: %q", private, diagnostic)
				}
			}
			for _, field := range []string{"keyword: attempted=true", "terms=2", "semantic: configured=", "rerank: configured=", "candidates=", "scored=", "rejected=", "selected="} {
				if !strings.Contains(diagnostic, field) {
					t.Fatalf("missing %q: %q", field, diagnostic)
				}
			}
		})
	}
}

func TestSearchDiagnosticConfiguredButNotAttempted(t *testing.T) {
	s := open(t)
	configureRerank(t, s, &fakeReranker{fn: func(context.Context, string, []memory.Note) ([]float64, error) {
		t.Fatal("skipped search called reranker")
		return nil, nil
	}})
	for _, tc := range []struct {
		query string
		limit int
	}{{"", 10}, {"car", 0}} {
		_, trace, err := s.SearchWithTrace(context.Background(), repo, "", tc.query, tc.limit, memory.SearchOptions{})
		if err != nil || !trace.SemanticEnabled || !trace.RerankEnabled || trace.LexicalAttempted || trace.SemanticAttempted || trace.RerankAttempted || trace.Started || trace.SkipReason == "" {
			t.Fatalf("skip trace: %+v %v", trace, err)
		}
		if diagnostic := trace.Diagnostic(err); !strings.Contains(diagnostic, "Memory search: skipped") || !strings.Contains(diagnostic, "configured=true, attempted=false") {
			t.Fatal(diagnostic)
		}
	}
	_, trace, err := s.SearchWithTrace(context.Background(), repo, "", "car", 10, memory.SearchOptions{Order: "private-invalid-order"})
	if err == nil || !strings.Contains(trace.Diagnostic(err), "Memory search: failed") || strings.Contains(trace.Diagnostic(err), err.Error()) {
		t.Fatalf("failed diagnostic: %q %v", trace.Diagnostic(err), err)
	}
}

func TestKeywordSearchWordingAndScope(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	local := add(t, s, memory.Note{Kind: "instruction", Key: "workflow", Text: "Wait for confirmation prior to edits."})
	global := add(t, s, memory.Note{Repo: memory.Global, Kind: "fact", Key: "shared", Text: "A confirmation can be recorded."})
	add(t, s, memory.Note{Repo: "github.com/elsewhere/project", Kind: "instruction", Key: "foreign", Text: "Use an approval workflow."})
	for _, query := range []string{"approval", "confirm", "xy"} {
		notes, trace, err := s.SearchWithTrace(ctx, repo, "", query, 10, memory.SearchOptions{})
		if err != nil || len(notes) != 0 || len(trace.LexicalIDs) != 0 {
			t.Fatalf("unexpected match for %q: %+v %+v %v", query, notes, trace, err)
		}
		if query == "xy" && (trace.LexicalTerms != 0 || trace.LexicalAttempted) {
			t.Fatalf("short term attempted FTS: %+v", trace)
		}
	}
	notes, trace, err := s.SearchWithTrace(ctx, repo, "", "confirmation", 10, memory.SearchOptions{})
	if err != nil || len(notes) != 2 || len(trace.LexicalIDs) != 2 {
		t.Fatalf("same-token scoped search: %+v %+v %v", notes, trace, err)
	}
	ids := map[int64]bool{}
	for _, n := range notes {
		ids[n.ID] = true
	}
	if !ids[local.ID] || !ids[global.ID] {
		t.Fatalf("scoped IDs: %v", ids)
	}
	notes, err = s.Search(ctx, memory.Global, "", "confirmation", 10)
	if err != nil || !reflect.DeepEqual(rerankIDs(notes), []int64{global.ID}) {
		t.Fatalf("global-only search: %+v %v", notes, err)
	}
	// A lexical miss is not evidence that the known note was lost.
	got, ok, err := s.Get(ctx, local.ID)
	if err != nil || !ok || got.ID != local.ID {
		t.Fatalf("get after lexical miss: %+v %t %v", got, ok, err)
	}
}
