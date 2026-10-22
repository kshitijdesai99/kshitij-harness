package test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"kh/internal/memory"
)

func TestRecallSkipsStandaloneSocialMessages(t *testing.T) {
	for _, hybrid := range []bool{false, true} {
		t.Run(fmt.Sprint("hybrid=", hybrid), func(t *testing.T) {
			s := open(t)
			add(t, s, memory.Note{Kind: "fact", Key: "greeting", Text: "hello hey thanks blog audio"})
			if hybrid {
				configureSemantic(t, s, &hybridTestEmbedder{fn: func(context.Context, []string) ([][]float32, error) {
					t.Fatal("social message contacted embedder")
					return nil, nil
				}})
				if err := s.ConfigureReranker(&fakeReranker{fn: func(context.Context, string, []memory.Note) ([]float64, error) {
					t.Fatal("social message contacted reranker")
					return nil, nil
				}}, nil); err != nil {
					t.Fatal(err)
				}
			} else {
				// Explicit searches are not conversational intent classification.
				notes, err := s.Search(context.Background(), repo, "", "hello", 10)
				if err != nil || len(notes) != 1 {
					t.Fatalf("explicit greeting search: %v %v", notes, err)
				}
			}
			for _, q := range []string{"hey", "Hey!", "hi", "hello there", "good morning", "thank you!", "thanks", "OK", "got it", "sounds good"} {
				text, trace, err := s.RecallWithTrace(context.Background(), repo, q, nil, memory.Budget{TopKnowledge: 5})
				if err != nil || text != "" || trace.Search.Started || trace.Search.SkipReason == "" || len(trace.LoadedIDs) != 0 || !strings.Contains(trace.Diagnostic(err), "skipped") {
					t.Fatalf("%q: %q %+v %v", q, text, trace, err)
				}
			}
		})
	}
}

func TestRecallGreetingPreservesCorrectionsSummaryAndNamedReference(t *testing.T) {
	for _, forget := range []bool{false, true} {
		t.Run(fmt.Sprint("forget=", forget), func(t *testing.T) {
			s := open(t)
			old := add(t, s, memory.Note{Kind: "instruction", Key: "policy", Text: "Old policy."})
			if forget {
				if _, err := s.Forget(context.Background(), repo, old.Key, "", "you"); err != nil {
					t.Fatal(err)
				}
			} else {
				add(t, s, memory.Note{Kind: "instruction", Key: old.Key, Text: "New policy."})
			}
			correction, _, err := s.Latest(context.Background(), repo, old.Key)
			if err != nil {
				t.Fatal(err)
			}
			must := add(t, s, memory.Note{Kind: "instruction", Key: "must-load", Text: "Always keep required policy."})
			summary := add(t, s, memory.Note{Kind: "summary", Key: memory.SummaryKey, Text: "Overview", Refs: []string{must.Key}})
			r, err := s.PutReference(context.Background(), memory.Reference{Note: memory.Note{Repo: repo, Key: "salute", Text: "Named greeting procedure.", Source: "you"}, Aliases: []string{"hi"}, Procedure: "PRIVATE BODY"}, 0)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[int64]bool{old.ID: true}
			b := memory.Budget{SummaryChars: 2000, TopInstructions: 5, TopKnowledge: 5, TotalChars: len(memory.Header) + 1}
			text, _, err := s.RecallWithTrace(context.Background(), repo, "hi", seen, b)
			if err != nil || text != "" || seen[correction.ID] {
				t.Fatalf("overbudget correction must remain retryable: %q %v %v", text, seen, err)
			}
			b.TotalChars = 4000
			text, trace, err := s.RecallWithTrace(context.Background(), repo, "hi", seen, b)
			for _, id := range []int64{correction.ID, r.Note.ID, summary.ID, must.ID} {
				if err != nil || !strings.Contains(text, fmt.Sprintf("#%d]", id)) || !seen[id] {
					t.Fatalf("missing %d: %q %+v %v", id, text, trace, err)
				}
			}
			if trace.Search.Started || strings.Contains(text, "PRIVATE BODY") {
				t.Fatalf("greeting retrieval: %q %+v", text, trace)
			}
		})
	}
}

func TestRerankRejectsWeakMatchesBeforeQuotasAndSeen(t *testing.T) {
	s := open(t)
	weak := add(t, s, memory.Note{Kind: "instruction", Key: "weak", Text: "car unrelated"})
	boundary := add(t, s, memory.Note{Kind: "instruction", Key: "boundary", Text: "car boundary"})
	operational := add(t, s, memory.Note{Kind: "instruction", Key: "operational", Text: "car operational"})
	zero := add(t, s, memory.Note{Kind: "gotcha", Key: "zero", Text: "car zero"})
	scores := map[int64]float64{weak.ID: .019999, boundary.ID: .02, operational.ID: .021125, zero.ID: 0}
	configureRerank(t, s, &fakeReranker{fn: func(_ context.Context, _ string, notes []memory.Note) ([]float64, error) {
		out := make([]float64, len(notes))
		for i, n := range notes {
			out[i] = scores[n.ID]
		}
		return out, nil
	}})
	seen := map[int64]bool{}
	b := memory.Budget{TopInstructions: 5, TopGotchas: 5, TotalChars: 4000}
	text, trace, err := s.RecallWithTrace(context.Background(), repo, "car", seen, b)
	if err != nil || !reflect.DeepEqual(trace.LoadedIDs, []int64{operational.ID, boundary.ID}) || seen[weak.ID] || seen[zero.ID] || len(trace.Search.RejectedIDs) != 2 || len(trace.Search.RerankScores) != 4 || trace.Search.RelevanceCutoff != .02 {
		t.Fatalf("relevance: %q %+v %v", text, trace, err)
	}
	diagnostic := trace.Diagnostic(err)
	if !strings.Contains(diagnostic, "rejected (<0.02)") || !strings.Contains(diagnostic, "scores:") {
		t.Fatal(diagnostic)
	}
	// A later query may make an initially rejected note relevant: do not mark it seen.
	scores[weak.ID] = .9
	_, next, err := s.RecallWithTrace(context.Background(), repo, "car", seen, b)
	if err != nil || !reflect.DeepEqual(next.LoadedIDs, []int64{weak.ID}) {
		t.Fatalf("retry: %+v %v", next, err)
	}
	// Successful reranking can legitimately return no relevant memories.
	for id := range scores {
		scores[id] = .0001
	}
	text, trace, err = s.RecallWithTrace(context.Background(), repo, "car", nil, b)
	if err != nil || text != "" || len(trace.LoadedIDs) != 0 || !trace.Search.RerankComplete || trace.Search.RerankFallback {
		t.Fatalf("empty success: %q %+v %v", text, trace, err)
	}
}

func TestRerankRelativeTailPreservesMultipleTopics(t *testing.T) {
	s := open(t)
	top := add(t, s, memory.Note{Kind: "fact", Key: "audio", Text: "car audio"})
	second := add(t, s, memory.Note{Kind: "instruction", Key: "summary-policy", Text: "car summary"})
	weak := add(t, s, memory.Note{Kind: "instruction", Key: "weak", Text: "car weak"})
	scores := map[int64]float64{top.ID: .9, second.ID: .2, weak.ID: .08}
	configureRerank(t, s, &fakeReranker{fn: func(_ context.Context, _ string, notes []memory.Note) ([]float64, error) {
		out := make([]float64, len(notes))
		for i, n := range notes {
			out[i] = scores[n.ID]
		}
		return out, nil
	}})
	_, trace, err := s.RecallWithTrace(context.Background(), repo, "car", nil, memory.Budget{TopInstructions: 5, TopKnowledge: 5})
	if err != nil || !reflect.DeepEqual(trace.LoadedIDs, []int64{top.ID, second.ID}) || !reflect.DeepEqual(trace.Search.RejectedIDs, []int64{weak.ID}) || trace.Search.RelevanceCutoff != scores[top.ID]*.1 {
		t.Fatalf("relative tail: %+v %v", trace, err)
	}
}

func TestRerankIneligibleTopScoreCannotSuppressLiveRequestedNotes(t *testing.T) {
	for _, mode := range []string{"disabled-kind", "forgotten-during-rerank"} {
		t.Run(mode, func(t *testing.T) {
			s := open(t)
			kind := "fact"
			if mode == "forgotten-during-rerank" {
				kind = "instruction"
			}
			high := add(t, s, memory.Note{Kind: kind, Key: "high", Text: "car high"})
			policy := add(t, s, memory.Note{Kind: "instruction", Key: "policy", Text: "car policy"})
			configureRerank(t, s, &fakeReranker{fn: func(ctx context.Context, _ string, notes []memory.Note) ([]float64, error) {
				out := make([]float64, len(notes))
				for i, n := range notes {
					out[i] = .04
					if n.ID == high.ID {
						out[i] = .99
					}
				}
				if mode == "forgotten-during-rerank" {
					if _, err := s.Forget(ctx, repo, high.Key, "", "you"); err != nil {
						t.Fatal(err)
					}
				}
				return out, nil
			}})
			_, trace, err := s.RecallWithTrace(context.Background(), repo, "car", nil, memory.Budget{TopInstructions: 1})
			if err != nil || !reflect.DeepEqual(trace.LoadedIDs, []int64{policy.ID}) || trace.Search.RelevanceCutoff != .02 || len(trace.Search.RejectedIDs) != 0 {
				t.Fatalf("eligible top: %+v %v", trace, err)
			}
		})
	}
}

func TestRecallDoesNotSkipShortTopicsOrMixedGreetings(t *testing.T) {
	for _, q := range []string{"audio", "tests", "washer", "slug", "hi audio", "hey, washer drainage?", "thanks, fix the slug"} {
		t.Run(q, func(t *testing.T) {
			s := open(t)
			n := add(t, s, memory.Note{Kind: "fact", Key: "topic", Text: q + " relevant preference"})
			configureSemantic(t, s, &hybridTestEmbedder{fn: func(_ context.Context, texts []string) ([][]float32, error) {
				out := make([][]float32, len(texts))
				for i := range out {
					out[i] = []float32{1, 0}
				}
				return out, nil
			}})
			if err := s.ConfigureReranker(&fakeReranker{fn: func(context.Context, string, []memory.Note) ([]float64, error) { return []float64{.03}, nil }}, nil); err != nil {
				t.Fatal(err)
			}
			_, trace, err := s.RecallWithTrace(context.Background(), repo, q, nil, memory.Budget{TopKnowledge: 1})
			if err != nil || !reflect.DeepEqual(trace.LoadedIDs, []int64{n.ID}) || trace.Search.SkipReason != "" {
				t.Fatalf("short query: %+v %v", trace, err)
			}
		})
	}
}
