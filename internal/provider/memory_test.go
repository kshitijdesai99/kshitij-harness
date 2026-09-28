package provider

import (
	"strings"
	"testing"

	"kh/internal/config"
)

func TestMemoryEphemeralAndOrdered(t *testing.T) {
	p := NewCodex(config.Defaults, "", nil)
	p.input = []map[string]any{
		{"type": "message", "role": "user", "content": "old conversation"},
		{"type": "message", "role": "user", "content": "current request"},
	}
	p.SetMemory("remember this")
	input := p.inputWithMemory()
	if len(input) != 3 || input[1]["role"] != "user" || !strings.Contains(input[1]["content"].([]map[string]any)[0]["text"].(string), "remember this") || input[2]["content"] != "current request" {
		t.Fatalf("memory not immediately before current request: %+v", input)
	}
	if len(p.input) != 2 {
		t.Fatal("memory mutated conversation")
	}
	saved, err := p.Save()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "remember this") {
		t.Fatal("memory leaked into saved session")
	}
	p.SetMemory("")
	if len(p.inputWithMemory()) != 2 {
		t.Fatal("memory not reset")
	}
}
