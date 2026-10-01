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
}

func Encode(backend string, state []byte) ([]byte, error) {
	if backend == "" || !json.Valid(state) || bytes.Equal(bytes.TrimSpace(state), []byte("null")) {
		return nil, fmt.Errorf("invalid session provider or state")
	}
	return json.Marshal(Record{Version: 1, Provider: backend, State: state})
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
