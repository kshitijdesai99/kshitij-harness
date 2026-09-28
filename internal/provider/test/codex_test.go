package test

import (
	"context"
	"encoding/json"
	"strings"
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

func TestReplay(t *testing.T) {
	state := `{"session":"s","input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"count files"}]},
		{"type":"reasoning","encrypted_content":"x"},
		{"type":"function_call","call_id":"1","name":"bash","arguments":"{\"command\":\"ls | wc -l\"}"},
		{"type":"function_call_output","call_id":"1","output":"3"},
		{"type":"function_call","call_id":"2","name":"edit","arguments":"{\"path\":\"a.go\",\"old\":\"\",\"new\":\"x\"}"},
		{"type":"web_search_call","action":{"type":"search","query":"go 1.27"}},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"There are 3."}]}
	]}`
	p := provider.NewCodex(config.Defaults, nil)
	if err := p.Load([]byte(state)); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	p.Replay(&b)
	want := "> count files\n$ ls | wc -l\nedit a.go\nsearch: go 1.27\nThere are 3.\n"
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}
