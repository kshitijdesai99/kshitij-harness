// Package tools holds everything the model can call.
package tools

import (
	"context"
	"encoding/json"
)

// Tool is one plugin. Add a tool = one file + one line in the list in main.
type Tool struct {
	Name        string
	Description string
	Params      map[string]any // JSON Schema "properties"
	Required    []string
	Run         func(ctx context.Context, input json.RawMessage) (string, error)
}
