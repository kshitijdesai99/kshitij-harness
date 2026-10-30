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
	if err != nil || !cfg.Auto {
		t.Fatalf("defaults: Auto=%v err=%v", cfg.Auto, err)
	}
}

func TestAutoIgnoresRetiredFolderSetting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  bool
	}{
		{"empty", `{}`, true},
		{"legacy-on", `{"auto_write":true}`, true},
		{"legacy-off", `{"auto_write":false,"sandbox":true,"writable":["/tmp"]}`, true},
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
		if err := os.WriteFile(path, []byte(`{"auto_write":true,"sandbox":true,"writable":["/tmp"],"custom":"preserve"}`), 0o600); err != nil {
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
		for _, retired := range []string{"auto_write", "sandbox", "writable"} {
			if _, ok := stored[retired]; ok {
				t.Fatalf("retired key %s retained: %s", retired, data)
			}
		}
		if stored["auto"] != enabled || stored["custom"] != "preserve" {
			t.Fatalf("unexpected persisted config: %s", data)
		}
	}
}
