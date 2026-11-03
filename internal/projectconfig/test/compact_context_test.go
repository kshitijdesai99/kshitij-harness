package projectconfig_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"kh/internal/projectconfig"
)

func TestCompactStartupRetainsGuidanceNotCatalogOrDecisions(t *testing.T) {
	_, root := fixture(t)
	write(t, root, "CLAUDE.md", "PLAIN_CLAUDE_BINDING")
	write(t, root, "AGENTS.md", "PLAIN_AGENTS_BINDING")
	write(t, root, ".claude/CLAUDE.md", "DOT_CLAUDE_BINDING")
	write(t, root, ".agents/AGENTS.md", "DOT_AGENTS_BINDING")
	write(t, root, ".agents/rules/global.md", "UNSCOPED_BINDING")
	decisions := write(t, root, ".agents/decisions.md", "HISTORICAL_DECISION_PROSE")
	reference := write(t, root, ".agents/README.md", "ROOT_REFERENCE_PROSE")
	for i := 0; i < 100; i++ {
		write(t, root, fmt.Sprintf(".agents/skills/case-%d/SKILL.md", i), "---\ndescription: CATALOG_DESCRIPTION\n---\nLAZY_SKILL_BODY")
	}
	c := load(t, root)
	for _, binding := range []string{"PLAIN_CLAUDE_BINDING", "PLAIN_AGENTS_BINDING", "DOT_CLAUDE_BINDING", "DOT_AGENTS_BINDING", "UNSCOPED_BINDING"} {
		if !strings.Contains(c.Prompt, binding) {
			t.Fatalf("lost binding guidance %s", binding)
		}
	}
	for _, lazy := range []string{"CATALOG_DESCRIPTION", "LAZY_SKILL_BODY", "HISTORICAL_DECISION_PROSE", "ROOT_REFERENCE_PROSE", "/case-"} {
		if strings.Contains(c.Prompt, lazy) {
			t.Fatalf("eager reference %s", lazy)
		}
	}
	if len(c.Prompt) > 2000 {
		t.Fatalf("catalog inflated prompt: %d", len(c.Prompt))
	}
	for path, body := range map[string]string{decisions: "HISTORICAL_DECISION_PROSE", reference: "ROOT_REFERENCE_PROSE"} {
		got, err := runProjectTool(t, c, "project_capability", map[string]any{"action": "search", "query": filepath.Base(path)})
		if err != nil || !strings.Contains(got, path) {
			t.Fatalf("missing reference: %s %v", got, err)
		}
		got, err = runProjectTool(t, c, "project_capability", map[string]any{"action": "read", "path": path})
		if err != nil || !strings.Contains(got, body) {
			t.Fatalf("unreadable reference: %s %v", got, err)
		}
	}
}

func TestWorkspaceDecisionPagingAndCompactDefault(t *testing.T) {
	_, root := fixture(t)
	body := strings.Repeat("é decision text\n", 3000)
	write(t, root, ".agents/decisions.md", body)
	write(t, root, "AGENTS.md", "CURRENT_BINDING")
	c := load(t, root)
	got, err := runProjectTool(t, c, "workspace", map[string]any{"action": "inspect", "directory": root})
	if err != nil {
		t.Fatal(err)
	}
	var overview map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &overview); err != nil {
		t.Fatal(err)
	}
	if overview["catalog"] != nil || overview["decisions"] != nil || !strings.Contains(got, "CURRENT_BINDING") || strings.Contains(got, "decision text") {
		t.Fatalf("not compact: %s", got)
	}
	var restored strings.Builder
	offset := 0
	for {
		got, err = runProjectTool(t, c, "workspace", map[string]any{"action": "inspect", "directory": root, "detail": "decisions", "offset": offset})
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Decisions  string
			NextOffset int `json:"next_offset"`
			Truncated  bool
		}
		if err := json.Unmarshal([]byte(got), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Decisions) > 8192 || strings.Contains(got, "CURRENT_BINDING") {
			t.Fatalf("unbounded/duplicated detail: %d", len(got))
		}
		restored.WriteString(page.Decisions)
		if !page.Truncated {
			break
		}
		if page.NextOffset <= offset {
			t.Fatal("pagination did not advance")
		}
		offset = page.NextOffset
	}
	if restored.String() != body {
		t.Fatal("paged decision text changed")
	}
	for _, args := range []map[string]any{
		{"detail": "invalid"}, {"detail": "decisions", "offset": -1}, {"detail": "decisions", "offset": 1}, {"detail": "decisions", "limit": 16385}, {"detail": "decisions", "limit": 1},
	} {
		args["action"] = "inspect"
		args["directory"] = root
		if _, err := runProjectTool(t, c, "workspace", args); err == nil {
			t.Fatalf("accepted invalid paging %v", args)
		}
	}
}

func TestOversizedBindingFileIsExplicitError(t *testing.T) {
	_, root := fixture(t)
	write(t, root, "AGENTS.md", strings.Repeat("binding", 80000))
	if _, err := projectconfig.Load(root); err == nil || !strings.Contains(err.Error(), "binding project guidance cannot be loaded") {
		t.Fatalf("binding file silently omitted: %v", err)
	}
}
