// Plugin hook discovery is inert. Neither discovery nor enabledPlugins grants
// execution authority; application callers must check persisted plugin trust.
package projectconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Hook is a normalized command declaration. Command and Args are executable argv,
// not Markdown. A shell command string is explicitly normalized to /bin/sh -c.
// TrustID fingerprints this plugin's root, identity and complete hook definitions.
// Env and Args retain literal environment substitutions for the runtime to expand.
type Hook struct {
	Event        string
	Matcher      string
	Plugin       string
	PluginRoot   string
	Source       string
	Command      string
	Args         []string
	Env          map[string]string
	Timeout      time.Duration
	RequiresNode bool
	TrustID      string
}

const maxPluginHooks = 256

func (l *loader) pluginHooks(root, identity string, raw json.RawMessage) {
	var documents []struct {
		path string
		data []byte
	}
	if len(raw) > 0 && raw[0] == '{' {
		documents = append(documents, struct {
			path string
			data []byte
		}{filepath.Join(root, ".claude-plugin", "plugin.json"), raw})
	} else {
		paths := []string{"./hooks/hooks.json"}
		if len(raw) > 0 {
			var path string
			if json.Unmarshal(raw, &path) == nil {
				paths = []string{path}
			} else if json.Unmarshal(raw, &paths) != nil || paths == nil {
				l.warn(root, "unsupported plugin hooks declaration; expected object or ./ JSON paths")
				return
			}
		}
		for _, rel := range paths {
			if !safeHookRelative(rel) {
				l.warn(root, "unsafe plugin hooks path")
				continue
			}
			path := filepath.Join(root, rel)
			if _, err := l.lstat(path); os.IsNotExist(err) {
				continue
			}
			if err := l.safePath(path, root); err != nil {
				l.warn(path, "unsafe plugin hooks file")
				continue
			}
			if data, ok := l.read(path); ok {
				documents = append(documents, struct {
					path string
					data []byte
				}{path, data})
			}
		}
	}
	var hooks []Hook
	for _, document := range documents {
		hooks = append(hooks, l.parsePluginHooks(root, identity, document.path, document.data)...)
	}
	if len(hooks) == 0 {
		return
	}
	if len(hooks) > maxPluginHooks {
		l.warn(root, "plugin hooks exceed 256 declarations; skipped")
		return
	}
	encoded, _ := json.Marshal(struct {
		Root, Identity string
		Hooks          []Hook
	}{root, identity, hooks})
	id := digest(encoded)
	for i := range hooks {
		hooks[i].TrustID = id
	}
	l.config.Hooks = append(l.config.Hooks, hooks...)
}

func safeHookRelative(path string) bool {
	if !strings.HasPrefix(path, "./") || strings.Contains(path, `\`) || filepath.Ext(path) != ".json" {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "./"), "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") || sensitive(part) {
			return false
		}
	}
	return true
}

func (l *loader) parsePluginHooks(root, identity, source string, data []byte) []Hook {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		l.warn(source, "invalid plugin hooks JSON object")
		return nil
	}
	if wrapped, ok := object["hooks"]; ok {
		if json.Unmarshal(wrapped, &object) != nil || object == nil {
			l.warn(source, "invalid plugin hooks map")
			return nil
		}
	}
	events := make([]string, 0, len(object))
	for event := range object {
		events = append(events, event)
	}
	sort.Strings(events)
	var hooks []Hook
	for _, event := range events {
		switch event {
		case "SessionStart", "UserPromptSubmit", "Stop", "PreToolUse", "SessionEnd":
		default:
			l.warn(source, "unsupported plugin hook event "+event)
			continue
		}
		var groups []map[string]json.RawMessage
		if json.Unmarshal(object[event], &groups) != nil || groups == nil {
			l.warn(source, "invalid plugin hook groups for "+event)
			continue
		}
		for _, group := range groups {
			var matcher string
			var declarations []json.RawMessage
			valid := group != nil
			for key := range group {
				if key != "matcher" && key != "hooks" {
					valid = false
				}
			}
			if raw, ok := group["matcher"]; ok && json.Unmarshal(raw, &matcher) != nil {
				valid = false
			}
			if json.Unmarshal(group["hooks"], &declarations) != nil || declarations == nil {
				valid = false
			}
			if !valid {
				l.warn(source, "unsupported or invalid plugin hook group for "+event)
				continue
			}
			for _, raw := range declarations {
				hook, err := parsePluginHook(raw)
				if err != nil {
					l.warn(source, "unsupported plugin hook: "+err.Error())
					continue
				}
				hook.Event, hook.Matcher, hook.Plugin, hook.PluginRoot, hook.Source = event, matcher, identity, root, source
				hooks = append(hooks, hook)
			}
		}
	}
	return hooks
}

func parsePluginHook(raw json.RawMessage) (Hook, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return Hook{}, fmt.Errorf("expected command object")
	}
	for key := range fields {
		switch key {
		case "type", "command", "args", "argv", "timeout", "env", "requiresNode":
		default:
			return Hook{}, fmt.Errorf("unknown command field %s", key)
		}
	}
	var declaration struct {
		Type         string            `json:"type"`
		Command      string            `json:"command"`
		Args         []string          `json:"args"`
		Argv         []string          `json:"argv"`
		Timeout      *float64          `json:"timeout"`
		Env          map[string]string `json:"env"`
		RequiresNode bool              `json:"requiresNode"`
	}
	if json.Unmarshal(raw, &declaration) != nil {
		return Hook{}, fmt.Errorf("invalid command/args/argv/timeout/env metadata")
	}
	if declaration.Type != "command" {
		return Hook{}, fmt.Errorf("only type command is supported")
	}
	h := Hook{Command: declaration.Command, Args: declaration.Args, Env: declaration.Env, Timeout: 5 * time.Second, RequiresNode: declaration.RequiresNode}
	if declaration.Timeout != nil {
		if *declaration.Timeout <= 0 || *declaration.Timeout > 30 {
			return Hook{}, fmt.Errorf("timeout must be positive and at most 30 seconds")
		}
		h.Timeout = time.Duration(*declaration.Timeout * float64(time.Second))
		if h.Timeout <= 0 {
			return Hook{}, fmt.Errorf("timeout must be at least one nanosecond")
		}
	}
	if _, ok := fields["argv"]; ok {
		if len(declaration.Argv) == 0 || declaration.Command != "" || fields["args"] != nil {
			return Hook{}, fmt.Errorf("argv must be nonempty and cannot mix with command/args")
		}
		h.Command, h.Args = declaration.Argv[0], declaration.Argv[1:]
	} else if _, ok := fields["args"]; !ok {
		if strings.TrimSpace(h.Command) == "" {
			return Hook{}, fmt.Errorf("empty command")
		}
		// Recognize the common Node shell form without attempting shell parsing.
		// More complex wrappers can declare requiresNode explicitly.
		if words := strings.Fields(h.Command); len(words) > 0 && words[0] == "node" {
			h.RequiresNode = true
		}
		h.Command, h.Args = "/bin/sh", []string{"-c", h.Command}
	}
	if strings.TrimSpace(h.Command) == "" || strings.ContainsRune(h.Command, 0) {
		return Hook{}, fmt.Errorf("empty or invalid executable")
	}
	if h.Command == "node" || filepath.Base(h.Command) == "node" {
		h.RequiresNode = true
	}
	for _, arg := range h.Args {
		if strings.ContainsRune(arg, 0) {
			return Hook{}, fmt.Errorf("NUL in argument")
		}
	}
	for key, value := range h.Env {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return Hook{}, fmt.Errorf("invalid environment entry")
		}
	}
	return h, nil
}
