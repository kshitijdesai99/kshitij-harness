// Package provider talks to models. Each provider keeps its own history
// so the loop never has to know the wire format.
package provider

import (
	"context"
	"encoding/json"
	"io"
	"time"
)

// Call is a tool call the model asked for.
type Call struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// Result is what we send back for one Call.
type Result struct {
	ID      string
	Output  string
	IsError bool
}

// Provider is one plugin per model backend.
type Provider interface {
	// Step sends user text (first turn) or tool results (later turns),
	// streams the reply to stdout, and returns the next tool calls.
	// No calls means the model is done.
	Step(ctx context.Context, user string, results []Result) ([]Call, error)
	// Save and Load the history, in the provider's own format, for resuming.
	Save() ([]byte, error)
	Load([]byte) error
	// Replay prints the history as it looked live: "> " for the user,
	// "$ cmd" / "edit path" / "search: q" for actions, then replies.
	Replay(w io.Writer)
	// Stats returns what this turn cost, then resets for the next turn.
	Stats() Stats
	// Use switches model and/or effort ("" keeps the current one), mid-session,
	// and returns both.
	Use(model, effort string) (string, string)
}

// Stats for one turn (one user message and all the steps it took).
type Stats struct {
	In, Cached, Out int
	Think           int           // part of Out spent thinking, not shown
	TTFT            time.Duration // user message to the model's first output; 0 if none
}
