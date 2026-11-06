package test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kh/internal/config"
	"kh/internal/tools"
)

func TestEditHookAfterPreflightBeforeMutation(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "file")
	writeFixture(t, path, "before")
	calls := 0
	denied := errors.New("denied before external hook")
	tool := tools.WithEditHook(tools.EditWithOutput(nil), func(ctx context.Context, ops []tools.EditOperation, batch bool) error {
		calls++
		requireText(t, path, "before")
		if batch || len(ops) != 1 || ops[0].Path != path || ops[0].Old != "before" || ops[0].New != "after" {
			t.Fatalf("operations=%+v batch=%v", ops, batch)
		}
		return denied
	})
	input, _ := json.Marshal(map[string]string{"path": path, "old": "absent", "new": "after"})
	if _, err := tool.Run(context.Background(), input); err == nil || calls != 0 {
		t.Fatalf("invalid preflight: calls=%d err=%v", calls, err)
	}
	input, _ = json.Marshal(map[string]string{"path": path, "old": "before", "new": "after"})
	if _, err := tool.Run(context.Background(), input); !errors.Is(err, denied) || calls != 1 {
		t.Fatalf("denial: calls=%d err=%v", calls, err)
	}
	requireText(t, path, "before")
}

func TestEditHookRegistryBatchOnceAndCancel(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "created")
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	tool := tools.WithEditHook(tools.EditWithOutput(nil), func(ctx context.Context, ops []tools.EditOperation, batch bool) error {
		calls++
		if !batch || len(ops) != 2 || ops[0].Path != path || ops[0].Old != "" || ops[1].Old != "one" {
			t.Fatalf("operations=%+v batch=%v", ops, batch)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("premature creation: %v", err)
		}
		cancel()
		return nil
	})
	registry, err := tools.NewRegistry([]tools.Tool{tool})
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"name": "edit", "arguments": map[string]any{"edits": []map[string]string{{"path": path, "old": "", "new": "one"}, {"path": path, "old": "one", "new": "two"}}}})
	// Registry's invocation schema uses name plus arguments, and retains native
	// cancellation and approval wrappers rather than adding a second hook.
	var invoke tools.Tool
	for _, candidate := range registry.Tools() {
		if candidate.Name == tools.InvokeToolName {
			invoke = candidate
		}
	}
	_, err = invoke.RunResult(ctx, input)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("canceled creation: %v", err)
	}
}

func TestBashCompatibilityEnvironmentKeepsApproval(t *testing.T) {
	t.Setenv("CLAUDE_SESSION_ID", "ambient")
	cfg := config.Defaults
	cfg.Auto, cfg.Safe = false, nil
	calls := 0
	allow := false
	tool := tools.Bash(cfg, fakeApprover(func(ctx context.Context, command string, safe bool) (bool, error) {
		calls++
		if safe {
			t.Error("compatibility environment must not authorize commands")
		}
		return allow, nil
	}))
	values := map[string]string{"CLAUDE_SESSION_ID": "fixture", "CLAUDE_PROJECT_DIR": "fixture dir", "CLAUDE_CONFIG_DIR": "fixture config", "CLAUDE_PLUGIN_ROOT": "fixture plugin", "CLAUDE_CODE_SESSION_ID": "fixture"}
	ctx := tools.WithBashEnvironment(context.Background(), values)
	values["CLAUDE_SESSION_ID"] = "mutated"
	input := json.RawMessage(`{"command":"printf '%s|%s|%s|%s|%s' \"$CLAUDE_SESSION_ID\" \"$CLAUDE_PROJECT_DIR\" \"$CLAUDE_CONFIG_DIR\" \"$CLAUDE_PLUGIN_ROOT\" \"$CLAUDE_CODE_SESSION_ID\""}`)
	if _, err := tool.Run(ctx, input); err == nil {
		t.Fatal("denied command ran")
	}
	allow = true
	out, err := tool.Run(ctx, input)
	if err != nil || strings.TrimSpace(out) != "fixture|fixture dir|fixture config|fixture plugin|fixture" || calls != 2 {
		t.Fatalf("out=%q err=%v calls=%d", out, err, calls)
	}
	if os.Getenv("CLAUDE_SESSION_ID") != "ambient" {
		t.Fatal("global environment changed")
	}
}
