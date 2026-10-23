package test

import (
	"context"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"kh/internal/hybrid"
	"kh/internal/memory"
)

func TestRerankerCanJudgeCandidatesBelowStandaloneCosineFloor(t *testing.T) {
	s := open(t)
	policy := add(t, s, memory.Note{Kind: "instruction", Key: "test-policy", Text: "Keep Go tests in package/test subfolders."})
	unrelated := add(t, s, memory.Note{Kind: "fact", Key: "blog", Text: "Blog posts have an AI summary."})
	configureSemantic(t, s, &hybridTestEmbedder{fn: func(_ context.Context, texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i, text := range texts {
			switch {
			case text == "regression coverage":
				out[i] = []float32{1, 0}
			case strings.Contains(text, policy.Text):
				out[i] = []float32{.34, .9404254}
			default:
				out[i] = []float32{.8, .6}
			}
		}
		return out, nil
	}})
	// Without a reranker, preserve the existing standalone cosine floor.
	notes, err := s.SemanticSearch(context.Background(), repo, "", "regression coverage", 10)
	if err != nil || !reflect.DeepEqual(rerankIDs(notes), []int64{unrelated.ID}) {
		t.Fatalf("standalone semantics: %v %v", notes, err)
	}
	if err := s.ConfigureReranker(&fakeReranker{fn: func(_ context.Context, _ string, notes []memory.Note) ([]float64, error) {
		scores := make([]float64, len(notes))
		for i, n := range notes {
			scores[i] = .0001
			if n.ID == policy.ID {
				scores[i] = .021125
			}
		}
		return scores, nil
	}}, nil); err != nil {
		t.Fatal(err)
	}
	notes, trace, err := s.SearchWithTrace(context.Background(), repo, "", "regression coverage", 10, memory.SearchOptions{})
	if err != nil || !reflect.DeepEqual(rerankIDs(notes), []int64{policy.ID}) || len(trace.LexicalIDs) != 0 || len(trace.SemanticIDs) != 2 || !reflect.DeepEqual(trace.RejectedIDs, []int64{unrelated.ID}) {
		t.Fatalf("hybrid judgment: %v %+v %v", notes, trace, err)
	}
	// Chronological searches do not run reranking, so must retain that floor.
	notes, trace, err = s.SearchWithTrace(context.Background(), repo, "", "regression coverage", 10, memory.SearchOptions{Order: "newest"})
	if err != nil || !reflect.DeepEqual(rerankIDs(notes), []int64{unrelated.ID}) || trace.RerankComplete {
		t.Fatalf("chronological semantics: %v %+v %v", notes, trace, err)
	}
}

// Opt in to exercise already-running local models; never starts services or
// downloads models. The disposable corpus never reads/writes the user's notes.
func TestLocalModelMemoryRelevance(t *testing.T) {
	if os.Getenv("KH_TEST_LOCAL_RETRIEVAL") != "1" {
		t.Skip("set KH_TEST_LOCAL_RETRIEVAL=1 with local embedding/reranking services running")
	}
	s := open(t)
	keys := map[int64]string{}
	for _, n := range []memory.Note{
		{Kind: "instruction", Key: "tests", Text: "All Go tests must live in package/test subfolders, not production package roots."},
		{Kind: "instruction", Key: "audio", Text: "Use user-provided article narration audio; do not generate AI narration."},
		{Kind: "instruction", Key: "resume", Text: "Resume the previous saved chat session with kh -r."},
		{Kind: "instruction", Key: "slug", Text: "Generate blog slugs from titles; resolve collisions with -1, -2, -3 suffixes."},
		{Kind: "decision", Key: "blog", Text: "Add a clearly labeled AI-generated summary at the bottom of blog posts."},
		{Kind: "fact", Key: "washer", Text: "For a washer full of water, check the drain filter and drain hose for blockage."},
	} {
		n = add(t, s, n)
		keys[n.ID] = n.Key
	}
	e, err := memory.NewOllamaEmbedder("http://127.0.0.1:11434/api/embed", "qwen3-embedding:0.6b")
	if err != nil {
		t.Fatal(err)
	}
	r, err := memory.NewRevisionedHTTPReranker(hybrid.DefaultEndpoint, hybrid.HelperRevision)
	if err != nil {
		t.Fatal(err)
	}
	configureSemantic(t, s, e)
	if err := s.ConfigureReranker(r, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ query, want string }{
		{"hey", ""}, {"hello!", ""}, {"thanks", ""},
		{"whats up", ""}, {"what's up?", ""}, {"what’s up", ""},
		{"how's it going?", ""}, {"hello, how are you?", ""}, {"okay, thanks", ""},
		{"Tell me a joke", ""}, {"Why is the sky blue?", ""}, {"How do I bake sourdough bread?", ""},
		{"audio", "audio"}, {"tests", "tests"}, {"resume", "resume"}, {"slug", "slug"},
		{"hi audio", "audio"}, {"hey washer drainage", "washer"},
		{"Restore my previous conversation", "resume"},
		{"Add regression coverage for the new search behavior", "tests"},
		{"Write a unit test for the new parser", "tests"},
		{"Write Go regression tests for this change", "tests"}, {"Move the tests to the right folder", "tests"},
		{"Open my saved conversation", "resume"}, {"Narrate the article", "audio"},
		{"Resolve a title collision in the blog slug", "slug"}, {"Add blog summaries", "blog"},
		{"What is the weather like today?", ""}, {"How do I cook pasta?", ""},
		{"What is 17 times 23?", ""}, {"Explain photosynthesis", ""},
		{"add audio and summary to blog", "audio blog"}, {"resume and write tests", "resume tests"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			text, trace, err := s.RecallWithTrace(ctx, repo, tc.query, nil, memory.Budget{TopInstructions: 5, TopGotchas: 5, TopKnowledge: 5, TotalChars: 4000})
			t.Log(trace.Diagnostic(err))
			if err != nil || trace.Search.SemanticFallback || trace.Search.RerankFallback {
				t.Fatalf("services failed: %v", err)
			}
			var got []string
			for _, id := range trace.LoadedIDs {
				got = append(got, keys[id])
			}
			want := strings.Fields(tc.want)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("want %v, got %v: %s", want, got, text)
			}
		})
	}
}
