// Local plugin bundles contribute inert capabilities, never installers or hooks.
package projectconfig

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var pluginName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// plugins discovers direct local bundles and explicitly enabled v2 installPath
// records confined to this configuration's plugins directory. It never resolves
// marketplace names, downloads packages, or executes installation hooks.
func (l *loader) plugins(root, base string) {
	dir := filepath.Join(root, "plugins")
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		return
	}
	if err := safeAssetPath(dir, root); err != nil {
		l.warn(dir, "unsafe plugin directory")
		return
	}
	enabled := map[string]bool{}
	valid := true
	for _, name := range []string{"settings.json", "settings.local.json"} {
		path := filepath.Join(root, name)
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		}
		data, ok := l.read(path)
		if !ok {
			valid = false
			continue
		}
		var values map[string]json.RawMessage
		if json.Unmarshal(data, &values) != nil {
			valid = false
			continue
		}
		if raw, ok := values["enabledPlugins"]; ok {
			var flags map[string]bool
			if string(raw) == "null" || json.Unmarshal(raw, &flags) != nil {
				l.warn(path, "invalid enabledPlugins boolean map; plugins skipped")
				valid = false
				continue
			}
			for key, value := range flags {
				enabled[key] = value
			}
		}
	}
	if !valid {
		return
	}
	seen := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		l.warn(dir, "cannot read plugin directory")
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || sensitive(entry.Name()) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if _, err := os.Lstat(filepath.Join(path, ".claude-plugin", "plugin.json")); err == nil {
			l.pluginBundle(path, dir, "", enabled, seen)
		}
	}
	registry := filepath.Join(dir, "installed_plugins.json")
	data, ok := l.read(registry)
	if !ok {
		return
	}
	var installed struct {
		Version int `json:"version"`
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
			Scope       string `json:"scope"`
			ProjectPath string `json:"projectPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(data, &installed) != nil || installed.Version != 2 {
		l.warn(registry, "unsupported installed_plugins.json (expected version 2 local installPath records)")
		return
	}
	keys := make([]string, 0, len(installed.Plugins))
	for key := range installed.Plugins {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !enabled[key] {
			continue
		}
		for _, record := range installed.Plugins[key] {
			switch record.Scope {
			case "user":
			case "project", "local":
				if filepath.Clean(record.ProjectPath) != base {
					continue
				}
			default:
				l.warn(registry, "unsupported plugin install scope")
				continue
			}
			if !filepath.IsAbs(record.InstallPath) || filepath.Clean(record.InstallPath) != record.InstallPath {
				l.warn(registry, "plugin installPath must be absolute and inside plugins directory")
				continue
			}
			l.pluginBundle(record.InstallPath, dir, key, enabled, seen)
		}
	}
}

// pluginPaths accepts only explicit ./ paths without traversal, hidden or
// sensitive components. Skills supplement defaults; commands replace defaults.
func pluginPaths(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	var paths []string
	var path string
	if json.Unmarshal(raw, &path) == nil {
		paths = []string{path}
	} else if json.Unmarshal(raw, &paths) != nil || paths == nil {
		return nil, false
	}
	for _, path := range paths {
		if !strings.HasPrefix(path, "./") || strings.Contains(path, `\`) {
			return nil, false
		}
		for _, part := range strings.Split(strings.TrimPrefix(path, "./"), "/") {
			if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") || sensitive(part) || part == "hooks" {
				return nil, false
			}
		}
	}
	return paths, true
}

func (l *loader) pluginBundle(root, boundary, identity string, enabled map[string]bool, seen map[string]bool) {
	root = filepath.Clean(root)
	if seen[root] {
		return
	}
	if err := safeAssetPath(root, boundary); err != nil {
		l.warn(root, "unsafe plugin install path")
		return
	}
	manifest := filepath.Join(root, ".claude-plugin", "plugin.json")
	if err := safeAssetPath(manifest, boundary); err != nil {
		l.warn(manifest, "unsafe plugin manifest path")
		return
	}
	data, ok := l.read(manifest)
	if !ok {
		return
	}
	var m struct {
		Name     string          `json:"name"`
		Commands json.RawMessage `json:"commands"`
		Skills   json.RawMessage `json:"skills"`
		Rules    json.RawMessage `json:"rules"`
		MCP      json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(data, &m) != nil || !pluginName.MatchString(m.Name) {
		l.warn(manifest, "invalid plugin manifest/name")
		return
	}
	if identity != "" && strings.Split(identity, "@")[0] != m.Name {
		l.warn(manifest, "installed plugin identity does not match manifest name")
		return
	}
	if value, ok := enabled[m.Name]; ok && !value {
		return
	}
	if identity == "" {
		// A qualified disabled declaration must also disable the direct bundle.
		for key, value := range enabled {
			if strings.Split(key, "@")[0] == m.Name && !value {
				return
			}
		}
	}
	components := map[string][]string{}
	for _, part := range []struct {
		name string
		raw  json.RawMessage
	}{{"commands", m.Commands}, {"skills", m.Skills}, {"rules", m.Rules}} {
		paths, valid := pluginPaths(part.raw)
		if !valid {
			l.warn(manifest, "unsafe or unsupported plugin "+part.name+" paths; bundle skipped")
			return
		}
		components[part.name] = append([]string{"./" + part.name}, paths...)
		if part.name == "commands" && len(part.raw) > 0 {
			components[part.name] = paths
		}
	}
	var mcpPath string
	if len(m.MCP) > 0 && m.MCP[0] != '{' {
		paths, valid := pluginPaths(m.MCP)
		// The canonical MCP filename is the only hidden component exception.
		var declared string
		if json.Unmarshal(m.MCP, &declared) == nil && declared == "./.mcp.json" {
			paths, valid = []string{declared}, true
		}
		if !valid || len(paths) != 1 {
			l.warn(manifest, "unsafe or unsupported plugin mcpServers; bundle skipped")
			return
		}
		mcpPath = paths[0]
	}
	// Validate declared existing paths before registering any part of the bundle.
	for _, paths := range components {
		for _, rel := range paths {
			path := filepath.Join(root, rel)
			if _, err := os.Lstat(path); os.IsNotExist(err) {
				continue
			}
			if err := safeAssetPath(path, boundary); err != nil {
				l.warn(path, "unsafe plugin component path; bundle skipped")
				return
			}
		}
	}
	if mcpPath != "" {
		path := filepath.Join(root, mcpPath)
		if err := safeAssetPath(path, boundary); err != nil {
			l.warn(path, "unsafe plugin MCP path; bundle skipped")
			return
		}
	}
	seen[root] = true
	for _, kind := range []string{"commands", "skills", "rules"} {
		for _, rel := range components[kind] {
			l.pluginComponent(root, boundary, filepath.Join(root, rel), kind, m.Name)
		}
	}
	for _, kind := range []string{"scripts", "references", "assets"} {
		l.pluginComponent(root, boundary, filepath.Join(root, kind), kind, m.Name)
	}
	l.pluginMCPFile(filepath.Join(root, ".mcp.json"), root, boundary, m.Name)
	if mcpPath != "" {
		// Connection files can contain secrets, even under a reference directory.
		delete(l.config.assets, filepath.Join(root, mcpPath))
		l.pluginMCPFile(filepath.Join(root, mcpPath), root, boundary, m.Name)
	} else if len(m.MCP) > 0 {
		l.pluginMCP(m.MCP, root, m.Name, manifest)
	}
}

// pluginComponent indexes a bounded, symlink-free component tree. Rules are
// inert on-demand references, not global instructions. Hooks are never indexed.
func (l *loader) pluginComponent(root, boundary, path, kind, namespace string) {
	count := 0
	_ = filepath.WalkDir(path, func(current string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			l.warn(current, "cannot read plugin component")
			return nil
		}
		count++
		if count > maxAssets {
			l.warn(path, "plugin component exceeds 4096 entries")
			return fs.SkipAll
		}
		if strings.HasPrefix(entry.Name(), ".") || sensitive(entry.Name()) || entry.Name() == "hooks" || entry.Name() == "node_modules" || entry.Name() == "__pycache__" {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if err := safeAssetPath(current, boundary); err != nil {
			l.warn(current, "unsafe plugin component skipped")
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		key := ""
		if kind == "commands" && filepath.Ext(current) == ".md" {
			rel, _ := filepath.Rel(path, current)
			if rel == "." {
				rel = filepath.Base(current)
			}
			key = strings.ReplaceAll(strings.TrimSuffix(filepath.ToSlash(rel), ".md"), "/", ":")
		} else if kind == "skills" && entry.Name() == "SKILL.md" {
			key = filepath.Base(filepath.Dir(current))
		}
		if key != "" {
			key = namespace + ":" + key
			l.command(key, current)
			if l.config.commandPaths[key] == current {
				l.config.commandRoots[key] = root
				l.asset(current, boundary, "")
			}
			return nil
		}
		assetKind := "reference"
		if kind == "scripts" || filepath.Ext(current) == ".sh" || filepath.Ext(current) == ".py" || filepath.Ext(current) == ".js" {
			assetKind = "script"
		}
		if kind == "rules" {
			assetKind = "rule"
		}
		if kind == "assets" {
			assetKind = "asset"
		}
		l.asset(current, boundary, assetKind)
		return nil
	})
}

func (l *loader) pluginMCPFile(path, root, boundary, namespace string) {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return
	}
	if err := safeAssetPath(path, boundary); err != nil {
		l.warn(path, "unsafe plugin MCP file")
		return
	}
	if data, ok := l.read(path); ok {
		l.pluginMCP(data, root, namespace, path)
	}
}

// pluginMCP keeps connection secrets out of context. Root substitutions happen
// in every server field before the existing MCP layer validates/starts servers.
func (l *loader) pluginMCP(data []byte, root, namespace, path string) {
	var declarations map[string]json.RawMessage
	if json.Unmarshal(data, &declarations) != nil || declarations == nil {
		l.warn(path, "invalid plugin MCP object")
		return
	}
	if nested, ok := declarations["mcpServers"]; ok {
		var servers map[string]json.RawMessage
		if json.Unmarshal(nested, &servers) != nil || servers == nil {
			l.warn(path, "invalid plugin mcpServers object")
			return
		}
		declarations = servers
	}
	names := make([]string, 0, len(declarations))
	for name := range declarations {
		names = append(names, name)
	}
	sort.Strings(names)
	replace := func(value string) string { return strings.ReplaceAll(value, "${CLAUDE_PLUGIN_ROOT}", root) }
	for _, name := range names {
		var server Server
		if strings.TrimSpace(name) == "" || json.Unmarshal(declarations[name], &server) != nil || server.Command == "" && server.URL == "" {
			l.warn(path, "invalid plugin MCP server declaration")
			continue
		}
		server.Command = replace(server.Command)
		server.URL = replace(server.URL)
		server.Type = replace(server.Type)
		for i, arg := range server.Args {
			server.Args[i] = replace(arg)
		}
		for key, value := range server.Env {
			server.Env[key] = replace(value)
		}
		for key, value := range server.Headers {
			server.Headers[key] = replace(value)
		}
		server.Dir = root
		l.config.Servers[namespace+":"+name] = server
	}
}
