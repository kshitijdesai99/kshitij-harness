// Package provider defines backend-neutral calls, results, and capabilities.
// Wire formats and credentials belong to the individual adapters.
package provider

import (
	"context"
	"encoding/json"
	"time"
)

type Call struct {
	ID    string
	Name  string
	Input json.RawMessage
}

type Result struct {
	ID      string
	Output  string
	IsError bool
}

// Output separates presentation from provider protocol handling. Reply accepts
// streaming fragments; the other methods accept complete lines.
type Output interface {
	Reply(string)
	Query(string)
	Action(string)
	Notice(string)
}

// Provider is the session-level contract. Save/Load exchange opaque JSON
// state; Stats consumes the current turn's metrics. The agent uses only Step.
// Step must promptly return when ctx is cancelled. On failure or cancellation,
// preserve supplied user/results but discard partial output and unpaired calls
// from that step, so steering and resume never reuse incomplete responses.
type Provider interface {
	Step(context.Context, string, []Result) ([]Call, error)
	Save() ([]byte, error)
	Load([]byte) error
	Replay(Output)
	Stats() Stats
	Use(model, effort string) (string, string)
}

// LastResponder exposes semantic message boundaries for session previews.
// Replay/Output callbacks are presentation fragments, not message boundaries.
// An empty most-recent assistant message returns empty, not an older reply.
type LastResponder interface{ LastResponse() string }

// Optional capabilities let adapters reject unsupported features explicitly.
type ImageAttacher interface{ AttachImage(string) }
type MemorySetter interface{ SetMemory(string) }

// Compacter summarizes history without executing tools. Failure leaves history intact.
type Compacter interface{ Compact(context.Context) error }

type Stats struct {
	In, Cached, Out int
	Think           int
	Context         int
	Window          int
	TTFT            time.Duration
}
