// Package tools holds everything the model can call.
package tools

import (
	"context"
	"encoding/json"
	"sync"
)

// Lines is every line typed at the terminal, from one background reader.
// Whoever is waiting gets the next line: a y/n question, the chat prompt, or
// agent.Run between steps (to steer a running task). Closed on Ctrl-D.
var Lines = make(chan string, 16)

var inputOnce sync.Once

// StartInput starts reading only after tmux attach has finished in the launcher;
// otherwise the reader could steal keystrokes from the tmux client.
func StartInput() {
	inputOnce.Do(func() { go readInput() })
}

// Tool is one plugin. Add a tool = one file + one line in the list in main.
type Tool struct {
	Name        string
	Description string
	Params      map[string]any // JSON Schema "properties"
	Required    []string
	Run         func(ctx context.Context, input json.RawMessage) (string, error)
}
