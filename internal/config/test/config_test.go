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

func TestCriticalMemoryBudgetRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	defaults, err := config.Load()
	if err != nil || defaults.CriticalMemoryMaxTokens != 0 || defaults.CriticalMemoryChars() != 0 {
		t.Fatalf("automatic critical share: %+v: %v", defaults, err)
	}
	for _, value := range []int{0, -1, -17, 125, 4000, int(^uint(0) >> 1)} {
		if err := config.Set("critical_memory_max_tokens", value); err != nil {
			t.Fatal(err)
		}
		got, err := config.Load()
		wantChars := value * 4
		if value < 0 {
			wantChars = -1
		} else if value > int(^uint(0)>>1)/4 {
			wantChars = int(^uint(0) >> 1)
		}
		if err != nil || got.CriticalMemoryMaxTokens != value || got.CriticalMemoryChars() != wantChars || got.MemoryMaxTokens != 500 {
			t.Fatalf("critical budget %d: %+v: %v", value, got, err)
		}
	}
}

func TestCustomSystemRemainsAnExactOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := config.Set("system", "CUSTOM-SYSTEM"); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load()
	if err != nil || got.System != "CUSTOM-SYSTEM" {
		t.Fatalf("custom system changed: %q: %v", got.System, err)
	}
	if strings.Contains(config.Defaults.System, "Memory policy:") || strings.Contains(config.Defaults.System, "Memory lives in a local database") {
		t.Fatal("memory policy belongs in shared runtime guidance, not replaceable defaults")
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
