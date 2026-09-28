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
	Effort     string   `json:"effort"` // reasoning: low, medium, high
	System     string   `json:"system"`
	TimeoutSec int      `json:"timeout_sec"` // per bash command
	OutputCap  int      `json:"output_cap"`  // bytes of command output sent to the model
	Safe       []string `json:"safe"`        // commands (or "cmd sub") that run without asking
	Yes        bool     `json:"yes"`         // run every command without asking
}

// Short and fixed on purpose: short = fast, fixed = cacheable.
var Defaults = Config{
	Model:  "gpt-6-luna",
	Effort: "medium",
	System: "You are kh, a fast coding agent working in the current directory. " +
		"Be brief. Use as few turns as possible: batch reads into one command and make independent tool calls in parallel. " +
		"Do the task, then stop.",
	TimeoutSec: 120,
	OutputCap:  20000,
	Safe: []string{
		"rg", "grep", "cat", "head", "tail", "ls", "wc", "sed", "find", "pwd", "file", "tree",
		"git status", "git diff", "git log", "git show",
	},
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
