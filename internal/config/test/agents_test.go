package test

import (
	"os"
	"path/filepath"
	"testing"

	"kh/internal/config"
)

func TestAgentsMaxLiveDefaultsAndOverrides(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := config.Load()
	if err != nil || cfg.Agents.MaxLive != 50 {
		t.Fatalf("default capacity: %+v %v", cfg.Agents, err)
	}
	if err := config.Set("agents", map[string]int{"max_live": 3}); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load()
	if err != nil || cfg.Agents.MaxLive != 3 {
		t.Fatalf("configured capacity: %+v %v", cfg.Agents, err)
	}
	// Missing nested values preserve the default; malformed/zero limits fail.
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"agents":{}}`, true},
		{`{"agents":{"max_live":0}}`, false},
		{`{"agents":{"max_live":-1}}`, false},
		{`{"agents":{"max_live":"3"}}`, false},
	} {
		if err := os.WriteFile(filepath.Join(config.Dir(), "config.json"), []byte(tc.body), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err = config.Load()
		if (err == nil) != tc.valid {
			t.Fatalf("%s: %+v, %v", tc.body, cfg.Agents, err)
		}
	}
}
