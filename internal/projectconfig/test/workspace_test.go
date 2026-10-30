package projectconfig_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kh/internal/projectconfig"
)

func runProjectTool(t *testing.T, c *projectconfig.Config, name string, args map[string]any) (string, error) {
	t.Helper()
	input, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range c.Tools() {
		if tool.Name == name {
			return tool.Run(context.Background(), input)
		}
	}
	t.Fatalf("missing tool %s", name)
	return "", nil
}

func TestTargetCapabilityDoesNotMutateStartup(t *testing.T) {
	_, startup := fixture(t)
	target, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, startup, ".agents/commands/check.md", "STARTUP_BODY")
	write(t, target, ".agents/commands/check.md", "TARGET_BODY")
	write(t, target, "nested/.agents/commands/check.md", "NESTED_BODY")
	c := load(t, startup)
	original := c.Prompt
	for _, action := range []string{"search", "load", "read"} {
		args := map[string]any{"action": action, "directory": filepath.Join(target, "nested"), "name": "check", "path": filepath.Join(target, "nested/.agents/commands/check.md")}
		got, err := runProjectTool(t, c, "project_capability", args)
		if err != nil {
			t.Fatal(err)
		}
		want := "NESTED_BODY"
		if action == "search" {
			want = "nested/.agents/commands/check.md"
		}
		if !strings.Contains(got, want) {
			t.Fatal(got)
		}
	}
	if c.Prompt != original || !strings.Contains(c.Expand("/check"), "STARTUP_BODY") {
		t.Fatal("startup configuration mutated")
	}
	if _, err := runProjectTool(t, c, "project_capability", map[string]any{"action": "search", "directory": filepath.Join(target, "missing")}); err == nil {
		t.Fatal("invalid target accepted")
	}
}

func TestWorkspaceInspectPrepareAndDecision(t *testing.T) {
	_, root := fixture(t)
	write(t, root, ".git/HEAD", "ref: refs/heads/main")
	write(t, root, "AGENTS.md", "PLAIN_ROOT_GUIDANCE")
	write(t, root, "src/deep/CLAUDE.md", "PLAIN_NESTED_GUIDANCE")
	write(t, root, "other/AGENTS.md", "UNRELATED_PLAIN_GUIDANCE")
	write(t, root, ".claude/rules/root.md", "ROOT_GUIDANCE")
	write(t, root, "src/deep/.claude/rules/local.md", "LOCAL_GUIDANCE")
	write(t, root, "src/deep/.claude/skills/check/SKILL.md", "---\ndescription: Target skill\n---\nLAZY_BODY")
	write(t, root, "other/.agents/rules/other.md", "UNRELATED_GUIDANCE")
	marker := filepath.Join(root, "mcp-was-executed")
	write(t, root, ".mcp.json", `{"mcpServers":{"no-run":{"command":"sh","args":["-c","touch `+marker+`"],"env":{"KEY":"PRIVATE_SECRET"}}}}`)
	c := load(t, root)
	args := map[string]any{"action": "inspect", "directory": filepath.Join(root, "src/deep")}
	got, err := runProjectTool(t, c, "workspace", args)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{root, "ROOT_GUIDANCE", "LOCAL_GUIDANCE", "Target skill", "PLAIN_ROOT_GUIDANCE", "PLAIN_NESTED_GUIDANCE"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s: %s", want, got)
		}
	}
	for _, unwanted := range []string{"UNRELATED_GUIDANCE", "LAZY_BODY", "PRIVATE_SECRET", "UNRELATED_PLAIN_GUIDANCE"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("leaked %s", unwanted)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".agents")); !os.IsNotExist(err) {
		t.Fatal("inspect wrote .agents")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("inspection executed MCP")
	}
	args["action"] = "prepare"
	if _, err := runProjectTool(t, c, "workspace", args); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(root, ".agents")); err != nil || !info.IsDir() {
		t.Fatalf("prepare: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "src/deep/.agents")); !os.IsNotExist(err) {
		t.Fatal("created nested .agents")
	}
	args["action"] = "record"
	args["title"] = "Target-aware loading"
	args["rationale"] = "Keep startup isolated"
	args["affected"] = "internal/projectconfig"
	args["verification"] = "Tests pass; integration outstanding"
	args["status"] = "proposed"
	if _, err := runProjectTool(t, c, "workspace", args); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".agents/decisions.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{time.Now().Format("2006-01-02"), "Target-aware loading", "Keep startup isolated", "proposed", "internal/projectconfig", "integration outstanding"} {
		if !strings.Contains(string(before), want) {
			t.Fatalf("missing %s", want)
		}
	}
	args["status"] = "implemented"
	if _, err := runProjectTool(t, c, "workspace", args); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(after), string(before)) || !strings.Contains(string(after[len(before):]), "implemented") {
		t.Fatal("decision not appended")
	}
	args["action"] = "inspect"
	got, err = runProjectTool(t, c, "workspace", args)
	if err != nil || !strings.Contains(got, "Target-aware loading") {
		t.Fatalf("decision not inspected: %s %v", got, err)
	}
}

func TestWorkspaceExistingConventionAndNonGitRoot(t *testing.T) {
	_, root := fixture(t)
	write(t, root, "docs/architecture.md", "EXISTING_CONVENTION\n")
	if err := os.MkdirAll(filepath.Join(root, "nested/deep"), 0700); err != nil {
		t.Fatal(err)
	}
	c := load(t, root)
	args := map[string]any{"directory": filepath.Join(root, "nested/deep"), "root": root, "action": "prepare"}
	if _, err := runProjectTool(t, c, "workspace", args); err != nil {
		t.Fatal(err)
	}
	args["action"] = "record"
	args["decision_path"] = "docs/architecture.md"
	args["title"] = "Custom convention"
	args["rationale"] = "Reuse"
	args["affected"] = "docs"
	args["verification"] = "Pending"
	args["status"] = "proposed"
	if _, err := runProjectTool(t, c, "workspace", args); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "docs/architecture.md"))
	if !strings.HasPrefix(string(data), "EXISTING_CONVENTION\n") {
		t.Fatal("existing convention overwritten")
	}
	args["action"] = "inspect"
	got, err := runProjectTool(t, c, "workspace", args)
	if err != nil || !strings.Contains(got, "Custom convention") {
		t.Fatalf("custom decisions: %s %v", got, err)
	}
	delete(args, "root")
	delete(args, "decision_path")
	got, err = runProjectTool(t, c, "workspace", args)
	if err != nil || !strings.Contains(got, root) {
		t.Fatalf("existing workspace not detected: %s %v", got, err)
	}
}

func TestWorkspaceRejectsUnsafeWrites(t *testing.T) {
	_, root := fixture(t)
	outside := t.TempDir()
	write(t, root, ".git", "gitdir: /unused") // worktree marker is a file
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	c := load(t, root)
	base := map[string]any{"action": "record", "directory": root, "title": "Title", "rationale": "Why", "affected": "Files", "verification": "Pending", "status": "proposed"}
	// Recording never implicitly prepares a workspace.
	if _, err := runProjectTool(t, c, "workspace", base); err == nil {
		t.Fatal("record prepared workspace implicitly")
	}
	if err := os.Symlink(outside, filepath.Join(root, ".agents")); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"prepare", "record"} {
		base["action"] = action
		if _, err := runProjectTool(t, c, "workspace", base); err == nil {
			t.Fatalf("%s followed .agents symlink", action)
		}
	}
	if err := os.Remove(filepath.Join(root, ".agents")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".agents"), 0700); err != nil {
		t.Fatal(err)
	}
	base["action"] = "record"
	for _, path := range []string{"../escape.md", filepath.Join(outside, "escape.md"), ".git/HEAD"} {
		base["decision_path"] = path
		if _, err := runProjectTool(t, c, "workspace", base); err == nil {
			t.Fatalf("unsafe decision path %s accepted", path)
		}
	}
	delete(base, "decision_path")
	if err := os.Symlink(filepath.Join(outside, "decisions.md"), filepath.Join(root, ".agents/decisions.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := runProjectTool(t, c, "workspace", base); err == nil {
		t.Fatal("followed decision symlink")
	}
	base["action"] = "prepare"
	base["directory"] = filepath.Join(root, "nested")
	base["root"] = filepath.Join(root, "nested")
	if _, err := runProjectTool(t, c, "workspace", base); err == nil {
		t.Fatal("accepted subdirectory of git root")
	}
}

func TestWorkspaceConcurrentAppend(t *testing.T) {
	_, root := fixture(t)
	write(t, root, ".agents/decisions.md", "Existing decisions\n")
	c := load(t, root)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := runProjectTool(t, c, "workspace", map[string]any{"action": "record", "directory": root, "title": "Concurrent entry", "rationale": "Reason", "affected": "Files", "verification": "Pending", "status": "proposed"})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	data, err := os.ReadFile(filepath.Join(root, ".agents/decisions.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "Existing decisions\n") || strings.Count(string(data), "Concurrent entry") != 12 {
		t.Fatal("concurrent entries lost or overwritten")
	}
}

func TestWorkspaceGuidancePrecedence(t *testing.T) {
	_, root := fixture(t)
	write(t, root, "CLAUDE.md", "ROOT_PLAIN_CLAUDE")
	write(t, root, "AGENTS.md", "ROOT_PLAIN_AGENTS")
	write(t, root, ".agents/rules/root.md", "ROOT_DOT_RULE")
	write(t, root, "nested/CLAUDE.md", "NESTED_PLAIN_CLAUDE")
	write(t, root, "nested/AGENTS.md", "NESTED_PLAIN_AGENTS")
	write(t, root, "nested/.agents/rules/local.md", "NESTED_DOT_RULE")
	got, err := runProjectTool(t, load(t, root), "workspace", map[string]any{"action": "inspect", "directory": filepath.Join(root, "nested")})
	if err != nil {
		t.Fatal(err)
	}
	previous := -1
	for _, marker := range []string{"ROOT_PLAIN_CLAUDE", "ROOT_PLAIN_AGENTS", "ROOT_DOT_RULE", "NESTED_PLAIN_CLAUDE", "NESTED_PLAIN_AGENTS", "NESTED_DOT_RULE"} {
		position := strings.Index(got, marker)
		if position <= previous {
			t.Fatalf("guidance precedence wrong at %s: %s", marker, got)
		}
		previous = position
	}
}

func TestWorkspaceValidationAndBoundedDecisions(t *testing.T) {
	_, root := fixture(t)
	write(t, root, ".agents/decisions.md", strings.Repeat("x", 512*1024+1))
	c := load(t, root)
	args := map[string]any{"action": "inspect", "directory": root}
	got, err := runProjectTool(t, c, "workspace", args)
	if err != nil || !strings.Contains(got, "512 KiB") {
		t.Fatalf("unbounded decisions: %d %v", len(got), err)
	}
	args["action"] = "record"
	args["title"] = "x"
	args["rationale"] = "y"
	args["affected"] = "z"
	args["verification"] = "pending"
	args["status"] = "implemented"
	if _, err := runProjectTool(t, c, "workspace", args); err == nil {
		t.Fatal("appended to oversized decisions")
	}
	if err := os.WriteFile(filepath.Join(root, ".agents/decisions.md"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"title", "rationale", "affected", "verification", "status"} {
		original := args[field]
		args[field] = ""
		if _, err := runProjectTool(t, c, "workspace", args); err == nil {
			t.Fatalf("missing %s accepted", field)
		}
		args[field] = original
	}
	for _, tool := range c.Tools() {
		if tool.Name == "workspace" {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			input, _ := json.Marshal(args)
			if _, err := tool.Run(ctx, input); err == nil {
				t.Fatal("canceled action accepted")
			}
		}
	}
}
