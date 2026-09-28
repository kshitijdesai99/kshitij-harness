// Package tools holds everything the model can call.
package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
)

// In is the one reader for the terminal. The chat prompt and the y/n question
// must share it, or one would swallow lines buffered by the other.
var In = bufio.NewReader(os.Stdin)

// Tool is one plugin. Add a tool = one file + one line in the list in main.
type Tool struct {
	Name        string
	Description string
	Params      map[string]any // JSON Schema "properties"
	Required    []string
	Run         func(ctx context.Context, input json.RawMessage) (string, error)
}
