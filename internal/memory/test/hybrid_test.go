package test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"kh/internal/memory"
)

type hybridTestEmbedder struct {
	fn func(context.Context, []string) ([][]float32, error)
}

func (*hybridTestEmbedder) Model() string { return "hybrid-test-model" }
func (e *hybridTestEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if e.fn != nil {
		return e.fn(ctx, texts)
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = []float32{0, 1}
		if strings.Contains(text, "automobile") || text == "car" {
			out[i] = []float32{1, 0}
		}
	}
	return out, nil
}

func TestHybridParaphraseAndLexicalUnion(t *testing.T) {
	s := open(t)
	car := add(t, s, memory.Note{Kind: "instruction", Key: "transport", Text: "Choose an automobile."})
	lexical := add(t, s, memory.Note{Kind: "fact", Key: "car-policy", Text: "car parking costs money."})
	add(t, s, memory.Note{Kind: "fact", Key: "unrelated", Text: "Bananas ripen quickly."})
	configureSemantic(t, s, &hybridTestEmbedder{})
	notes, err := s.Search(context.Background(), repo, "", "car", 10)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int64]bool{}
	for _, n := range notes {
		ids[n.ID] = true
	}
	if len(notes) != 2 || !ids[car.ID] || !ids[lexical.ID] {
		t.Fatalf("hybrid union/paraphrase: %+v", notes)
	}
	// Single-note stores must still rank, not return every note as a shortcut.
	single := open(t)
	add(t, single, memory.Note{Kind: "fact", Key: "food", Text: "Bananas ripen quickly."})
	configureSemantic(t, single, &hybridTestEmbedder{})
	if got, err := single.Search(context.Background(), repo, "", "car", 10); err != nil || len(got) != 0 {
		t.Fatalf("unrelated small store: %+v %v", got, err)
	}
}

func TestHybridLexicalFallbackNotice(t *testing.T) {
	s := open(t)
	n := add(t, s, memory.Note{Kind: "instruction", Key: "database", Text: "Use SQLite."})
	var notices []string
	e := &hybridTestEmbedder{fn: func(context.Context, []string) ([][]float32, error) {
		return nil, errors.New("private provider diagnostic")
	}}
	if err := s.ConfigureSemantic(e, func(s string) { notices = append(notices, s) }); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		got, err := s.Search(context.Background(), repo, "", "SQLite", 10)
		if err != nil || len(got) != 1 || got[0].ID != n.ID {
			t.Fatalf("fallback: %+v %v", got, err)
		}
	}
	if len(notices) != 1 || !strings.Contains(strings.ToLower(notices[0]), "keyword") || strings.Contains(notices[0], "private") {
		t.Fatalf("fallback notices: %q", notices)
	}
}

func TestHybridChronologicalFiltersAndOrder(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	before := add(t, s, memory.Note{Kind: "fact", Key: "before", Text: "automobile", StatedAt: at.Add(-time.Hour)})
	first := add(t, s, memory.Note{Kind: "fact", Key: "first", Text: "automobile", StatedAt: at})
	second := add(t, s, memory.Note{Kind: "fact", Key: "second", Text: "automobile", StatedAt: at})
	after := add(t, s, memory.Note{Kind: "fact", Key: "after", Text: "automobile", StatedAt: at.Add(time.Hour)})
	configureSemantic(t, s, &hybridTestEmbedder{})
	for _, query := range []string{"automobile", "car"} {
		for _, tc := range []struct {
			opts memory.SearchOptions
			ids  []int64
		}{
			{memory.SearchOptions{Order: "oldest"}, []int64{before.ID, first.ID, second.ID, after.ID}},
			{memory.SearchOptions{Order: "newest"}, []int64{after.ID, second.ID, first.ID, before.ID}},
			{memory.SearchOptions{Since: at, Until: at, Order: "oldest"}, []int64{first.ID, second.ID}},
			{memory.SearchOptions{Since: at, Until: at, Order: "newest"}, []int64{second.ID, first.ID}},
		} {
			got, err := s.SearchWithOptions(context.Background(), repo, "", query, 10, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			var ids []int64
			for _, n := range got {
				ids = append(ids, n.ID)
			}
			if !reflect.DeepEqual(ids, tc.ids) {
				t.Fatalf("%q %+v: %v want %v", query, tc.opts, ids, tc.ids)
			}
		}
	}
	for _, opts := range []memory.SearchOptions{{Order: "invalid"}, {Since: at.Add(time.Hour), Until: at}} {
		if _, err := s.SearchWithOptions(context.Background(), repo, "", "automobile", 10, opts); err == nil {
			t.Fatalf("accepted invalid options %+v", opts)
		}
	}
}

func TestHybridRecallCapAndSkippedSeen(t *testing.T) {
	s := open(t)
	large := add(t, s, memory.Note{Kind: "instruction", Key: "large", Text: "budget " + strings.Repeat("large ", 100)})
	small := add(t, s, memory.Note{Kind: "instruction", Key: "small", Text: "budget small"})
	seen := map[int64]bool{}
	b := memory.Budget{TopInstructions: 2, TotalChars: len(memory.Header) + 220}
	out, err := s.Recall(context.Background(), repo, "budget", seen, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > b.TotalChars || !seen[small.ID] || seen[large.ID] || !strings.Contains(out, small.Text) {
		t.Fatalf("cap/seen: bytes=%d seen=%v output=%q", len(out), seen, out)
	}
	b.TotalChars = 2000
	out, err = s.Recall(context.Background(), repo, "budget", seen, b)
	if err != nil || !seen[large.ID] || !strings.Contains(out, large.Text) || strings.Contains(out, small.Text) {
		t.Fatalf("skipped note not retried: %q %v seen=%v", out, err, seen)
	}
}

func TestHybridRecallCorrectionsBeforeSummary(t *testing.T) {
	for _, forget := range []bool{false, true} {
		t.Run(fmt.Sprint("forget=", forget), func(t *testing.T) {
			s := open(t)
			old := add(t, s, memory.Note{Kind: "instruction", Key: "editor", Text: "Use old editor."})
			var correction memory.Note
			if forget {
				if _, err := s.Forget(context.Background(), repo, old.Key, "", "you"); err != nil {
					t.Fatal(err)
				}
				var ok bool
				var err error
				correction, ok, err = s.Latest(context.Background(), repo, old.Key)
				if err != nil || !ok {
					t.Fatalf("forgotten version: %v %v", ok, err)
				}
			} else {
				correction = add(t, s, memory.Note{Kind: "instruction", Key: old.Key, Text: "Use new editor."})
			}
			summary := add(t, s, memory.Note{Kind: "summary", Key: memory.SummaryKey, Text: strings.Repeat("overview ", 25)})
			seen := map[int64]bool{old.ID: true}
			b := memory.Budget{SummaryChars: 1000, TotalChars: len(memory.Header) + 260}
			out, err := s.Recall(context.Background(), repo, "", seen, b)
			marker := fmt.Sprintf("[memory #%d]", correction.ID)
			if err != nil || len(out) > b.TotalChars || !strings.Contains(out, marker) || !seen[correction.ID] || seen[summary.ID] {
				t.Fatalf("correction priority: %q %v seen=%v", out, err, seen)
			}
			if forget && !strings.Contains(out, "no longer applies") {
				t.Fatalf("missing forget correction: %q", out)
			}
			b.TotalChars = 2000
			out, err = s.Recall(context.Background(), repo, "", seen, b)
			if err != nil || !seen[summary.ID] || !strings.Contains(out, summary.Text) {
				t.Fatalf("skipped summary not retried: %q %v", out, err)
			}
		})
	}
}

func TestHybridRecallFactsAndDecisions(t *testing.T) {
	s := open(t)
	fact := add(t, s, memory.Note{Kind: "fact", Key: "build", Text: "Compiler build uses Go."})
	decision := add(t, s, memory.Note{Kind: "decision", Key: "build-system", Text: "Compiler build uses modules."})
	seen := map[int64]bool{}
	out, err := s.Recall(context.Background(), repo, "Compiler", seen, memory.Budget{TopKnowledge: 1, TotalChars: 2000})
	if err != nil || !seen[fact.ID] || !seen[decision.ID] || !strings.Contains(out, fact.Text) || !strings.Contains(out, decision.Text) {
		t.Fatalf("knowledge recall: %q %v seen=%v", out, err, seen)
	}
}

func TestHybridRevalidatesLexicalAfterNetworkMutation(t *testing.T) {
	for _, forget := range []bool{false, true} {
		t.Run(fmt.Sprint("forget=", forget), func(t *testing.T) {
			s := open(t)
			old := add(t, s, memory.Note{Kind: "instruction", Key: "transport", Text: "automobile"})
			entered, release := make(chan struct{}), make(chan struct{})
			e := &hybridTestEmbedder{fn: func(ctx context.Context, _ []string) ([][]float32, error) {
				close(entered)
				select {
				case <-release:
					return nil, errors.New("offline")
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}}
			configureSemantic(t, s, e)
			type result struct {
				notes []memory.Note
				err   error
			}
			done := make(chan result, 1)
			go func() {
				notes, err := s.Search(context.Background(), repo, "", "automobile", 10)
				done <- result{notes, err}
			}()
			// Always release embedding, including if an assertion below fails.
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("embedding did not start")
			}
			if forget {
				if _, err := s.Forget(context.Background(), repo, old.Key, "", "you"); err != nil {
					t.Fatal(err)
				}
			} else {
				add(t, s, memory.Note{Kind: "instruction", Key: old.Key, Text: "Prefer walking."})
			}
			unblock()
			select {
			case got := <-done:
				if got.err != nil || len(got.notes) != 0 {
					t.Fatalf("stale lexical note escaped revalidation: %+v %v", got.notes, got.err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("search did not finish")
			}
		})
	}
}
