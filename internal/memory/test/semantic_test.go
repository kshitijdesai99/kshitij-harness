package test

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kh/internal/memory"
)

type fakeEmbedder struct {
	model string
	mu    sync.Mutex
	calls [][]string
	fn    func(context.Context, []string) ([][]float32, error)
}

func (f *fakeEmbedder) Model() string { return f.model }
func (f *fakeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), texts...))
	f.mu.Unlock()
	if f.fn != nil {
		return f.fn(ctx, texts)
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		if strings.Contains(text, "automobile") || strings.Contains(text, "car") {
			out[i] = []float32{1, 0}
		} else {
			out[i] = []float32{0, 1}
		}
	}
	return out, nil
}

func (f *fakeEmbedder) inputs() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.calls...)
}

func configureSemantic(t *testing.T, s *memory.Store, f memory.Embedder) {
	t.Helper()
	if err := s.ConfigureSemantic(f, nil); err != nil {
		t.Fatal(err)
	}
}

func semanticSearch(t *testing.T, s *memory.Store, query string) []memory.Note {
	t.Helper()
	notes, err := s.SemanticSearch(context.Background(), repo, "", query, 10)
	if err != nil {
		t.Fatal(err)
	}
	return notes
}

func TestSemanticParaphraseAndCacheIdentity(t *testing.T) {
	s := open(t)
	car := add(t, s, memory.Note{Kind: "instruction", Key: "transport", Text: "Choose an automobile."})
	add(t, s, memory.Note{Kind: "gotcha", Key: "database", Text: "SQLite has one writer."})
	f := &fakeEmbedder{model: "endpoint-a/model"}
	configureSemantic(t, s, f)
	for i := 0; i < 2; i++ {
		notes := semanticSearch(t, s, "car")
		if len(notes) != 1 || notes[0].ID != car.ID {
			t.Fatalf("paraphrase result: %+v", notes)
		}
	}
	calls := f.inputs()
	if len(calls) != 3 || len(calls[1]) != 2 || len(calls[2]) != 1 || calls[2][0] != "car" {
		t.Fatalf("document vectors not reused: %v", calls)
	}
	// Reconfiguration with an identical identity also reuses persistent vectors.
	same := &fakeEmbedder{model: f.model}
	configureSemantic(t, s, same)
	semanticSearch(t, s, "car")
	if len(same.inputs()) != 1 {
		t.Fatalf("same model cache missed: %v", same.inputs())
	}
	changed := &fakeEmbedder{model: "endpoint-b/model"}
	configureSemantic(t, s, changed)
	semanticSearch(t, s, "car")
	if len(changed.inputs()) != 2 {
		t.Fatalf("different endpoint reused vectors: %v", changed.inputs())
	}
}

func TestSemanticScopeLiveVersionsAndSummary(t *testing.T) {
	s, ctx := open(t), context.Background()
	add(t, s, memory.Note{Kind: "instruction", Key: "changed", Text: "Old automobile preference."})
	add(t, s, memory.Note{Kind: "instruction", Key: "changed", Text: "Prefer walking."})
	add(t, s, memory.Note{Kind: "gotcha", Key: "forgotten", Text: "Discard automobile note."})
	if _, err := s.Forget(ctx, repo, "forgotten", "", "you"); err != nil {
		t.Fatal(err)
	}
	add(t, s, memory.Note{Repo: "other-repo", Kind: "instruction", Key: "other", Text: "Other automobile note."})
	add(t, s, memory.Note{Kind: "summary", Key: memory.SummaryKey, Text: "Automobile summary."})
	global := add(t, s, memory.Note{Repo: memory.Global, Kind: "instruction", Key: "global", Text: "Choose an automobile globally."})
	local := add(t, s, memory.Note{Kind: "gotcha", Key: "local", Text: "An automobile needs fuel."})
	f := &fakeEmbedder{model: "m"}
	configureSemantic(t, s, f)
	notes := semanticSearch(t, s, "car")
	if len(notes) != 2 || notes[0].ID != local.ID || notes[1].ID != global.ID {
		t.Fatalf("scope/live versions: %+v", notes)
	}
	for _, input := range f.inputs()[1] {
		for _, forbidden := range []string{"Old automobile", "Discard automobile", "Other automobile", "Automobile summary"} {
			if strings.Contains(input, forbidden) {
				t.Fatalf("embedded ineligible document %q", input)
			}
		}
	}
	got, err := s.SemanticSearch(ctx, repo, "instruction", "car", 10)
	if err != nil || len(got) != 1 || got[0].ID != global.ID {
		t.Fatalf("kind filter: %+v %v", got, err)
	}
}

func TestSemanticConcurrentSearchesReuseDocumentVectors(t *testing.T) {
	s := open(t)
	add(t, s, memory.Note{Kind: "instruction", Key: "transport", Text: "automobile"})
	f := &fakeEmbedder{model: "m"}
	configureSemantic(t, s, f)
	const workers = 8
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			notes, err := s.SemanticSearch(context.Background(), repo, "", "car", 1)
			if err == nil && len(notes) != 1 {
				err = errors.New("missing semantic match")
			}
			errs <- err
		}()
	}
	for i := 0; i < workers; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := len(f.inputs()); got != workers+1 {
		t.Fatalf("concurrent searches duplicated document work: %v", f.inputs())
	}
}

func TestSemanticInvalidVectors(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    []float32
	}{
		{"empty", nil}, {"zero", []float32{0, 0}},
		{"nan", []float32{float32(math.NaN()), 1}},
		{"infinity", []float32{1, float32(math.Inf(1))}},
		{"dimensions", []float32{1, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := open(t)
			add(t, s, memory.Note{Kind: "instruction", Key: "transport", Text: "automobile"})
			f := &fakeEmbedder{model: "m", fn: func(_ context.Context, texts []string) ([][]float32, error) {
				if texts[0] == "car" {
					return [][]float32{{1, 0}}, nil
				}
				return [][]float32{tc.v}, nil
			}}
			configureSemantic(t, s, f)
			if _, err := s.SemanticSearch(context.Background(), repo, "", "car", 10); err == nil {
				t.Fatal("accepted invalid document embedding")
			}
		})
	}
	t.Run("query", func(t *testing.T) {
		s := open(t)
		add(t, s, memory.Note{Kind: "instruction", Key: "transport", Text: "automobile"})
		f := &fakeEmbedder{model: "m", fn: func(context.Context, []string) ([][]float32, error) {
			return [][]float32{{0, 0}}, nil
		}}
		configureSemantic(t, s, f)
		if _, err := s.SemanticSearch(context.Background(), repo, "", "car", 1); err == nil {
			t.Fatal("accepted zero query vector")
		}
	})
	t.Run("count", func(t *testing.T) {
		s := open(t)
		add(t, s, memory.Note{Kind: "instruction", Key: "transport", Text: "automobile"})
		f := &fakeEmbedder{model: "m", fn: func(context.Context, []string) ([][]float32, error) { return nil, nil }}
		configureSemantic(t, s, f)
		if _, err := s.SemanticSearch(context.Background(), repo, "", "car", 1); err == nil {
			t.Fatal("accepted missing query vector")
		}
	})
}

func TestSemanticNoWorkAndBoundedBackfill(t *testing.T) {
	s := open(t)
	for i := 0; i < 70; i++ {
		add(t, s, memory.Note{Kind: "instruction", Key: time.Unix(int64(i), 0).String(), Text: "automobile"})
	}
	f := &fakeEmbedder{model: "m"}
	configureSemantic(t, s, f)
	for _, tc := range []struct {
		query string
		limit int
	}{{"  ", 1}, {"car", 0}, {"car", -1}} {
		if got, err := s.SemanticSearch(context.Background(), repo, "", tc.query, tc.limit); err != nil || len(got) != 0 {
			t.Fatalf("empty request: %+v %v", got, err)
		}
	}
	if len(f.inputs()) != 0 {
		t.Fatal("empty request embedded text")
	}
	semanticSearch(t, s, "car")
	calls := f.inputs()
	if len(calls) != 5 {
		t.Fatalf("backfill calls: %d", len(calls))
	}
	for _, call := range calls[1:] {
		if len(call) != 16 {
			t.Fatalf("batch size: %d", len(call))
		}
	}
	semanticSearch(t, s, "car")
	calls = f.inputs()
	if len(calls) != 7 || len(calls[6]) != 6 {
		t.Fatalf("bounded continuation: %v", calls)
	}
}

func TestSemanticTimeBoundsAndTieBreak(t *testing.T) {
	s, ctx := open(t), context.Background()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	add(t, s, memory.Note{Kind: "instruction", Key: "old", Text: "automobile", StatedAt: at.Add(-time.Hour)})
	first := add(t, s, memory.Note{Kind: "instruction", Key: "first", Text: "automobile", StatedAt: at})
	second := add(t, s, memory.Note{Kind: "instruction", Key: "second", Text: "automobile", StatedAt: at})
	add(t, s, memory.Note{Kind: "instruction", Key: "future", Text: "automobile", StatedAt: at.Add(time.Hour)})
	f := &fakeEmbedder{model: "m"}
	configureSemantic(t, s, f)
	notes, err := s.SemanticSearchBetween(ctx, repo, "", "car", 10, at, at)
	if err != nil || len(notes) != 2 || notes[0].ID != second.ID || notes[1].ID != first.ID {
		t.Fatalf("time filter/tie break: %+v %v", notes, err)
	}
	if len(f.inputs()) != 2 || len(f.inputs()[1]) != 2 {
		t.Fatalf("embedded notes outside bounds: %v", f.inputs())
	}
}

func TestSemanticPurgeCorruptionAndSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	n := add(t, s, memory.Note{Kind: "instruction", Key: "transport", Text: "automobile"})
	f := &fakeEmbedder{model: "m"}
	configureSemantic(t, s, f)
	semanticSearch(t, s, "car")
	if _, err := db.Exec(`UPDATE notes_vectors SET vector='[1,0,0]' WHERE note_id=?`, n.ID); err != nil {
		t.Fatal(err)
	}
	if notes, err := s.SemanticSearch(ctx, repo, "", "car", 1); err != nil || len(notes) != 1 {
		t.Fatalf("inconsistent cached dimensions were not rebuilt: %+v %v", notes, err)
	}
	var rebuilt string
	if err := db.QueryRow(`SELECT vector FROM notes_vectors WHERE note_id=?`, n.ID).Scan(&rebuilt); err != nil || rebuilt != "[1,0]" {
		t.Fatalf("cache not repaired: %q %v", rebuilt, err)
	}
	if _, err := s.Purge(ctx, repo, n.Key); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM notes_vectors`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("purge left vectors: %d %v", count, err)
	}
	// Legacy/imported data can predate today's guards. Never send any secret
	// field to the provider even when it bypassed Add's validation.
	for _, column := range []string{"text", "key", "context_summary"} {
		entry := add(t, s, memory.Note{Kind: "instruction", Key: "legacy-" + column, Text: "automobile"})
		if _, err := db.Exec(`UPDATE notes SET `+column+`=? WHERE id=?`, "password: privatevalue", entry.ID); err != nil {
			t.Fatal(err)
		}
	}
	before := len(f.inputs())
	if got := semanticSearch(t, s, "car"); len(got) != 0 || len(f.inputs()) != before {
		t.Fatalf("secret notes were embedded: %+v %v", got, f.inputs()[before:])
	}
	if _, err := s.SemanticSearch(ctx, repo, "", "token=privatevalue", 1); err == nil || len(f.inputs()) != before {
		t.Fatal("secret query was embedded")
	}
}

func TestSemanticCancellationAndReplacementDuringEmbedding(t *testing.T) {
	s := open(t)
	add(t, s, memory.Note{Kind: "instruction", Key: "transport", Text: "automobile"})
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f := &fakeEmbedder{model: "m", fn: func(ctx context.Context, texts []string) ([][]float32, error) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		out := make([][]float32, len(texts))
		for i := range out {
			out[i] = []float32{1, 0}
		}
		return out, nil
	}}
	configureSemantic(t, s, f)
	result := make(chan error, 1)
	go func() {
		got, err := s.SemanticSearch(context.Background(), repo, "", "car", 10)
		if err == nil && len(got) != 0 {
			err = errors.New("returned forgotten note")
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("embedding did not start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.SemanticSearch(ctx, repo, "", "car", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting retrieval ignored cancellation: %v", err)
	}
	if _, err := s.Forget(context.Background(), repo, "transport", "", "you"); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retrieval did not finish")
	}
}
