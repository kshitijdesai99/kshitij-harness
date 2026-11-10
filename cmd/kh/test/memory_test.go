package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kh/internal/memory"
)

func TestMemoryCLIExplainPrivacy(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	isolateCLIFixture(t)
	for _, mode := range []string{"keyword", "hybrid", "embedding failure", "rerank failure", "invalid endpoint"} {
		t.Run(mode, func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			cwd, err := filepath.EvalSymlinks(cwd)
			if err != nil {
				t.Fatal(err)
			}
			const query = "privatequerymarker"
			const key = "private-key-marker"
			const text = query + " private note body marker"
			const providerError = "private provider error marker"
			const model = "private-model-marker"
			s, err := memory.Open(filepath.Join(home, ".kh", "memory.db"))
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = s.Add(context.Background(), memory.Note{Repo: memory.RepoScope(cwd), Kind: "fact", Key: key, Text: text, Source: "you"}, memory.Any)
			s.Close()
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "embedding failure" || (mode == "rerank failure" && r.URL.Path == "/rerank") {
					http.Error(w, providerError, http.StatusInternalServerError)
					return
				}
				var in struct {
					Input     []string `json:"input"`
					Documents []string `json:"documents"`
				}
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					t.Error(err)
					return
				}
				if r.URL.Path == "/rerank" {
					results := make([]map[string]any, len(in.Documents))
					for i := range results {
						results[i] = map[string]any{"index": i, "relevance_score": 0.9}
					}
					json.NewEncoder(w).Encode(map[string]any{"results": results})
					return
				}
				vectors := make([][]float32, len(in.Input))
				for i := range vectors {
					vectors[i] = []float32{1, 0}
				}
				json.NewEncoder(w).Encode(map[string]any{"embeddings": vectors})
			}))
			defer server.Close()
			endpoint := server.URL + "/api/embed"
			if mode == "invalid endpoint" {
				endpoint = "://private-endpoint-marker"
			}
			cfg, _ := json.Marshal(map[string]any{"memory_model": "off", "memory_hybrid": mode != "keyword", "memory_embedding_url": endpoint, "memory_embedding_model": model, "memory_reranker_url": server.URL + "/rerank"})
			if err := os.WriteFile(filepath.Join(home, ".kh", "config.json"), cfg, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "memory", "explain", query)
			cmd.Dir = cwd
			cmd.Env = append(os.Environ(), "HOME="+home, "KH_AUTO_REBUILD=0", "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", "KH_PARENT=", "KH_AGENT=")
			cmd.Stdin = strings.NewReader("")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("explain: %s %v", out, err)
			}
			trace := string(out)
			requested := mode != "keyword"
			configured := requested && mode != "invalid endpoint"
			if !strings.Contains(trace, fmt.Sprintf("hybrid requested: %t; pipeline configured: %t", requested, configured)) {
				t.Fatalf("configuration state: %s", trace)
			}
			for _, want := range []string{"scope: repo+global", "Memory search: ok", "terms=1, candidates=1", "selected=1"} {
				if !strings.Contains(trace, want) {
					t.Fatalf("missing %q: %s", want, trace)
				}
			}
			if (mode == "embedding failure" || mode == "rerank failure") && !strings.Contains(trace, "fallback=true") {
				t.Fatalf("missing fallback: %s", trace)
			}
			for _, private := range []string{query, key, text, providerError, model, home, cwd, server.URL, endpoint} {
				if strings.Contains(trace, private) {
					t.Fatalf("explain disclosed private fixture data: %s", trace)
				}
			}
		})
	}
}

func TestMemoryCLIProvenanceAndSemanticSearch(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	isolateCLIFixture(t)
	home := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	s, err := memory.Open(filepath.Join(home, ".kh", "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	first := memory.Note{Repo: memory.RepoScope(wd), Kind: "decision", Key: "capture-policy", Text: "Archive every conversation.", ContextSummary: "The initial proposal was discussed.", Source: "you", StatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	_, first, err = s.Add(context.Background(), first, memory.Any)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.Text = "Keep archived discussions out of the prompt."
	second.ContextSummary = "The user clarified selective retention rather than complete archives."
	second.StatedAt = first.StatedAt.Add(time.Hour)
	_, second, err = s.Add(context.Background(), second, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rerank" {
			var in struct {
				Documents []string `json:"documents"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			results := make([]map[string]any, len(in.Documents))
			for i := range results {
				results[i] = map[string]any{"index": i, "relevance_score": 0.9}
			}
			json.NewEncoder(w).Encode(map[string]any{"results": results})
			return
		}
		var in struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		vectors := make([][]float32, len(in.Input))
		for i := range vectors {
			vectors[i] = []float32{1, 0}
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": vectors})
	}))
	defer server.Close()
	cfg, _ := json.Marshal(map[string]any{"memory_model": "off", "memory_embedding_url": server.URL + "/api/embed", "memory_embedding_model": "test-model", "memory_reranker_url": server.URL + "/rerank"})
	if err := os.WriteFile(filepath.Join(home, ".kh", "config.json"), cfg, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, append([]string{"memory"}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home, "KH_AUTO_REBUILD=0", "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", "KH_PARENT=", "KH_AGENT=")
		cmd.Stdin = strings.NewReader("")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("memory %v: %s %v", args, out, err)
		}
		return string(out)
	}
	if out := run("status"); !strings.Contains(out, "decision: 1") || !strings.Contains(out, "hybrid configured") {
		t.Fatalf("status: %s", out)
	}
	if out := run("search", "reduce context footprint"); !strings.Contains(out, second.Text) || strings.Contains(out, first.Text) {
		t.Fatalf("semantic: %s", out)
	}
	if out := run("explain", "reduce context footprint"); !strings.Contains(out, "rerank: configured=true, attempted=true, complete=true, fallback=false") || !strings.Contains(out, "selected=1") || strings.Contains(out, second.Text) || strings.Contains(out, second.Key) {
		t.Fatalf("reranking trace: %s", out)
	}
	if out := run("get", fmt.Sprint(first.ID)); !strings.Contains(out, "state: superseded") || !strings.Contains(out, first.ContextSummary) {
		t.Fatalf("get: %s", out)
	}
	if out := run("history", "repo", "capture-policy"); strings.Index(out, second.Text) > strings.Index(out, first.Text) || !strings.Contains(out, fmt.Sprintf("overrides: #%d", first.ID)) {
		t.Fatalf("history: %s", out)
	}
	server.Close()
	if out := run("search", "archived"); !strings.Contains(out, "using keyword search") || !strings.Contains(out, second.Text) {
		t.Fatalf("fallback: %s", out)
	}
}
