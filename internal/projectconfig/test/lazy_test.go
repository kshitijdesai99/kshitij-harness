package projectconfig_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func capability(t *testing.T, root string, input string) (string, error) {
	t.Helper()
	c := load(t, root)
	for _, tool := range c.Tools() {
		if tool.Name == "project_capability" {
			return tool.Run(context.Background(), json.RawMessage(input))
		}
	}
	t.Fatal("missing project_capability tool")
	return "", nil
}

func TestHomeAncestorPrecedence(t *testing.T) {
	_, parent := fixture(t)
	home := filepath.Join(parent, "home")
	cwd := filepath.Join(home, "work")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	write(t, parent, ".claude/commands/which.md", "PARENT_COMMAND")
	write(t, home, ".claude/commands/which.md", "HOME_COMMAND")
	write(t, parent, ".claude/rules/order.md", "PARENT_RULE")
	write(t, home, ".claude/rules/order.md", "HOME_RULE")
	write(t, parent, ".claude/settings.json", `{"mcpServers":{"which":{"command":"parent"}}}`)
	write(t, home, ".claude/settings.json", `{"mcpServers":{"which":{"command":"home"}}}`)

	c := load(t, cwd)
	if got := c.Expand("/which"); !strings.Contains(got, "HOME_COMMAND") || strings.Contains(got, "PARENT_COMMAND") {
		t.Errorf("home command must override parent: %s", got)
	}
	if got := c.Servers["which"].Command; got != "home" {
		t.Errorf("home server must override parent: %s", got)
	}
	if parentAt, homeAt := strings.Index(c.Prompt, "PARENT_RULE"), strings.Index(c.Prompt, "HOME_RULE"); parentAt < 0 || homeAt <= parentAt {
		t.Errorf("home rule must follow parent rule: %s", c.Prompt)
	}
	if strings.Count(c.Prompt, "HOME_RULE") != 1 {
		t.Error("home rule must be loaded exactly once")
	}
}

func TestEnabledPluginsIsRecognizedSetting(t *testing.T) {
	_, root := fixture(t)
	write(t, root, ".claude/plugins/enabled/.claude-plugin/plugin.json", `{"name":"enabled"}`)
	write(t, root, ".claude/plugins/enabled/commands/check.md", "ENABLED_COMMAND")
	write(t, root, ".claude/plugins/disabled/.claude-plugin/plugin.json", `{"name":"disabled"}`)
	write(t, root, ".claude/plugins/disabled/commands/check.md", "DISABLED_COMMAND")
	write(t, root, ".claude/settings.json", `{"enabledPlugins":{"enabled":true,"disabled":false}}`)
	c := load(t, root)
	if !c.HasCommand("/enabled:check") || c.HasCommand("/disabled:check") {
		t.Error("enabledPlugins flags were not applied")
	}
	for _, warning := range c.Warnings {
		if strings.Contains(warning, "unsupported setting enabledPlugins") {
			t.Errorf("supported setting warned as unsupported: %s", warning)
		}
	}
}

func TestLazyCapabilityCatalogAndSlash(t *testing.T) {
	_, root := fixture(t)
	write(t, root, ".claude/skills/check/SKILL.md", "---\nname: check\ndescription: Check the release\n---\nSKILL_BODY $ARGUMENTS")
	reference := write(t, root, ".claude/skills/check/references/guide.md", "REFERENCE_BODY")
	script := write(t, root, ".claude/skills/check/scripts/run.sh", "#!/bin/sh\nSCRIPT_BODY")
	write(t, root, ".claude/commands/nested/run.md", "---\ndescription: Run a check\n---\nCOMMAND_BODY $1")
	write(t, root, ".claude/CLAUDE.md", "CORE_BODY")
	write(t, root, ".claude/rules/global.md", "RULE_BODY")
	c := load(t, root)
	for _, body := range []string{"SKILL_BODY", "REFERENCE_BODY", "SCRIPT_BODY", "COMMAND_BODY"} {
		if strings.Contains(c.Prompt, body) {
			t.Errorf("startup leaked %s", body)
		}
	}
	for _, metadata := range []string{"Check the release", "Run a check", "CORE_BODY", "RULE_BODY", "project_capability"} {
		if !strings.Contains(c.Prompt, metadata) {
			t.Errorf("missing %s", metadata)
		}
	}
	if c.Commands["check"] != "" || !c.HasCommand("/check hello") || !strings.Contains(c.Expand("/check hello"), "SKILL_BODY hello") {
		t.Fatal("slash expansion not lazy", c.Commands)
	}
	for _, tc := range []struct{ input, want string }{
		{`{"action":"search","query":"release"}`, "Check the release"},
		{`{"action":"load","name":"check"}`, "SKILL_BODY"},
		{`{"action":"read","path":` + quoted(reference) + `}`, "REFERENCE_BODY"},
		{`{"action":"read","path":` + quoted(script) + `}`, "SCRIPT_BODY"},
	} {
		got, err := capability(t, root, tc.input)
		if err != nil || !strings.Contains(got, tc.want) {
			t.Fatalf("%s => %s %v", tc.input, got, err)
		}
	}
}

func TestLazyLoadDirectoryMetadataAndAncestorSwap(t *testing.T) {
	_, root := fixture(t)
	write(t, root, ".agents/skills/check/SKILL.md", "Read ${CLAUDE_SKILL_DIR}/references/guide.md")
	path := write(t, root, ".agents/skills/check/references/guide.md", "guide")
	c := load(t, root)
	tool := c.Tools()[0]
	got, err := tool.Run(context.Background(), json.RawMessage(`{"action":"load","name":"check"}`))
	if err != nil || !strings.Contains(got, "Skill directory: "+filepath.Join(root, ".agents/skills/check")) {
		t.Fatalf("missing directory metadata: %s %v", got, err)
	}
	if err := os.Rename(root, root+"-saved"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(root); _ = os.Rename(root+"-saved", root) })
	if err := os.Symlink(root+"-saved", root); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"action":"read","path":`+quoted(path)+`}`)); err == nil {
		t.Fatal("swapped ancestor of catalog root accepted")
	}
}

func quoted(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestCatalogLimitKeepsMoreSpecificOverride(t *testing.T) {
	home, root := fixture(t)
	write(t, home, ".claude/commands/review.md", "HOME_COMMAND")
	for i := 0; i < 4095; i++ {
		write(t, home, fmt.Sprintf(".claude/references/%04d.txt", i), "reference")
	}
	write(t, root, ".agents/commands/review.md", "PROJECT_COMMAND")
	c := load(t, root)
	expanded := c.Expand("/review")
	if !strings.Contains(expanded, "PROJECT_COMMAND") || strings.Contains(expanded, "HOME_COMMAND") {
		t.Fatal("catalog limit lost project override", expanded)
	}
}

func TestCatalogBoundsAndConcurrentReads(t *testing.T) {
	_, root := fixture(t)
	write(t, root, ".agents/skills/check/SKILL.md", "---\ndescription: >\n  A folded\n  capability description\n---\nBODY")
	for i := 0; i < 4100; i++ {
		write(t, root, fmt.Sprintf(".agents/skills/check/references/%04d.txt", i), "reference")
	}
	c := load(t, root)
	tool := c.Tools()[0]
	search := func(input string) struct {
		Capabilities []struct{ Name, Description string }
		Truncated    bool
	} {
		t.Helper()
		got, err := tool.Run(context.Background(), json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Capabilities []struct{ Name, Description string }
			Truncated    bool
		}
		if err := json.Unmarshal([]byte(got), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := search(`{"action":"search"}`)
	if len(result.Capabilities) != 50 || !result.Truncated {
		t.Fatal("unbounded search", len(result.Capabilities), result.Truncated)
	}
	if got := search(`{"action":"search","query":"folded"}`); len(got.Capabilities) != 1 || got.Capabilities[0].Description != "A folded capability description" {
		t.Fatal(got)
	}
	if !strings.Contains(strings.Join(c.Warnings, "\n"), "4096") {
		t.Fatal("missing catalog limit warning")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"action":"read","path":`+quoted(filepath.Join(root, ".agents/skills/check/references/4099.txt"))+`}`)); err == nil {
		t.Fatal("overflow entry indexed")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if _, err := tool.Run(context.Background(), json.RawMessage(`{"action":"load","name":"check"}`)); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}

func TestScopedRulesWithIndentedOrMalformedMetadata(t *testing.T) {
	for _, header := range []string{"---\n  paths: [src/**]\n---\n", "---\n\"paths\": [src/**]\n---\n", "---\npaths: [src/**]\n"} {
		t.Run(header, func(t *testing.T) {
			_, root := fixture(t)
			write(t, root, ".agents/rules/scoped.md", header+"NEVER_GLOBAL")
			c := load(t, root)
			if strings.Contains(c.Prompt, "NEVER_GLOBAL") {
				t.Fatal("scoped or malformed rule applied globally")
			}
			if len(c.Warnings) == 0 {
				t.Fatal("no rule warning")
			}
		})
	}
}

func TestOnDemandSizeAndEncodingLimits(t *testing.T) {
	_, root := fixture(t)
	path := write(t, root, ".agents/skills/check/SKILL.md", "body")
	tool := load(t, root).Tools()[0]
	for _, body := range []string{strings.Repeat("x", 512*1024+1), "invalid\xff", "nul\x00"} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := tool.Run(context.Background(), json.RawMessage(`{"action":"load","name":"check"}`)); err == nil {
			t.Fatal("unsafe lazy body accepted")
		}
	}
}

func TestAgentsAliasAndScopedRules(t *testing.T) {
	_, root := fixture(t)
	write(t, root, ".claude/commands/which.md", "claude")
	write(t, root, ".agent/commands/which.md", "singular")
	write(t, root, ".agents/commands/which.md", "plural")
	write(t, root, ".agents/rules/scoped.md", "---\npaths:\n - src/**\n---\nSCOPED_RULE")
	write(t, root, ".agents/rules/global.md", "GLOBAL_RULE")
	c := load(t, root)
	if !strings.Contains(c.Expand("/which"), "plural") || strings.Contains(c.Prompt, "SCOPED_RULE") || !strings.Contains(c.Prompt, "GLOBAL_RULE") {
		t.Fatal(c.Prompt, c.Expand("/which"))
	}
	if !strings.Contains(strings.Join(c.Warnings, "\n"), "scoped rule") {
		t.Fatal(c.Warnings)
	}
}

func TestOnDemandSafetyRechecked(t *testing.T) {
	_, root := fixture(t)
	path := write(t, root, ".agents/skills/check/references/guide.md", "safe")
	write(t, root, ".agents/skills/check/SKILL.md", "skill")
	write(t, root, ".agents/skills/check/references/password.md", "PRIVATE")
	binary := write(t, root, ".agents/skills/check/assets/logo.png", "\x00binary")
	c := load(t, root)
	tool := c.Tools()[0]
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := write(t, t.TempDir(), "outside.md", "OUTSIDE")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, outside, binary, filepath.Join(root, ".agents/skills/check/references/password.md")} {
		got, err := tool.Run(context.Background(), json.RawMessage(`{"action":"read","path":`+quoted(p)+`}`))
		if err == nil {
			t.Fatalf("unsafe read %s => %s", p, got)
		}
	}
	// Replacing an ancestor directory with a symlink must not expose its target.
	parent := filepath.Dir(path)
	if err := os.Rename(parent, parent+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), parent); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"action":"read","path":`+quoted(path)+`}`)); err == nil {
		t.Fatal("symlink parent accepted")
	}
}
