package test

import (
	"os"
	"path/filepath"
	"testing"

	"kh/internal/config"
)

func TestHybridMemoryOptIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".kh"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		enabled    bool
	}{
		{"default", `{}`, false},
		{"url only", `{"memory_embedding_url":"http://localhost/api/embed"}`, false},
		{"model only", `{"memory_embedding_model":"legacy"}`, false},
		{"legacy", `{"memory_embedding_url":"http://localhost/api/embed","memory_embedding_model":"legacy"}`, true},
		{"explicit on", `{"memory_hybrid":true}`, true},
		{"explicit off", `{"memory_hybrid":false,"memory_embedding_url":"http://localhost/api/embed","memory_embedding_model":"legacy"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(home, ".kh", "config.json"), []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.HybridMemory() != tc.enabled {
				t.Fatalf("hybrid = %t; want %t", cfg.HybridMemory(), tc.enabled)
			}
			if cfg.MemoryRerankerURL != "" {
				t.Fatalf("reranker default must remain empty: %q", cfg.MemoryRerankerURL)
			}
		})
	}
}

func TestHybridFlagPersistencePreservesEndpoints(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for key, value := range map[string]string{
		"memory_embedding_url":   "http://localhost:11434/api/embed",
		"memory_embedding_model": "legacy",
		"memory_reranker_url":    "http://localhost:11435/rerank",
	} {
		if err := config.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, enabled := range []bool{false, true} {
		if err := config.Set("memory_hybrid", enabled); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MemoryHybrid == nil || cfg.HybridMemory() != enabled || cfg.MemoryEmbeddingModel != "legacy" || cfg.MemoryEmbeddingURL != "http://localhost:11434/api/embed" || cfg.MemoryRerankerURL != "http://localhost:11435/rerank" {
			t.Fatalf("unexpected persisted configuration: %+v", cfg)
		}
	}
}
