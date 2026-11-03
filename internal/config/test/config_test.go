package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kh/internal/config"
)

func TestCompactMemoryBudgetDefaultsAndExplicitOverrides(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	defaults, err := config.Load()
	if err != nil || defaults.MemoryMaxTokens != 500 || defaults.SummaryMaxTokens != 2500 || defaults.MapCap != 0 {
		t.Fatalf("compact defaults: %+v: %v", defaults, err)
	}
	for _, budget := range []int{0, 500, 4000, 12345} {
		if err := config.Set("memory_max_tokens", budget); err != nil {
			t.Fatal(err)
		}
		got, err := config.Load()
		if err != nil || got.MemoryMaxTokens != budget {
			t.Fatalf("explicit budget %d: %+v: %v", budget, got, err)
		}
	}
}

// Set changes one key and keeps the rest of the user's file.
func TestSetKeepsOtherKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".kh"), 0o700)
	os.WriteFile(filepath.Join(home, ".kh", "config.json"), []byte(`{"timeout_sec": 99}`), 0o600)

	if err := config.Set("model", "gpt-5.5"); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Model != "gpt-5.5" || c.TimeoutSec != 99 || c.Effort != config.Defaults.Effort {
		t.Errorf("got model %q timeout %d effort %q", c.Model, c.TimeoutSec, c.Effort)
	}
}

func TestSetRefusesBrokenFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".kh"), 0o700)
	os.WriteFile(filepath.Join(home, ".kh", "config.json"), []byte(`{broken`), 0o600)
	if err := config.Set("model", "x"); err == nil {
		t.Error("should not overwrite a file it can't read")
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".kh", "config.json")); !strings.Contains(string(b), "broken") {
		t.Error("broken file was overwritten")
	}
}
