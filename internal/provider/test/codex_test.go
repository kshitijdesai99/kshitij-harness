package test

import (
	"context"
	"encoding/json"
	"testing"

	"kh/internal/config"
	"kh/internal/provider"
)

// A failed step (here: not logged in) must leave history resumable.
func TestSaveLoadAfterFailedStep(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := provider.NewCodex(config.Defaults, nil)
	if _, err := p.Step(context.Background(), "hi", nil); err == nil {
		t.Fatal("want not-logged-in error")
	}
	b, err := p.Save()
	if err != nil {
		t.Fatal(err)
	}

	q := provider.NewCodex(config.Defaults, nil)
	if err := q.Load(b); err != nil {
		t.Fatal(err)
	}
	b2, _ := q.Save()
	if string(b) != string(b2) {
		t.Errorf("round trip changed state:\n%s\n%s", b, b2)
	}
	var s struct{ Input []map[string]any }
	json.Unmarshal(b, &s)
	if len(s.Input) != 1 || s.Input[0]["role"] != "user" {
		t.Errorf("want just the user message, got %v", s.Input)
	}
}
