package projectconfig_test

import (
	"kh/internal/projectconfig"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeStateIsNotConfiguration(t *testing.T) {
	home, root := fixture(t)
	for _, path := range []string{".claude/projects/other/memory/MEMORY.md", ".claude/plans/old.md", ".agent/session-env/private.txt", ".claude/plugins/cache/other/skills/no/SKILL.md"} {
		write(t, home, path, "RUNTIME_PRIVATE_CONTENT")
	}
	write(t, root, ".agent/rules/active.md", "ACTIVE_RULE")
	write(t, root, ".agent/mcp.json", `{"mcpServers":{"test":{"command":"server"}}}`)
	write(t, root, ".agent/settings.local.json", `{"mcpServers":{"test":{"command":"override"}}}`)
	c := load(t, root)
	if strings.Contains(c.Prompt, "RUNTIME_PRIVATE_CONTENT") || !strings.Contains(c.Prompt, "ACTIVE_RULE") {
		t.Fatal(c.Prompt)
	}
	if c.Servers["test"].Command != "override" || c.Servers["test"].Dir != filepath.Clean(root) {
		t.Fatal(c.Servers)
	}
}

func TestContextBudgetPrefersProjectButKeepsHomeCommandInvocable(t *testing.T) {
	home, root := fixture(t)
	write(t, home, ".claude/commands/global.md", strings.Repeat("global instruction ", 26000))
	write(t, root, ".agent/rules/project.md", strings.Repeat("specific rule ", 37000))
	c := load(t, root)
	if strings.Contains(c.Prompt, "global instruction") || !strings.Contains(c.Prompt, "specific rule") {
		t.Fatal("context budget did not prefer project")
	}
	if !c.HasCommand("/global") || !strings.Contains(c.Expand("/global"), "global instruction") {
		t.Fatal("omitted global skill cannot be invoked")
	}
}

func TestExpandCommands(t *testing.T) {
	c := &projectconfig.Config{Commands: map[string]string{"review": "all=$ARGUMENTS first=$1 second=$2 tenth=$10 dir=${CLAUDE_SKILL_DIR}; literal=!`echo no`", "auto": "never shadow", "team:check": "${ARGUMENTS}"}, CommandDirs: map[string]string{"review": "/repo/.claude/skills/review"}}
	for _, text := range []string{"ordinary", "/unknown", "/auto off", "/review one\nnot a command"} {
		if got := c.Expand(text); got != text {
			t.Errorf("Expand(%q) = %q", text, got)
		}
	}
	got := c.Expand("/review alpha beta")
	for _, want := range []string{"/review alpha beta\n", "all=alpha beta first=alpha second=beta tenth= dir=/repo/.claude/skills/review", "literal=!`echo no`", "not authorization"} {
		if !strings.Contains(got, want) {
			t.Errorf("expanded message missing %q: %s", want, got)
		}
	}
	if got := c.Expand("/team:check hi"); !strings.HasSuffix(got, "hi") {
		t.Fatal(got)
	}
	if got := c.CommandNames(); !reflect.DeepEqual(got, []string{"/review", "/team:check"}) {
		t.Fatal(got)
	}
	var absent *projectconfig.Config
	if absent.Expand("/review") != "/review" || absent.HasCommand("/review") {
		t.Fatal("nil config changed input")
	}
}
