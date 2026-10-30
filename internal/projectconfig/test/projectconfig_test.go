package projectconfig_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"kh/internal/projectconfig"
)

func write(t *testing.T, root, path, text string) string {
	t.Helper()
	path = filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixture(t *testing.T) (home, root string) {
	t.Helper()
	var err error
	home, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	return home, root
}

func load(t *testing.T, dir string) *projectconfig.Config {
	t.Helper()
	c, err := projectconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDiscoveryAndOverrides(t *testing.T) {
	home, root := fixture(t)
	write(t, home, ".claude/commands/global.md", "home command")
	write(t, home, ".agent/rules/home.md", "home rule")
	write(t, root, ".claude/commands/nested/do.md", "ancestor claude")
	write(t, root, ".agent/commands/nested/do.md", "ancestor agent")
	write(t, root, ".claude/rules/first.md", "ancestor context")
	write(t, root, "child/.claude/commands/nested/do.md", "child claude")
	write(t, root, "child/.agent/commands/nested/do.md", "child agent")
	write(t, root, "child/.agent/agents/reviewer.md", "review context")
	write(t, root, "unrelated/.agent/rules/no.md", "UNRELATED_CONTENT")
	write(t, root, "child/deeper/.agent/rules/no.md", "DESCENDANT_CONTENT")
	c := load(t, filepath.Join(root, "child"))
	if !strings.Contains(c.Expand("/nested:do"), "child agent") || !strings.Contains(c.Expand("/global"), "home command") {
		t.Fatalf("commands: %#v", c.Commands)
	}
	if c.CommandDirs["nested:do"] != filepath.Join(root, "child/.agent/commands/nested") {
		t.Fatal(c.CommandDirs)
	}
	for _, want := range []string{"home rule", "ancestor context", "Source: " + filepath.Join(root, ".claude/rules/first.md"), "lower priority than core instructions"} {
		if !strings.Contains(c.Prompt, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, unwanted := range []string{"UNRELATED_CONTENT", "DESCENDANT_CONTENT"} {
		if strings.Contains(c.Prompt, unwanted) {
			t.Error(unwanted)
		}
	}
	for _, body := range []string{"ancestor claude", "ancestor agent", "child claude", "review context"} {
		if strings.Contains(c.Prompt, body) {
			t.Errorf("eager capability body: %s", body)
		}
	}
	again := load(t, filepath.Join(root, "child"))
	if !reflect.DeepEqual(c, again) {
		t.Fatal("load is nondeterministic")
	}
}

func TestHomeDeduplicated(t *testing.T) {
	home, _ := fixture(t)
	write(t, home, ".agent/rules/home.md", "unique-home-content")
	child := filepath.Join(home, "work")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	c := load(t, child)
	if strings.Count(c.Prompt, "unique-home-content") != 1 {
		t.Fatal("home was loaded twice")
	}
}

func TestSkillsCommandsAndAssets(t *testing.T) {
	_, root := fixture(t)
	skill := "---\nname: deploy\nallowed-tools: Bash\n---\n# Deploy\nUse $ARGUMENTS and $1.\nComplete workflow."
	path := write(t, root, ".claude/skills/deploy/SKILL.md", skill)
	write(t, root, ".claude/skills/deploy/reference.md", "supporting reference")
	write(t, root, ".claude/skills/deploy/scripts/run.sh", "DO_NOT_LOAD_SCRIPT")
	write(t, root, ".agent/hooks/run.sh", "DO_NOT_RUN_HOOK")
	write(t, root, ".agent/plugins/config.json", `{"private":"DO_NOT_LOAD_CONFIG"}`)
	write(t, root, ".agent/note.txt", "plain text context")
	c := load(t, root)
	if !strings.Contains(c.Expand("/deploy"), "Complete workflow.") || c.Commands["deploy"] != "" || c.CommandDirs["deploy"] != filepath.Dir(path) {
		t.Fatalf("skill: %#v", c.Commands)
	}
	for _, want := range []string{"plain text context", "Project skill /deploy", "project_capability"} {
		if !strings.Contains(c.Prompt, want) {
			t.Error("missing", want)
		}
	}
	for _, unwanted := range []string{skill, "supporting reference", "DO_NOT_LOAD_SCRIPT", "DO_NOT_RUN_HOOK", "DO_NOT_LOAD_CONFIG"} {
		if strings.Contains(c.Prompt, unwanted) {
			t.Error(unwanted)
		}
	}
	warnings := strings.Join(c.Warnings, "\n")
	if !strings.Contains(warnings, "frontmatter") || !strings.Contains(warnings, "unsupported executable capability") {
		t.Fatal(warnings)
	}
}

func TestMCPMergeAndSensitiveConfigNotInPrompt(t *testing.T) {
	_, root := fixture(t)
	write(t, root, ".mcp.json", `{"mcpServers":{"shared":{"command":"root"},"root":{"url":"https://example.invalid","headers":{"Authorization":"PRIVATE_HEADER"},"type":"http"}}}`)
	write(t, root, ".claude/settings.json", `{"hooks":{"command":"DO_NOT_RUN"},"permissions":{"allow":["*"]},"mcpServers":{"shared":{"command":"claude"}}}`)
	write(t, root, ".claude/settings.local.json", `{"mcpServers":{"shared":{"command":"local"}}}`)
	write(t, root, ".agent/.mcp.json", `{"mcpServers":{"shared":{"command":"agent","args":["one"],"env":{"API_KEY":"PRIVATE_ENV"},"unsupported":"PRIVATE_UNKNOWN"}}}`)
	write(t, root, "child/.mcp.json", `{"mcpServers":{"shared":{"command":"child"}}}`)
	c := load(t, root)
	server := c.Servers["shared"]
	if server.Command != "agent" || server.Dir != root || server.Env["API_KEY"] != "PRIVATE_ENV" || !reflect.DeepEqual(server.Args, []string{"one"}) {
		t.Fatalf("server: %#v", server)
	}
	if c.Servers["root"].Headers["Authorization"] != "PRIVATE_HEADER" {
		t.Fatal(c.Servers)
	}
	for _, unwanted := range []string{"PRIVATE_HEADER", "PRIVATE_ENV", "PRIVATE_UNKNOWN", "DO_NOT_RUN"} {
		if strings.Contains(c.Prompt, unwanted) {
			t.Error(unwanted)
		}
	}
	warnings := strings.Join(c.Warnings, "\n")
	for _, want := range []string{"unsupported setting hooks", "unsupported setting permissions", "unsupported MCP server field shared.unsupported"} {
		if !strings.Contains(warnings, want) {
			t.Error(warnings)
		}
	}
	child := load(t, filepath.Join(root, "child"))
	if child.Servers["shared"].Command != "child" {
		t.Fatal(child.Servers)
	}
	// Without .agent, local settings override settings.json.
	if err := os.Rename(filepath.Join(root, ".agent"), filepath.Join(root, "disabled-agent")); err != nil {
		t.Fatal(err)
	}
	if load(t, root).Servers["shared"].Command != "local" {
		t.Fatal("local settings not applied last")
	}
}

func TestUnsafeFilesAndSymlinksAreSkipped(t *testing.T) {
	_, root := fixture(t)
	outside := t.TempDir()
	target := write(t, outside, "external.md", "OUTSIDE_CONTENT")
	write(t, root, ".claude/rules/safe.md", "safe rule")
	write(t, root, ".claude/.env", "ENV_SECRET")
	write(t, root, ".claude/secrets/info.md", "NESTED_SECRET")
	write(t, root, ".claude/token.txt", "TOKEN_SECRET")
	write(t, root, ".claude/binary.md", "BINARY_SECRET\x00")
	write(t, root, ".claude/large.md", strings.Repeat("x", 512*1024+1))
	if err := os.Symlink(target, filepath.Join(root, ".claude/linked.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".agent")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ".mcp.json")); err != nil {
		t.Fatal(err)
	}
	c := load(t, root)
	if !strings.Contains(c.Prompt, "safe rule") {
		t.Fatal(c.Prompt)
	}
	for _, unwanted := range []string{"OUTSIDE_CONTENT", "ENV_SECRET", "NESTED_SECRET", "TOKEN_SECRET", "BINARY_SECRET"} {
		if strings.Contains(c.Prompt, unwanted) {
			t.Error(unwanted)
		}
	}
	warnings := strings.Join(c.Warnings, "\n")
	for _, want := range []string{"symlink", "sensitive", "binary", "512 KiB"} {
		if !strings.Contains(warnings, want) {
			t.Error(warnings)
		}
	}
}

func TestInvalidJSONAndWorkingDirectory(t *testing.T) {
	_, root := fixture(t)
	write(t, root, ".claude/settings.json", `{bad`)
	write(t, root, ".agent/settings.json", `{"mcpServers":{"bad":42,"empty":{},"good":{"command":"ok"}}}`)
	c := load(t, root)
	if len(c.Warnings) < 3 || c.Servers["good"].Command != "ok" {
		t.Fatalf("%#v", c)
	}
	if _, err := projectconfig.Load(filepath.Join(root, "missing")); err == nil {
		t.Error("missing cwd accepted")
	}
	file := write(t, root, "file", "x")
	if _, err := projectconfig.Load(file); err == nil {
		t.Error("file cwd accepted")
	}
}

func TestStartupFiltersRuntimeAndTrash(t *testing.T) {
	_, root := fixture(t)
	for _, path := range []string{".claude/skills/.trash/old/SKILL.md", ".claude/daemon.log", ".claude/history.jsonl", ".claude/.claude.json", ".claude/settings.json.bak-123", ".claude/stats-cache.json"} {
		write(t, root, path, "RUNTIME-CONTENT")
	}
	write(t, root, ".claude/CLAUDE.md", "CORE-INSTRUCTIONS")
	c := load(t, root)
	for _, unwanted := range []string{"RUNTIME-CONTENT", ".trash", "daemon.log", "history.jsonl", "stats-cache.json", "settings.json.bak"} {
		if strings.Contains(c.Prompt, unwanted) || strings.Contains(strings.Join(c.Warnings, "\n"), unwanted) {
			t.Errorf("runtime entry loaded or diagnosed: %s", unwanted)
		}
	}
	if c.HasCommand("/old") || !strings.Contains(c.Prompt, "CORE-INSTRUCTIONS") {
		t.Fatal("trash registered or core instructions missing")
	}
}

func TestContextPriorityAndOmittedCommand(t *testing.T) {
	home, root := fixture(t)
	write(t, home, ".claude/CLAUDE.md", "HOME-CORE")
	write(t, home, ".claude/rules/core.md", "HOME-RULE")
	write(t, home, ".claude/skills/deploy/SKILL.md", "DEPLOY-ENTRY")
	for i := 0; i < 4; i++ {
		write(t, home, fmt.Sprintf(".claude/skills/deploy/references/%d.md", i), strings.Repeat("reference", 60000))
	}
	write(t, root, ".agent/CLAUDE.md", "PROJECT-CORE")
	c := load(t, root)
	for _, want := range []string{"HOME-CORE", "HOME-RULE", "Project skill /deploy", "PROJECT-CORE"} {
		if !strings.Contains(c.Prompt, want) {
			t.Errorf("lost priority context: %s", want)
		}
	}
	if len(c.Prompt) > 768*1024+4096 {
		t.Fatal("unbounded prompt", len(c.Prompt))
	}
	// Entry points remain invocable even when higher-priority rules fill the budget.
	write(t, root, ".agent/rules/full.md", strings.Repeat("rule", 128000))
	body := strings.Repeat("command", 70000)
	write(t, root, ".agent/skills/large/SKILL.md", body)
	c = load(t, root)
	if strings.Contains(c.Prompt, body) || !strings.Contains(c.Expand("/large"), body) {
		t.Fatal("omitted capability not available on explicit invocation")
	}
	write(t, root, ".agent/commands/project.md", "SHADOW")
	if load(t, root).HasCommand("/project diagnostics") {
		t.Fatal("project capability shadows diagnostics")
	}
}

func TestContextBudget(t *testing.T) {
	_, root := fixture(t)
	for i := 0; i < 10; i++ {
		write(t, root, ".agent/rules/"+string(rune('a'+i))+".md", strings.Repeat(string(rune('a'+i)), 512*1024))
	}
	c := load(t, root)
	if len(c.Prompt) > 768*1024+4096 {
		t.Fatal("unbounded prompt", len(c.Prompt))
	}
	if !strings.Contains(strings.Join(c.Warnings, "\n"), "768 KiB") {
		t.Fatal(c.Warnings)
	}
}
