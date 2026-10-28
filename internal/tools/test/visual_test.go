package test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kh/internal/config"
	"kh/internal/tools"
	"kh/internal/visual"
)

func visualManager(t *testing.T) *visual.Manager {
	t.Helper()
	m, err := visual.New(visual.Options{Home: t.TempDir(), UV: "kh-nonexistent-uv"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}
func TestVisualSharesNarrowArtifactGrantWithEditAndBash(t *testing.T) {
	t.Chdir(t.TempDir())
	m := visualManager(t)
	var grants []string
	access := newTestAccess(t, config.Config{Auto: false}, accessApprover(func(_ context.Context, path string) (bool, error) { grants = append(grants, path); return true, nil }))
	tool := tools.Visual(m, config.Config{}, nil, access)
	out, err := tool.Run(context.Background(), []byte(`{"action":"create","name":"demo"}`))
	if err != nil {
		t.Fatal(err)
	}
	var server visual.Server
	if err := json.Unmarshal([]byte(out), &server); err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 || grants[0] != m.Root() {
		t.Fatalf("broad or missing grant: %v", grants)
	}
	input, _ := json.Marshal(map[string]string{"path": server.Notebook, "old": "# Visual companion", "new": "# Updated"})
	if _, err := tools.EditWithAccess(nil, access).Run(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 {
		t.Fatal("edit did not share visual grant", grants)
	}
	dirs, err := access.SandboxDirectories()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		if dir == filepath.Dir(m.Root()) || dir == filepath.Dir(filepath.Dir(m.Root())) {
			t.Fatal("visual granted whole artifacts/kh directory", dirs)
		}
	}
	out, err = tool.Run(context.Background(), []byte(`{"action":"list"}`))
	if err != nil || !strings.Contains(out, m.Root()) || !strings.Contains(out, m.Cache()) {
		t.Fatalf("list layout missing: %s %v", out, err)
	}
	if !strings.Contains(tool.Description, m.Root()) || !strings.Contains(tool.Description, m.Cache()) {
		t.Fatal("assistant cannot discover layout")
	}
}
func TestVisualDenialsAndDynamicAuto(t *testing.T) {
	t.Chdir(t.TempDir())
	m := visualManager(t)
	deny := newTestAccess(t, config.Config{Auto: false}, accessApprover(func(context.Context, string) (bool, error) { return false, nil }))
	tool := tools.Visual(m, config.Config{}, nil, deny)
	if _, err := tool.Run(context.Background(), []byte(`{"action":"create","name":"denied"}`)); err == nil {
		t.Fatal("folder denial ignored")
	}
	if _, err := os.Stat(filepath.Join(m.Root(), "denied.py")); !os.IsNotExist(err) {
		t.Fatal("denial created notebook", err)
	}
	deny.SetAuto(true)
	if _, err := tool.Run(context.Background(), []byte(`{"action":"create","name":"demo"}`)); err != nil {
		t.Fatal(err)
	}
	if err := deny.Require(context.Background(), []string{m.Cache()}); err != nil {
		t.Fatal(err)
	}
	deny.SetAuto(false)
	approvals := 0
	tool = tools.Visual(m, config.Config{Auto: true}, fakeApprover(func(_ context.Context, command string, allowed bool) (bool, error) {
		approvals++
		if allowed {
			t.Error("captured config overrode active auto")
		}
		if !strings.Contains(command, "demo") {
			t.Error(command)
		}
		return false, nil
	}), deny)
	if _, err := tool.Run(context.Background(), []byte(`{"action":"start","name":"demo"}`)); err == nil || !strings.Contains(err.Error(), "user said no") {
		t.Fatalf("command denial ignored: %v", err)
	}
	if approvals != 1 {
		t.Fatalf("command approvals=%d", approvals)
	}
	if len(m.List()) != 0 {
		t.Fatal("denied start spawned server")
	}
	deny.SetAuto(true)
	tool = tools.Visual(m, config.Config{Auto: false}, nil, deny)
	if _, err := tool.Run(context.Background(), []byte(`{"action":"start","name":"demo"}`)); err == nil || !strings.Contains(err.Error(), "install uv") {
		t.Fatalf("live auto did not reach uv lookup: %v", err)
	}
}
func TestVisualRejectsInvalidActionsAndCancelledCalls(t *testing.T) {
	m := visualManager(t)
	tool := tools.Visual(m, config.Config{}, nil, nil)
	for _, input := range []string{`{"action":"exec","command":"touch nope"}`, `{"action":"create","name":"ok","port":1234}`, `{"action":"stop","name":"../other"}`} {
		if _, err := tool.Run(context.Background(), []byte(input)); err == nil {
			t.Fatal("accepted", input)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Run(ctx, []byte(`{"action":"list"}`)); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := tool.Run(context.Background(), []byte(`{"action":"create","name":"ok"}`)); err == nil {
		t.Fatal("nil access permitted write")
	}
}
