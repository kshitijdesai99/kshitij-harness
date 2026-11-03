package provider

import "encoding/json"

// Event is a public conversation event, never a provider wire item. Text is
// caller/assistant/tool text only, not reasoning, auth state or image fields.
// Arbitrary conversation text may contain secrets; this is not a redactor.
// An assistant item can finish in an attempt that later fails; AttemptEnd is
// the authority for whether that attempt entered resumable provider history.
type Event struct {
	Type      string          `json:"type"`
	AttemptID string          `json:"attempt_id,omitempty"`
	MessageID string          `json:"message_id,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Phase     string          `json:"phase,omitempty"`
	Text      string          `json:"text,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Status    string          `json:"status,omitempty"`
}

// EventObserver is optional. Failures must reach the caller without executing
// returned tool calls. It must not be attached to replay or synthetic requests.
type EventObserver interface{ SetEventObserver(func(Event) error) }
