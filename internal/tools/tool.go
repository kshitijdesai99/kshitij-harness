// Package tools defines the model-callable operations.
package tools

import (
	"context"
	"encoding/json"
)

// Tool composes schema metadata with behavior; it has no terminal singleton.
type Tool struct {
	Name        string
	Description string
	Params      map[string]any
	Required    []string
	Run         func(context.Context, json.RawMessage) (string, error)
}
