package projectconfig_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func plugin(t *testing.T, root, dir, manifest string) string {
	t.Helper()
	write(t, root, dir+"/.claude-plugin/plugin.json", manifest)
	return filepath.Join(root, filepath.FromSlash(dir))
}

func TestLocalPluginLazyNamespacedCommands(t *testing.T) {
	_, root := fixture(t)
	dir := plugin(t, root, ".claude/plugins/demo", `{"name":"demo"}`)
	write(t, dir, "commands/db/check.md", "PLUGIN_COMMAND_BODY ${CLAUDE_PLUGIN_ROOT} $ARGUMENTS")
	write(t, dir, "skills/review/SKILL.md", "---\ndescription: Review code safely\n---\nPLUGIN_SKILL_BODY")
	write(t, dir, "rules/guide.md", "PLUGIN_RULE_BODY")
	write(t, dir, "scripts/check.sh", "PLUGIN_SCRIPT_BODY")
	write(t, dir, "references/guide.md", "PLUGIN_REFERENCE_BODY")
	c := load(t, root)
	for _, name := range []string{"demo:db:check", "demo:review"} {
		if _, ok := c.Commands[name]; !ok {
			t.Errorf("missing namespaced command %s: %#v", name, c.CommandNames())
		}
	}
	for _, body := range []string{"PLUGIN_COMMAND_BODY", "PLUGIN_SKILL_BODY", "PLUGIN_RULE_BODY", "PLUGIN_SCRIPT_BODY", "PLUGIN_REFERENCE_BODY"} {
		if strings.Contains(c.Prompt, body) {
			t.Errorf("eager plugin body %s", body)
		}
	}
	expanded := c.Expand("/demo:db:check table")
	if !strings.Contains(expanded, "PLUGIN_COMMAND_BODY "+dir+" table") {
		t.Fatal(expanded)
	}
}

func TestPluginOnDemandFilesAndRecheckedSafety(t *testing.T) {
	_, root := fixture(t)
	dir := plugin(t, root, ".claude/plugins/demo", `{"name":"demo"}`)
	write(t, dir, "commands/run.md", "command body")
	script := write(t, dir, "scripts/check.sh", "script body")
	rule := write(t, dir, "rules/guide.md", "rule body")
	reference := write(t, dir, "references/guide.md", "reference body")
	secret := write(t, dir, "references/password.txt", "NEVER_EXPOSE")
	hook := write(t, dir, "hooks/setup.sh", "NEVER_EXECUTE")
	c := load(t, root)
	tool := c.Tools()[0]
	for _, tc := range []struct{ path, body string }{{script, "script body"}, {rule, "rule body"}, {reference, "reference body"}} {
		input, _ := json.Marshal(map[string]string{"action": "read", "path": tc.path})
		got, err := tool.Run(context.Background(), input)
		if err != nil || !strings.Contains(got, tc.body) {
			t.Fatalf("read %s: %q %v", tc.path, got, err)
		}
	}
	for _, path := range []string{secret, hook} {
		input, _ := json.Marshal(map[string]string{"action": "read", "path": path})
		if got, err := tool.Run(context.Background(), input); err == nil {
			t.Fatalf("unsafe asset indexed: %s", got)
		}
	}
	if err := os.Rename(filepath.Join(dir, "references"), filepath.Join(dir, "references-old")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	write(t, outside, "guide.md", "OUTSIDE_SECRET")
	if err := os.Symlink(outside, filepath.Join(dir, "references")); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"action": "read", "path": reference})
	if got, err := tool.Run(context.Background(), input); err == nil || strings.Contains(got, "OUTSIDE_SECRET") {
		t.Fatalf("changed ancestor read: %q %v", got, err)
	}
}

func TestPluginManifestPathsAndMCP(t *testing.T) {
	_, root := fixture(t)
	dir := plugin(t, root, ".agents/plugins/demo", `{"name":"demo","commands":["./extra/check.md"],"skills":"./extra-skills","rules":"./guidance","mcpServers":{"api":{"command":"node","args":["${CLAUDE_PLUGIN_ROOT}/server.js"],"env":{"API_TOKEN":"not-for-context"}}}}`)
	write(t, dir, "extra/check.md", "custom command")
	write(t, dir, "commands/default-command.md", "default command replaced")
	write(t, dir, "extra-skills/audit/SKILL.md", "audit skill")
	write(t, dir, "skills/standard/SKILL.md", "default skill remains")
	write(t, dir, "guidance/team.md", "guidance body")
	write(t, dir, ".mcp.json", `{"mcpServers":{"local":{"command":"${CLAUDE_PLUGIN_ROOT}/scripts/server"}}}`)
	c := load(t, root)
	for _, key := range []string{"demo:check", "demo:audit", "demo:standard"} {
		if _, ok := c.Commands[key]; !ok {
			t.Errorf("missing %s", key)
		}
	}
	if c.HasCommand("/demo:default-command") {
		t.Error("custom commands must replace default commands scan")
	}
	s, ok := c.Servers["demo:api"]
	if !ok || len(s.Args) != 1 || s.Args[0] != filepath.Join(dir, "server.js") || s.Dir != dir {
		t.Fatalf("MCP: %#v", c.Servers)
	}
	if c.Servers["demo:local"].Command != filepath.Join(dir, "scripts/server") {
		t.Fatal(c.Servers)
	}
	if strings.Contains(c.Prompt, "not-for-context") || strings.Contains(c.Prompt, "guidance body") {
		t.Fatal("plugin secrets/rules leaked into startup context")
	}
}

func TestPluginExplicitMCPFileAndAllRootFields(t *testing.T) {
	_, root := fixture(t)
	dir := plugin(t, root, ".claude/plugins/demo", `{"name":"demo","mcpServers":"./.mcp.json"}`)
	write(t, dir, "commands/run.md", "body")
	write(t, dir, ".mcp.json", `{"mcpServers":{"remote":{"url":"https://example.test/${CLAUDE_PLUGIN_ROOT}","headers":{"Authorization":"${CLAUDE_PLUGIN_ROOT}/secret"}},"stdio":{"command":"node","args":["${CLAUDE_PLUGIN_ROOT}/server.js"],"env":{"ROOT":"${CLAUDE_PLUGIN_ROOT}"}}}}`)
	c := load(t, root)
	if !c.HasCommand("/demo:run") || c.Servers["demo:stdio"].Env["ROOT"] != dir || c.Servers["demo:remote"].Headers["Authorization"] != dir+"/secret" || c.Servers["demo:remote"].URL != "https://example.test/"+dir {
		t.Fatal(c.Servers, c.CommandNames())
	}
	if strings.Contains(c.Prompt, "Authorization") {
		t.Fatal("connection values leaked")
	}
}

func TestPluginMCPWrappedObject(t *testing.T) {
	for _, sibling := range []string{"", `,"metadata":{"command":"must-not-run"}`} {
		t.Run(sibling, func(t *testing.T) {
			_, root := fixture(t)
			dir := plugin(t, root, ".claude/plugins/demo", `{"name":"demo"}`)
			write(t, dir, ".mcp.json", `{"mcpServers":{"api":{"command":"node"}}`+sibling+`}`)
			c := load(t, root)
			if len(c.Servers) != 1 || c.Servers["demo:api"].Command != "node" {
				t.Fatalf("wrapped MCP servers: %#v", c.Servers)
			}
			if len(c.Warnings) != 0 {
				t.Fatalf("valid wrapped MCP object warned: %v", c.Warnings)
			}
		})
	}
}

func TestPluginMCPRejectsInvalidWrappedObject(t *testing.T) {
	for _, nested := range []string{`null`, `[]`, `"bad"`, `42`, `true`} {
		t.Run(nested, func(t *testing.T) {
			_, root := fixture(t)
			dir := plugin(t, root, ".claude/plugins/demo", `{"name":"demo"}`)
			write(t, dir, ".mcp.json", `{"mcpServers":`+nested+`,"metadata":{"command":"must-not-run"}}`)
			c := load(t, root)
			if len(c.Servers) != 0 {
				t.Fatalf("invalid wrapped MCP object loaded servers: %#v", c.Servers)
			}
			if !strings.Contains(strings.Join(c.Warnings, "\n"), "invalid plugin mcpServers object") {
				t.Fatalf("invalid wrapped MCP object missing diagnostic: %v", c.Warnings)
			}
		})
	}
}

func TestPluginMCPConnectionFilesAreNotReadableAssets(t *testing.T) {
	_, root := fixture(t)
	dir := plugin(t, root, ".claude/plugins/demo", `{"name":"demo","mcpServers":"./references/connection.json"}`)
	path := write(t, dir, "references/connection.json", `{"api":{"command":"node","env":{"AUTH":"PRIVATE_CONNECTION_VALUE"}}}`)
	c := load(t, root)
	if c.Servers["demo:api"].Env["AUTH"] != "PRIVATE_CONNECTION_VALUE" {
		t.Fatal(c.Servers)
	}
	input, _ := json.Marshal(map[string]string{"action": "read", "path": path})
	if got, err := c.Tools()[0].Run(context.Background(), input); err == nil {
		t.Fatalf("connection file exposed through capability read: %s", got)
	}
}

func TestPluginMCPNamesMatchCoreDeclarations(t *testing.T) {
	_, root := fixture(t)
	plugin(t, root, ".claude/plugins/demo", `{"name":"demo","mcpServers":{"api_server":{"command":"node"},"apiServer":{"command":"node"}}}`)
	c := load(t, root)
	for _, name := range []string{"demo:api_server", "demo:apiServer"} {
		if _, ok := c.Servers[name]; !ok {
			t.Errorf("missing MCP server %s", name)
		}
	}
}

func TestPluginEnabledAndInstalledSubset(t *testing.T) {
	_, root := fixture(t)
	dir := plugin(t, root, ".claude/plugins/cache/market/demo/v1", `{"name":"demo"}`)
	write(t, dir, "commands/run.md", "installed command")
	records := map[string]any{"version": 2, "plugins": map[string]any{"demo@market": []any{map[string]any{"scope": "project", "projectPath": root, "installPath": dir}}}}
	data, _ := json.Marshal(records)
	write(t, root, ".claude/plugins/installed_plugins.json", string(data))
	write(t, root, ".claude/settings.json", `{"enabledPlugins":{"demo@market":false}}`)
	if c := load(t, root); c.HasCommand("/demo:run") {
		t.Fatal("disabled plugin loaded")
	}
	write(t, root, ".claude/settings.local.json", `{"enabledPlugins":{"demo@market":true}}`)
	if c := load(t, root); !c.HasCommand("/demo:run") {
		t.Fatal("local enabled override not applied")
	}
	records["plugins"] = map[string]any{"demo@market": []any{map[string]any{"scope": "project", "projectPath": filepath.Join(root, "other"), "installPath": dir}}}
	data, _ = json.Marshal(records)
	write(t, root, ".claude/plugins/installed_plugins.json", string(data))
	if c := load(t, root); c.HasCommand("/demo:run") {
		t.Fatal("another project's installed record loaded")
	}
}

func TestPluginRejectsUnsafeInstalledRecords(t *testing.T) {
	for _, variant := range []string{"outside", "traversal", "parent-symlink", "unenabled", "bad-settings"} {
		t.Run(variant, func(t *testing.T) {
			_, root := fixture(t)
			dir := plugin(t, root, ".claude/plugins/cache/demo/v1", `{"name":"demo"}`)
			write(t, dir, "commands/run.md", "installed body")
			install := dir
			switch variant {
			case "outside":
				install = plugin(t, t.TempDir(), "outside", `{"name":"demo"}`)
				write(t, install, "commands/run.md", "outside")
			case "traversal":
				install = filepath.Dir(dir) + "/../demo/v1"
			case "parent-symlink":
				moved := filepath.Join(root, "elsewhere")
				if err := os.Rename(filepath.Join(root, ".claude/plugins/cache"), moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, filepath.Join(root, ".claude/plugins/cache")); err != nil {
					t.Fatal(err)
				}
			}
			records := map[string]any{"version": 2, "plugins": map[string]any{"demo@market": []any{map[string]any{"scope": "user", "installPath": install}}}}
			data, _ := json.Marshal(records)
			write(t, root, ".claude/plugins/installed_plugins.json", string(data))
			if variant != "unenabled" {
				write(t, root, ".claude/settings.json", `{"enabledPlugins":{"demo@market":true}}`)
			}
			if variant == "bad-settings" {
				write(t, root, ".claude/settings.local.json", `{"enabledPlugins":{"demo@market":"true"}}`)
			}
			if c := load(t, root); c.HasCommand("/demo:run") {
				t.Fatal("unsafe/unenabled installed plugin loaded")
			}
		})
	}
}

func TestPluginRejectsUnsafePathsAndSymlinks(t *testing.T) {
	for _, unsafe := range []string{"../outside", "./../outside", "/tmp/outside", "./extra/../outside", "https://example.com/plugin", "commands/plain.md", "./.env", "./hooks"} {
		t.Run(unsafe, func(t *testing.T) {
			_, root := fixture(t)
			manifest, _ := json.Marshal(map[string]any{"name": "demo", "commands": unsafe})
			dir := plugin(t, root, ".claude/plugins/demo", string(manifest))
			write(t, dir, "commands/run.md", "must not register unsafe manifest")
			c := load(t, root)
			if c.HasCommand("/demo:run") {
				t.Fatal("unsafe manifest was not rejected")
			}
			if len(c.Warnings) == 0 {
				t.Fatal("unsafe path had no diagnostic")
			}
		})
	}
	t.Run("symlink", func(t *testing.T) {
		_, root := fixture(t)
		outside := t.TempDir()
		plugin(t, outside, "bundle", `{"name":"escape"}`)
		write(t, outside, "bundle/commands/run.md", "ESCAPED_BODY")
		write(t, root, ".claude/plugins/placeholder", "inert")
		if err := os.Symlink(filepath.Join(outside, "bundle"), filepath.Join(root, ".claude/plugins/escape")); err != nil {
			t.Fatal(err)
		}
		c := load(t, root)
		if c.HasCommand("/escape:run") || strings.Contains(c.Prompt, "ESCAPED_BODY") {
			t.Fatal("symlink followed")
		}
	})
}
