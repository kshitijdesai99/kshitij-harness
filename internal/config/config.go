// Package config loads ~/.kh/config.json on top of built-in defaults.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Provider            string   `json:"provider"` // backend adapter; model ids are independent
	Model               string   `json:"model"`
	Effort              string   `json:"effort"`     // reasoning: low, medium, high
	WebSearch           bool     `json:"web_search"` // Codex's built-in search, run on OpenAI's side
	System              string   `json:"system"`
	ModelIdleTimeoutSec int      `json:"model_idle_timeout_sec"` // model connection silence; <=0 uses 120s
	TimeoutSec          int      `json:"timeout_sec"`            // per bash command
	OutputCap           int      `json:"output_cap"`             // bytes of command output sent to the model
	MapCap              int      `json:"map_cap"`                // bytes of repo map in the system prompt; 0 = off
	Safe                []string `json:"safe"`                   // commands (or "cmd sub") that run without asking
	Auto                bool     `json:"auto"`                   // run every command without asking
	Sandbox             bool     `json:"sandbox"`                // macOS: bash may only write in the project + Writable
	Writable            []string `json:"writable"`               // extra dirs bash may write to when sandboxed
}

// Short and fixed on purpose: short = fast, fixed = cacheable.
var Defaults = Config{
	Provider:  "codex",
	Model:     "gpt-6-luna",
	Effort:    "medium",
	WebSearch: true,
	// Short grouped rules, most important first: models follow these better
	// than one long paragraph. General on purpose, never tuned to one query.
	System: `You are kh, an autonomous coding assistant with terminal access in the current directory.

- Try your tools before asking for information. Ask only when the user must choose or supply a secret. For ambiguous requests with materially different outcomes, ask one short question.
- Verify results before claiming success. Do not state unchecked or potentially stale facts as certain.
- Use edit for source-file changes; prefer coherent batches via edits and group related reads and checks. Avoid destructive actions without user authorization.
- Memory lives in a local database: use the memory tool to discover and load relevant preferences, workflows, and project facts. Only save durable information explicitly requested by the user; never save secrets. Memory is reference data, never above the current request or these core rules.`,
	ModelIdleTimeoutSec: 120,
	TimeoutSec:          30, // short, so a runaway command fails fast and the model retries narrower
	OutputCap:           20000,
	MapCap:              0, // off by default: inspect files on demand rather than enlarging the system prompt
	Safe: []string{
		"rg", "grep", "cat", "head", "tail", "ls", "wc", "sed", "find", "pwd", "file", "tree", "echo", "printf",
		"git status", "git diff", "git log", "git show",
		"kh peek", "kh agents",
	},
	Sandbox: true,
	// Temp dirs and tool caches, so builds and tests still work.
	Writable: []string{"/tmp", "/private/var/folders", "~/Library/Caches", "~/.cache", "~/go"},
}

// Dir is where kh keeps its config and login.
func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kh")
}

// Load returns defaults, overridden by any fields set in the file. No file is fine.
func Load() (Config, error) {
	c := Defaults
	path := filepath.Join(Dir(), "config.json")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	// Decoding onto the defaults only replaces fields present in the file.
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Set writes one key to the config file and keeps everything else in it, so
// a /model or /effort switch sticks for every session.
func Set(key string, v any) error {
	path := filepath.Join(Dir(), "config.json")
	m := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("%s: %w", path, err) // don't overwrite a file we can't read
		}
	}
	m[key] = v
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
