package test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"kh/internal/memory"
)

func TestMemoryHybridCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	env := func(home string) []string {
		return append(os.Environ(), "HOME="+home, "KH_AUTO_REBUILD=0", "KH_AUTO=", "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", "KH_PARENT=", "KH_AGENT=", "TMUX=", "TMUX_PANE=")
	}
	write := func(t *testing.T, home, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(home, ".kh"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".kh", "config.json"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("validation and off preserve config", func(t *testing.T) {
		home := t.TempDir()
		write(t, home, `{"memory_model":"off","memory_hybrid":false,"memory_embedding_url":"http://invalid.example/api/embed","memory_embedding_model":"preserve","memory_reranker_url":"http://invalid.example/rerank","custom":"keep"}`)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary)
		cmd.Env = env(home)
		cmd.Stdin = strings.NewReader("/memory\n/memory status\n/memory hybrid\n/memory hybrid ON\n/memory rerank off\n/memory hybrid on extra\n/memory hybrid off\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI: %s %v", out, err)
		}
		if strings.Count(string(out), "usage: /memory hybrid on|off") != 6 || !strings.Contains(string(out), "memory hybrid off: keyword-only") || strings.Contains(string(out), "unavailable") {
			t.Fatalf("native commands: %s", out)
		}
		data, err := os.ReadFile(filepath.Join(home, ".kh", "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		var cfg map[string]any
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg["memory_hybrid"] != false || cfg["memory_embedding_model"] != "preserve" || cfg["custom"] != "keep" || cfg["memory_embedding_url"] != "http://invalid.example/api/embed" || cfg["memory_reranker_url"] != "http://invalid.example/rerank" {
			t.Fatalf("lost config: %s", data)
		}
	})
	t.Run("uncached startup falls back without downloads", func(t *testing.T) {
		home := t.TempDir()
		write(t, home, `{"memory_model":"off","memory_hybrid":true}`)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary)
		cmd.Env = env(home)
		cmd.Stdin = strings.NewReader("first query\nsecond query\n")
		out, err := cmd.CombinedOutput()
		if err != nil || strings.Count(string(out), "Hybrid runtime unavailable; using keyword search") != 1 {
			t.Fatalf("startup: %s %v", out, err)
		}
		if _, err := os.Stat(filepath.Join(home, ".kh", "hybrid")); !os.IsNotExist(err) {
			t.Fatalf("startup created runtime directory: %v", err)
		}
		data, _ := os.ReadFile(filepath.Join(home, ".kh", "config.json"))
		if !strings.Contains(string(data), `"memory_hybrid":true`) {
			t.Fatalf("startup changed opt-in: %s", data)
		}
	})
	t.Run("mixed endpoints do not require unrelated local caches", func(t *testing.T) {
		for _, endpoints := range []string{
			`"memory_embedding_url":"http://127.0.0.1:11434/api/embed","memory_reranker_url":"http://127.0.0.1:9/rerank"`,
			`"memory_embedding_url":"http://127.0.0.1:9/api/embed","memory_reranker_url":"http://127.0.0.1:11435/rerank"`,
		} {
			home := t.TempDir()
			write(t, home, `{"auto":false,"memory_model":"off","memory_hybrid":true,`+endpoints+`}`)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			cmd := exec.CommandContext(ctx, binary)
			cmd.Env = env(home)
			cmd.Stdin = strings.NewReader("")
			out, err := cmd.CombinedOutput()
			cancel()
			if err != nil || strings.Contains(string(out), "unavailable") {
				t.Fatalf("mixed startup required unrelated managed services: %s %v", out, err)
			}
			if _, err := os.Stat(filepath.Join(home, ".kh", "hybrid")); !os.IsNotExist(err) {
				t.Fatalf("mixed startup touched local setup: %v", err)
			}
		}
	})
	t.Run("reload between user messages", func(t *testing.T) {
		home := t.TempDir()
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			var body struct {
				Input     []string `json:"input"`
				Documents []string `json:"documents"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			if r.URL.Path == "/rerank" {
				results := make([]map[string]any, len(body.Documents))
				for i := range results {
					results[i] = map[string]any{"index": i, "relevance_score": 0.9}
				}
				json.NewEncoder(w).Encode(map[string]any{"results": results})
			} else {
				vectors := make([][]float32, len(body.Input))
				for i := range vectors {
					vectors[i] = []float32{1, 0}
				}
				json.NewEncoder(w).Encode(map[string]any{"embeddings": vectors})
			}
		}))
		defer server.Close()
		setMode := func(enabled bool) {
			body, _ := json.Marshal(map[string]any{"auto": false, "memory_model": "off", "memory_hybrid": enabled, "memory_embedding_url": server.URL + "/api/embed", "memory_embedding_model": "fixture", "memory_reranker_url": server.URL + "/rerank"})
			write(t, home, string(body))
		}
		setMode(false)
		db, err := memory.Open(filepath.Join(home, ".kh", "memory.db"))
		if err != nil {
			t.Fatal(err)
		}
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = db.Add(context.Background(), memory.Note{Repo: memory.RepoScope(cwd), Kind: "fact", Key: "context", Text: "Keep the context concise.", Source: "you"}, memory.Any)
		db.Close()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary)
		cmd.Env = env(home)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		defer stdin.Close()
		log, err := os.CreateTemp(t.TempDir(), "output")
		if err != nil {
			t.Fatal(err)
		}
		defer log.Close()
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if cmd.ProcessState == nil {
				cancel()
				_ = cmd.Wait()
			}
		}()
		waitFor := func(text string, count int) {
			t.Helper()
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				data, _ := os.ReadFile(log.Name())
				if strings.Count(string(data), text) >= count {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			data, _ := os.ReadFile(log.Name())
			t.Fatalf("missing %q x%d: %s", text, count, data)
		}
		stdin.Write([]byte("/auto\n"))
		waitFor("auto off:", 1)
		if calls.Load() != 0 {
			t.Fatal("disabled startup contacted models")
		}
		stdin.Write([]byte("/memory explain\n"))
		waitFor("no retrieval details recorded since startup", 1)
		setMode(true)
		stdin.Write([]byte("context\n"))
		waitFor("error: not logged in", 1)
		waitFor("Memory search: ok; keyword: ok", 1)
		waitFor("semantic: ok", 1)
		waitFor("rerank: ok", 1)
		waitFor(" context", 1) // loaded memory key, not just candidate counts
		if calls.Load() < 2 {
			t.Fatalf("reload did not enable both services: %d calls", calls.Load())
		}
		beforeExplain := calls.Load()
		stdin.Write([]byte("/memory explain\n"))
		waitFor("scores:", 1)
		if calls.Load() != beforeExplain {
			t.Fatal("explain reran retrieval instead of showing the last trace")
		}
		setMode(false)
		before := calls.Load()
		stdin.Write([]byte("context\n"))
		waitFor("error: not logged in", 2)
		waitFor("Memory search: skipped;", 1)
		waitFor("already in chat", 1)
		waitFor("semantic: not run; rerank: not run", 1)
		if calls.Load() != before {
			t.Fatal("disabled reload contacted models")
		}
		stdin.Close()
		if err := cmd.Wait(); err != nil {
			data, _ := os.ReadFile(log.Name())
			t.Fatalf("exit: %s %v", data, err)
		}
	})
}
