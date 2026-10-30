package test

import (
	"context"
	"encoding/json"
	"os"
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

func TestVisualCreatesOutsideProjectWithoutGrants(t *testing.T) {
	t.Chdir(t.TempDir())
	m := visualManager(t)
	tool := tools.Visual(m, config.Config{}, nil, nil)
	out, err := tool.Run(context.Background(), []byte(`{"action":"create","name":"demo"}`))
	if err != nil {
		t.Fatal(err)
	}
	var server visual.Server
	if err := json.Unmarshal([]byte(out), &server); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(server.Notebook); err != nil {
		t.Fatal(err)
	}
	out, err = tool.Run(context.Background(), []byte(`{"action":"list"}`))
	if err != nil || !strings.Contains(out, m.Root()) || !strings.Contains(out, m.Cache()) {
		t.Fatalf("list layout: %s %v", out, err)
	}
	if !strings.Contains(tool.Description, m.Root()) || !strings.Contains(tool.Description, m.Cache()) {
		t.Fatal("assistant cannot discover layout")
	}
}

func TestVisualDynamicCommandApproval(t *testing.T) {
	m := visualManager(t)
	policy := tools.NewApprovalPolicy(false)
	var defaults []bool
	tool := tools.Visual(m, config.Config{Auto: true}, fakeApprover(func(_ context.Context, command string, allowed bool) (bool, error) {
		defaults = append(defaults, allowed)
		if !strings.Contains(command, "demo") {
			t.Error(command)
		}
		return allowed, nil
	}), policy)
	if _, err := tool.Run(context.Background(), []byte(`{"action":"create","name":"demo"}`)); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true, false} {
		policy.SetAuto(enabled)
		_, err := tool.Run(context.Background(), []byte(`{"action":"start","name":"demo"}`))
		want := "user said no"
		if enabled {
			want = "install uv"
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("auto=%v err=%v", enabled, err)
		}
	}
	if len(defaults) != 3 || defaults[0] || !defaults[1] || defaults[2] {
		t.Fatalf("live defaults: %v", defaults)
	}
	if len(m.List()) != 0 {
		t.Fatal("denied or failed start spawned server")
	}
}

func TestVisualConfigAutoWithoutPolicy(t *testing.T) {
	for _, auto := range []bool{false, true} {
		m := visualManager(t)
		tool := tools.Visual(m, config.Config{Auto: auto}, nil, nil)
		if _, err := tool.Run(context.Background(), []byte(`{"action":"create","name":"demo"}`)); err != nil {
			t.Fatal(err)
		}
		_, err := tool.Run(context.Background(), []byte(`{"action":"start","name":"demo"}`))
		want := "user said no"
		if auto {
			want = "install uv"
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("auto=%v err=%v", auto, err)
		}
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
}
