package test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kh/internal/config"
	"kh/internal/provider"
	"kh/internal/provider/codex"
	"kh/internal/terminal"
)

// Memory is saved in history just before the message it was retrieved for,
// once, so later turns can tell it was already sent.
func TestMemorySavedOnceBeforeUserMessage(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // not logged in: the step fails but keeps the user message
	p := codex.New(config.Defaults, "", nil, nil)
	note := provider.MemoryHeader + "\n[memory #7] instruction css: Use plain CSS."
	p.SetMemory(note)
	p.Step(context.Background(), "style the button", nil)
	p.Step(context.Background(), "and the header", nil)
	saved, err := p.Save()
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Input []struct {
			Role    string
			Content []struct{ Text string }
		}
	}
	json.Unmarshal(saved, &s)
	var texts []string
	for _, it := range s.Input {
		texts = append(texts, it.Content[0].Text)
	}
	if len(texts) != 3 || texts[0] != note || texts[1] != "style the button" || texts[2] != "and the header" {
		t.Fatalf("history: %q", texts)
	}
	resumed := codex.New(config.Defaults, "", nil, nil)
	if err := resumed.Load(saved); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	resumed.Replay(terminal.Renderer{Out: &out, Err: &out})
	if got := out.String(); strings.Contains(got, "Use plain CSS") || !strings.Contains(got, "[memory: 1 notes]") {
		t.Fatalf("replay should not show memory as typed text: %q", got)
	}
}
