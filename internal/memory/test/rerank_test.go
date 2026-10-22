package test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kh/internal/memory"
)

type fakeReranker struct {
	fn func(context.Context, string, []memory.Note) ([]float64, error)
}

func (r *fakeReranker) Rerank(ctx context.Context, query string, notes []memory.Note) ([]float64, error) {
	if r.fn != nil {
		return r.fn(ctx, query, notes)
	}
	scores := make([]float64, len(notes))
	for i := range scores {
		scores[i] = .5
	}
	return scores, nil
}

func configureRerank(t *testing.T, s *memory.Store, r memory.Reranker) {
	t.Helper()
	configureSemantic(t, s, &fakeEmbedder{model: "rerank-test"})
	if err := s.ConfigureReranker(r, nil); err != nil {
		t.Fatal(err)
	}
}

func rerankIDs(notes []memory.Note) []int64 {
	ids := make([]int64, len(notes))
	for i, n := range notes {
		ids[i] = n.ID
	}
	return ids
}

func TestRerankOverridesFusionAndRecencyOnlyBreaksTies(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	old := add(t, s, memory.Note{Kind: "fact", Key: "old", Text: "automobile", StatedAt: at})
	newer := add(t, s, memory.Note{Kind: "fact", Key: "new", Text: "car", StatedAt: at.Add(time.Hour)})
	newest := add(t, s, memory.Note{Kind: "fact", Key: "newest", Text: "car", StatedAt: at.Add(2 * time.Hour)})
	configureSemantic(t, s, &fakeEmbedder{model: "rerank-test"})
	before, err := s.Search(context.Background(), repo, "", "car", 10)
	if err != nil || len(before) != 3 || before[0].ID == old.ID {
		t.Fatalf("fixture must rank lexical/semantic overlap over old semantic-only note: %v %v", rerankIDs(before), err)
	}
	configureRerank(t, s, &fakeReranker{fn: func(_ context.Context, query string, notes []memory.Note) ([]float64, error) {
		if query != "car" {
			t.Errorf("query = %q", query)
		}
		scores := make([]float64, len(notes))
		for i, n := range notes {
			scores[i] = .4
			if n.ID == old.ID {
				scores[i] = .9
			}
		}
		return scores, nil
	}})
	got, trace, err := s.SearchWithTrace(context.Background(), repo, "", "car", 10, memory.SearchOptions{})
	want := []int64{old.ID, newest.ID, newer.ID}
	if err != nil || !reflect.DeepEqual(rerankIDs(got), want) || !reflect.DeepEqual(trace.RerankedIDs, want) || trace.RerankFallback || !trace.RerankEnabled {
		t.Fatalf("reranked = %v trace=%+v err=%v; want %v", rerankIDs(got), trace, err, want)
	}
}

func TestRerankFailureReturnsLexicalOnly(t *testing.T) {
	for _, failure := range []string{"semantic", "rerank", "short", "nan", "infinite", "range", "no-embedder"} {
		t.Run(failure, func(t *testing.T) {
			s := open(t)
			lexical := add(t, s, memory.Note{Kind: "fact", Key: "parking", Text: "car parking"})
			add(t, s, memory.Note{Kind: "fact", Key: "transport", Text: "automobile"})
			var calls int
			r := &fakeReranker{fn: func(_ context.Context, _ string, notes []memory.Note) ([]float64, error) {
				calls++
				if failure == "rerank" {
					return nil, errors.New("private diagnostic")
				}
				if failure == "short" {
					return nil, nil
				}
				scores := make([]float64, len(notes))
				for i := range scores {
					switch failure {
					case "nan":
						scores[i] = math.NaN()
					case "infinite":
						scores[i] = math.Inf(1)
					case "range":
						scores[i] = 1.1
					}
				}
				return scores, nil
			}}
			configureRerank(t, s, r)
			if failure == "semantic" {
				configureSemantic(t, s, &fakeEmbedder{model: "offline", fn: func(context.Context, []string) ([][]float32, error) {
					return nil, errors.New("private diagnostic")
				}})
			}
			if failure == "no-embedder" {
				if err := s.ConfigureSemantic(nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			var notices []string
			if err := s.ConfigureReranker(r, func(message string) { notices = append(notices, message) }); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				got, trace, err := s.SearchWithTrace(context.Background(), repo, "", "car", 10, memory.SearchOptions{})
				if err != nil || !reflect.DeepEqual(rerankIDs(got), []int64{lexical.ID}) || !trace.RerankFallback || len(trace.RerankedIDs) != 0 {
					t.Fatalf("fallback must exclude semantic-only result: %v trace=%+v err=%v", rerankIDs(got), trace, err)
				}
			}
			if (failure == "semantic" || failure == "no-embedder") && calls != 0 {
				t.Fatalf("reranker called after semantic failure: %d", calls)
			}
			if len(notices) != 1 || !strings.Contains(strings.ToLower(notices[0]), "keyword") || strings.Contains(notices[0], "private") {
				t.Fatalf("unsafe or repeated notices: %q", notices)
			}
		})
	}
}

func TestRerankScopeCurrentAndSecretBoundaries(t *testing.T) {
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
	add(t, s, memory.Note{Kind: "fact", Key: "replaced", Text: "car old"})
	current := add(t, s, memory.Note{Kind: "fact", Key: "replaced", Text: "car current"})
	global := add(t, s, memory.Note{Repo: memory.Global, Kind: "fact", Key: "global", Text: "car global"})
	add(t, s, memory.Note{Repo: "another-repo", Kind: "fact", Key: "foreign", Text: "car foreign"})
	add(t, s, memory.Note{Kind: "summary", Key: memory.SummaryKey, Text: "car summary"})
	add(t, s, memory.Note{Kind: "fact", Key: "forgotten", Text: "car forgotten"})
	if _, err := s.Forget(context.Background(), repo, "forgotten", "", "you"); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"text", "key", "context_summary"} {
		n := add(t, s, memory.Note{Kind: "fact", Key: "legacy-" + column, Text: "car legacy"})
		if _, err := db.Exec(`UPDATE notes SET `+column+`=? WHERE id=?`, "car password: privatevalue", n.ID); err != nil {
			t.Fatal(err)
		}
	}
	var batches [][]int64
	configureRerank(t, s, &fakeReranker{fn: func(_ context.Context, _ string, notes []memory.Note) ([]float64, error) {
		batches = append(batches, rerankIDs(notes))
		for _, n := range notes {
			if memory.Secret(n.Key) || memory.Secret(n.Text) || memory.Secret(n.ContextSummary) {
				t.Errorf("secret sent to reranker: id=%d", n.ID)
			}
		}
		return (&fakeReranker{}).Rerank(context.Background(), "", notes)
	}})
	for _, scope := range []string{repo, memory.Global} {
		got, err := s.Search(context.Background(), scope, "fact", "car", 10)
		want := map[int64]bool{global.ID: true}
		if scope == repo {
			want[current.ID] = true
		}
		if err != nil || len(got) != len(want) || len(batches) == 0 || len(batches[len(batches)-1]) != len(want) {
			t.Fatalf("scope %s: results=%v batches=%v err=%v", scope, rerankIDs(got), batches, err)
		}
		for _, id := range append(rerankIDs(got), batches[len(batches)-1]...) {
			if !want[id] {
				t.Fatalf("out-of-scope/stale/secret id %d in scope %s", id, scope)
			}
		}
	}
	before := len(batches)
	if _, err := s.Search(context.Background(), repo, "", "token=privatevalue", 10); err != nil {
		t.Fatal(err)
	}
	if len(batches) != before {
		t.Fatal("secret query sent to reranker")
	}
}

func TestHTTPRerankerRequestAndScoreOrder(t *testing.T) {
	var request struct {
		Query     string   `json:"query"`
		Documents []string `json:"documents"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, `{"results":[{"index":1,"relevance_score":0.8},{"index":0,"relevance_score":0.2}]}`)
	}))
	defer server.Close()
	r, err := memory.NewHTTPReranker(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	scores, err := r.Rerank(context.Background(), "car", []memory.Note{{Key: "first", Text: "first text", ContextSummary: "discussion"}, {Key: "second", Text: "second text"}})
	if err != nil || !reflect.DeepEqual(scores, []float64{.2, .8}) || request.Query != "car" || len(request.Documents) != 2 {
		t.Fatalf("scores=%v request=%+v err=%v", scores, request, err)
	}
	if !strings.Contains(request.Documents[0], "first text") || !strings.Contains(request.Documents[0], "discussion") || !strings.Contains(request.Documents[1], "second text") {
		t.Fatalf("documents lost content/order: %q", request.Documents)
	}
}

func TestHTTPRerankerAllowsQueuedInference(t *testing.T) {
	var inference sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inference.Lock()
		defer inference.Unlock()
		select {
		case <-time.After(2100 * time.Millisecond):
			fmt.Fprint(w, `{"results":[{"index":0,"relevance_score":0.5}]}`)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	r, err := memory.NewHTTPReranker(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := r.Rerank(ctx, "car", []memory.Note{{Text: "safe"}})
			done <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Errorf("queued inference failed: %v", err)
		}
	}
}

func TestSearchRerankDeadlineAllowsQueueAndInference(t *testing.T) {
	s := open(t)
	add(t, s, memory.Note{Kind: "fact", Key: "transport", Text: "car automobile"})
	called := false
	configureRerank(t, s, &fakeReranker{fn: func(ctx context.Context, query string, notes []memory.Note) ([]float64, error) {
		called = true
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining < 7500*time.Millisecond || remaining > 8*time.Second {
			t.Errorf("rerank deadline must allow bounded queue and inference: %v (set=%v)", remaining, ok)
		}
		return (&fakeReranker{}).Rerank(ctx, query, notes)
	}})
	if _, err := s.Search(context.Background(), repo, "", "car", 10); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("search did not invoke reranker")
	}
}

func TestHTTPRerankerRejectsInvalidScores(t *testing.T) {
	for name, body := range map[string]string{
		"missing-result":  `{"results":[{"index":0,"relevance_score":0.2}]}`,
		"missing-index":   `{"results":[{"relevance_score":0.2},{"index":1,"relevance_score":0.8}]}`,
		"missing-score":   `{"results":[{"index":0},{"index":1,"relevance_score":0.2}]}`,
		"null-score":      `{"results":[{"index":0,"relevance_score":null},{"index":1,"relevance_score":0.2}]}`,
		"duplicate-index": `{"results":[{"index":0,"relevance_score":0.2},{"index":0,"relevance_score":0.8}]}`,
		"negative-index":  `{"results":[{"index":-1,"relevance_score":0.2},{"index":1,"relevance_score":0.8}]}`,
		"large-index":     `{"results":[{"index":2,"relevance_score":0.2},{"index":1,"relevance_score":0.8}]}`,
		"nan":             `{"results":[{"index":0,"relevance_score":NaN},{"index":1,"relevance_score":0.8}]}`,
		"infinite":        `{"results":[{"index":0,"relevance_score":1e999},{"index":1,"relevance_score":0.8}]}`,
		"negative-score":  `{"results":[{"index":0,"relevance_score":-0.1},{"index":1,"relevance_score":0.8}]}`,
		"large-score":     `{"results":[{"index":0,"relevance_score":1.1},{"index":1,"relevance_score":0.8}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			r, err := memory.NewHTTPReranker(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Rerank(context.Background(), "car", []memory.Note{{Text: "one"}, {Text: "two"}}); err == nil {
				t.Fatalf("accepted %s", body)
			}
		})
	}
}

func TestHTTPRerankerSecretsAndCancellation(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	r, err := memory.NewHTTPReranker(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		note  memory.Note
	}{
		{"password: privatevalue", memory.Note{Text: "safe"}},
		{"car", memory.Note{Key: "password: privatevalue"}},
		{"car", memory.Note{Text: "password: privatevalue"}},
		{"car", memory.Note{Text: "safe", ContextSummary: "password: privatevalue"}},
	} {
		if _, err := r.Rerank(context.Background(), tc.query, []memory.Note{tc.note}); err == nil {
			t.Fatal("secret accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("secret request reached HTTP server")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := r.Rerank(ctx, "car", []memory.Note{{Text: "safe"}}); done <- err }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled HTTP request succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP reranker ignored cancellation")
	}
}

func TestRerankCancellationAndConcurrentForget(t *testing.T) {
	for _, action := range []string{"cancel", "forget", "replace"} {
		t.Run(action, func(t *testing.T) {
			s := open(t)
			old := add(t, s, memory.Note{Kind: "fact", Key: "transport", Text: "car automobile"})
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			configureRerank(t, s, &fakeReranker{fn: func(ctx context.Context, _ string, notes []memory.Note) ([]float64, error) {
				close(entered)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-release:
					return (&fakeReranker{}).Rerank(ctx, "", notes)
				}
			}})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				notes []memory.Note
				err   error
			}
			done := make(chan result, 1)
			go func() { notes, err := s.Search(ctx, repo, "", "car", 10); done <- result{notes, err} }()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("reranking did not start")
			}
			switch action {
			case "cancel":
				cancel()
			case "forget":
				if _, err := s.Forget(context.Background(), repo, old.Key, "", "you"); err != nil {
					t.Fatal(err)
				}
			case "replace":
				add(t, s, memory.Note{Kind: "fact", Key: old.Key, Text: "Prefer walking"})
			}
			unblock()
			select {
			case got := <-done:
				if action == "cancel" {
					if !errors.Is(got.err, context.Canceled) {
						t.Fatalf("cancel error = %v", got.err)
					}
				} else if got.err != nil || len(got.notes) != 0 {
					t.Fatalf("stale note returned after %s: %v %v", action, rerankIDs(got.notes), got.err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("search did not finish")
			}
		})
	}
}

func TestProbeHybridEmptyStore(t *testing.T) {
	s := open(t)
	if err := s.ProbeHybrid(context.Background()); err == nil {
		t.Fatal("unconfigured probe succeeded")
	}
	var embeddingCalls, rerankCalls int
	configureSemantic(t, s, &fakeEmbedder{model: "probe", fn: func(_ context.Context, texts []string) ([][]float32, error) {
		embeddingCalls++
		if len(texts) != 1 || texts[0] == "" {
			t.Errorf("probe inputs: %q", texts)
		}
		return [][]float32{{1, 0}}, nil
	}})
	if err := s.ProbeHybrid(context.Background()); err == nil {
		t.Fatal("probe without reranker succeeded")
	}
	if err := s.ConfigureReranker(&fakeReranker{fn: func(_ context.Context, query string, notes []memory.Note) ([]float64, error) {
		rerankCalls++
		if query == "" || len(notes) != 1 || notes[0].ID != 0 {
			t.Errorf("probe request: %q %+v", query, notes)
		}
		return []float64{.5}, nil
	}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.ProbeHybrid(context.Background()); err != nil {
		t.Fatal(err)
	}
	if embeddingCalls != 1 || rerankCalls != 1 {
		t.Fatalf("probe calls: embed=%d rerank=%d", embeddingCalls, rerankCalls)
	}
	if notes, err := s.Current(context.Background(), repo, ""); err != nil || len(notes) != 0 {
		t.Fatalf("probe mutated empty store: %+v %v", notes, err)
	}
}

func TestRerankRecallReservesKindsBeforeCandidateCap(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 65; i++ {
		add(t, s, memory.Note{Kind: "instruction", Key: fmt.Sprintf("car-instruction-%03d", i), Text: "car automobile", StatedAt: at.Add(time.Hour)})
	}
	gotcha := add(t, s, memory.Note{Kind: "gotcha", Key: "maintenance", Text: "car automobile needs maintenance", StatedAt: at})
	var batchSize int
	var gotchaScored bool
	configureRerank(t, s, &fakeReranker{fn: func(_ context.Context, _ string, notes []memory.Note) ([]float64, error) {
		batchSize = len(notes)
		scores := make([]float64, len(notes))
		for i, n := range notes {
			scores[i] = .9
			if n.ID == gotcha.ID {
				gotchaScored = true
				scores[i] = .1
			}
		}
		return scores, nil
	}})
	seen := map[int64]bool{}
	out, err := s.Recall(context.Background(), repo, "car", seen, memory.Budget{TopInstructions: 1, TopGotchas: 1, TotalChars: 4000})
	if err != nil || batchSize != 64 || !gotchaScored || !seen[gotcha.ID] || len(seen) != 2 || !strings.Contains(out, "instruction car-instruction-") || !strings.Contains(out, gotcha.Text) {
		t.Fatalf("kind reservation: batch=%d gotchaScored=%v seen=%v output=%q err=%v", batchSize, gotchaScored, seen, out, err)
	}
}

func TestRerankBoundsCandidates(t *testing.T) {
	s := open(t)
	for i := 0; i < 80; i++ {
		add(t, s, memory.Note{Kind: "fact", Key: fmt.Sprintf("transport-%03d", i), Text: "car automobile"})
	}
	var batchSize int
	configureRerank(t, s, &fakeReranker{fn: func(ctx context.Context, query string, notes []memory.Note) ([]float64, error) {
		batchSize = len(notes)
		return (&fakeReranker{}).Rerank(ctx, query, notes)
	}})
	got, err := s.Search(context.Background(), repo, "", "car", 100)
	if err != nil || batchSize != 64 || len(got) != 64 {
		t.Fatalf("candidate bound: sent=%d returned=%d err=%v", batchSize, len(got), err)
	}
}
