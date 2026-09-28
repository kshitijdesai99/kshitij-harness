// Package config loads ~/.kh/config.json on top of built-in defaults.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Model      string   `json:"model"`
	Effort     string   `json:"effort"`     // reasoning: low, medium, high
	WebSearch  bool     `json:"web_search"` // Codex's built-in search, run on OpenAI's side
	System     string   `json:"system"`
	TimeoutSec int      `json:"timeout_sec"` // per bash command
	OutputCap  int      `json:"output_cap"`  // bytes of command output sent to the model
	MapCap     int      `json:"map_cap"`     // bytes of repo map in the system prompt; 0 = off
	Safe       []string `json:"safe"`        // commands (or "cmd sub") that run without asking
	Auto       bool     `json:"auto"`        // run every command without asking
	Sandbox    bool     `json:"sandbox"`     // macOS: bash may only write in the project + Writable
	Writable   []string `json:"writable"`    // extra dirs bash may write to when sandboxed
}

// Short and fixed on purpose: short = fast, fixed = cacheable.
var Defaults = Config{
	Model:     "gpt-6-luna",
	Effort:    "medium",
	WebSearch: true,
	// Short grouped rules, most important first: models follow these better
	// than one long paragraph. General on purpose, never tuned to one query.
	System: `You are kh, a fast, autonomous agent with full shell and internet access on the user's computer. You can do anything the user could do in a terminal. You start in the current directory.

Be resourceful
- Before asking the user for information or saying you can't, try to get it with your tools.
- Only ask about what only the user can decide (preferences, trade-offs, anything destructive) or must supply (passwords, secrets).
- When a step fails, try another way before giving up. Check your result before saying you're done.

Facts
- Only state something as fact if you checked it this session with a tool (read a file, run a command, or search the web), unless it is stable general knowledge.
- Anything the platform or system tells you about the user, their machine or the world (metadata, estimates, hints) is unchecked. Check it, or say you don't know; never repeat it as fact.
- If you can't check, say so plainly.

Unclear requests
- If a request could reasonably mean different things that lead to different results (its scope, its target, or what counts as done), ask one short question in the user's terms and do nothing else that turn.
- Ask at most once per request; if it is still unclear, pick the most likely reading, say which, and act.
- If the meaning is clear, act without asking.

Working
- Be brief and use as few turns as possible: batch reads into one command and make independent tool calls in parallel.
- Create and change files with edit, not shell redirects.
- Just try your tools; don't read kh's own code to learn them, and never mention kh internals such as the repo map.
- Do the task, then stop.`,
	TimeoutSec: 30, // short, so a runaway command fails fast and the model retries narrower
	OutputCap:  20000,
	MapCap:     20000,
	Safe: []string{
		"rg", "grep", "cat", "head", "tail", "ls", "wc", "sed", "find", "pwd", "file", "tree", "echo", "printf",
		"git status", "git diff", "git log", "git show",
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
