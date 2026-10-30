package test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"kh/internal/config"
	"kh/internal/tools"
)

func TestBashCrossDirectoryWithoutGrants(t *testing.T) {
	t.Chdir(t.TempDir())
	outside := t.TempDir()
	path := filepath.Join(outside, "new", "result")
	cfg := config.Defaults
	cfg.Auto = false
	calls := 0
	tool := tools.Bash(cfg, fakeApprover(func(_ context.Context, _ string, allowed bool) (bool, error) {
		calls++
		if allowed {
			t.Error("write command automatically allowed")
		}
		return true, nil
	}))
	input, _ := json.Marshal(map[string]string{"command": "mkdir -p " + strconv.Quote(filepath.Dir(path)) + " && printf outside > " + strconv.Quote(path)})
	if out, err := tool.Run(context.Background(), input); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if calls != 1 {
		t.Fatalf("command approvals=%d", calls)
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "outside" {
		t.Fatalf("cross-directory write: %q %v", body, err)
	}
	input, _ = json.Marshal(map[string]string{"command": "cat " + strconv.Quote(path)})
	if out, err := tools.Bash(cfg, nil).Run(context.Background(), input); err != nil || out != "outside" {
		t.Fatalf("cross-directory read: %q %v", out, err)
	}
	if _, ok := tool.Params["write_dirs"]; ok {
		t.Fatal("obsolete folder grant parameter")
	}
}

func TestBashDynamicCommandApproval(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Defaults
	cfg.Auto, cfg.Safe = true, nil
	policy := tools.NewApprovalPolicy(false)
	var defaults []bool
	tool := tools.BashWithPolicy(cfg, fakeApprover(func(_ context.Context, _ string, allowed bool) (bool, error) {
		defaults = append(defaults, allowed)
		return allowed, nil
	}), policy)
	for _, enabled := range []bool{false, true, false} {
		policy.SetAuto(enabled)
		_, err := tool.Run(context.Background(), []byte(`{"command":"touch result"}`))
		if (err == nil) != enabled {
			t.Fatalf("auto=%v err=%v", enabled, err)
		}
	}
	if len(defaults) != 3 || defaults[0] || !defaults[1] || defaults[2] {
		t.Fatalf("live defaults: %v", defaults)
	}
}
