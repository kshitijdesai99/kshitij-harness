package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"kh/internal/config"
)

func TestAutoDefaultsOn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := config.Load()
	if err != nil || !cfg.Auto || !cfg.Sandbox {
		t.Fatalf("defaults: Auto=%v Sandbox=%v err=%v", cfg.Auto, cfg.Sandbox, err)
	}
}

func TestAutoMigratesLegacyOnlyWithoutExplicitAuto(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  bool
	}{
		{"empty", `{}`, true},
		{"legacy-on", `{"auto_write":true}`, true},
		{"legacy-off", `{"auto_write":false}`, false},
		{"explicit-off-wins", `{"auto":false,"auto_write":true}`, false},
		{"explicit-on-wins", `{"auto":true,"auto_write":false}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(config.Dir(), "config.json"), []byte(tc.input), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load()
			if err != nil || cfg.Auto != tc.want {
				t.Fatalf("Load: Auto=%v want %v err=%v", cfg.Auto, tc.want, err)
			}
		})
	}
}

func TestSetAutoRemovesLegacyKey(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Setenv("HOME", t.TempDir())
		if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(config.Dir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"auto_write":true,"custom":"preserve"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := config.Set("auto", enabled); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var stored map[string]any
		if err := json.Unmarshal(data, &stored); err != nil {
			t.Fatal(err)
		}
		if _, ok := stored["auto_write"]; ok {
			t.Fatalf("legacy key retained: %s", data)
		}
		if stored["auto"] != enabled || stored["custom"] != "preserve" {
			t.Fatalf("unexpected persisted config: %s", data)
		}
	}
}
