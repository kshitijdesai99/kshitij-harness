// Package tools defines the model-callable operations.
package tools

import (
	"context"
	"encoding/json"

	"kh/internal/provider"
)

// Tool composes schema metadata with behavior; it has no terminal singleton.
type Tool struct {
	Name        string
	Description string
	Params      map[string]any
	InputSchema map[string]any // optional complete JSON Schema; overrides Params/Required
	Required    []string
	Run         func(context.Context, json.RawMessage) (string, error)
	RunResult   func(context.Context, json.RawMessage) (provider.Result, error) // optional multimodal output instead of Run
}
