package tools

import (
	"encoding/json"
	"strings"
)

// CommandLabel is shared by live shell approval and saved-call replay.
func CommandLabel(command string) string { return "$ " + command }

// DescribeCall formats a saved tool call by its declared name, never by guessing
// from argument fields. Unknown tools retain their identity without displaying
// arbitrary arguments (which may contain secrets or large edit payloads).
func DescribeCall(name string, input json.RawMessage) []string {
	var args struct {
		Command, Path, Action, Address, Name string
		Edits                                []struct{ Path string }
	}
	if json.Unmarshal(input, &args) != nil {
		return []string{toolLabel(name)}
	}
	switch name {
	case "bash":
		if args.Command != "" {
			return []string{CommandLabel(args.Command)}
		}
	case "edit":
		var paths []string
		seen := make(map[string]bool)
		for _, edit := range args.Edits {
			if edit.Path != "" && !seen[edit.Path] {
				paths = append(paths, "edit "+edit.Path)
				seen[edit.Path] = true
			}
		}
		if len(paths) > 0 {
			return paths
		}
		if args.Path != "" {
			return []string{"edit " + args.Path}
		}
	case "memory":
		return []string{strings.TrimSpace("memory " + args.Action)}
	case "agents":
		label := strings.TrimSpace("agents " + args.Action)
		if args.Action == "spawn" && args.Name != "" {
			label += " " + args.Name
		}
		if (args.Action == "send" || args.Action == "peek") && args.Address != "" {
			label += " " + args.Address
		}
		return []string{label}
	}
	return []string{toolLabel(name)}
}

func toolLabel(name string) string {
	if name == "" {
		return "tool [unknown]"
	}
	return "tool " + name
}
