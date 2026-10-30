package session

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Record identifies the adapter without interpreting its private JSON state.
type Record struct {
	Version  int             `json:"version"`
	Provider string          `json:"provider"`
	State    json.RawMessage `json:"state"`
	Identity *Identity       `json:"identity,omitempty"`
}

// Identity records how a chat was created, independently of its current process.
// Missing metadata stays unknown, including when an old chat is resumed.
type Identity struct {
	Role   string `json:"role"`
	Name   string `json:"name,omitempty"`
	Parent string `json:"parent,omitempty"`
}

// NewIdentity classifies only new chats. An address alone does not make a worker.
func NewIdentity(parent, name string) *Identity {
	if parent != "" {
		return &Identity{Role: "subagent", Name: name, Parent: parent}
	}
	return &Identity{Role: "main", Name: name}
}

// Label returns the saved role for a preview; it never infers legacy identity.
func (i *Identity) Label() string {
	if i != nil {
		switch i.Role {
		case "main":
			return "[main]"
		case "subagent":
			if i.Name != "" {
				return "[subagent: " + i.Name + "]"
			}
			return "[subagent]"
		}
	}
	return "[unknown]"
}

func Encode(backend string, state []byte) ([]byte, error) {
	return EncodeWithIdentity(backend, state, nil)
}

// EncodeWithIdentity wraps provider state without changing the loaded identity.
func EncodeWithIdentity(backend string, state []byte, identity *Identity) ([]byte, error) {
	if backend == "" || !json.Valid(state) || bytes.Equal(bytes.TrimSpace(state), []byte("null")) {
		return nil, fmt.Errorf("invalid session provider or state")
	}
	return json.Marshal(Record{Version: 1, Provider: backend, State: state, Identity: identity})
}

func Decode(b []byte) (Record, error) {
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return r, err
	}
	if r.Version == 0 && r.Provider == "" && len(r.State) == 0 {
		// Pre-envelope chats were Codex-only. Keep this migration here rather
		// than teaching the agent or new adapters about legacy wire formats.
		var legacy struct {
			Session string          `json:"session"`
			Input   json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(b, &legacy); err != nil || legacy.Session == "" || len(legacy.Input) == 0 {
			return r, fmt.Errorf("invalid legacy session")
		}
		return Record{Version: 1, Provider: "codex", State: b}, nil
	}
	if r.Version != 1 || r.Provider == "" || !json.Valid(r.State) || bytes.Equal(bytes.TrimSpace(r.State), []byte("null")) {
		return r, fmt.Errorf("unsupported or invalid session record")
	}
	return r, nil
}
