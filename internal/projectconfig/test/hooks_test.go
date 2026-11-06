package projectconfig_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kh/internal/projectconfig"
)

func TestPluginHooksExplicitEnabledAndInert(t *testing.T) {
	_, root := fixture(t)
	dir := plugin(t, root, ".agents/plugins/demo", `{"name":"demo"}`)
	marker := filepath.Join(root, "executed")
	write(t, dir, "hooks/hooks.json", `{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"touch `+marker+`"}]}],"UserPromptSubmit":[{"hooks":[{"type":"command","command":"node","args":["${CLAUDE_PLUGIN_ROOT}/run.js"],"env":{"PLUGIN_MODE":"test"},"timeout":10}]}],"Stop":[{"hooks":[{"type":"command","argv":["node","stop.js"]}]}],"PreToolUse":[{"matcher":"bash","hooks":[{"type":"command","command":"true"}]}],"SessionEnd":[{"hooks":[{"type":"command","command":"true"}]}]}}`)
	if got := load(t, root); len(got.Hooks) != 0 {
		t.Fatalf("implicitly enabled hooks: %#v", got.Hooks)
	}
	write(t, root, ".agents/settings.json", `{"enabledPlugins":{"demo":true}}`)
	c := load(t, root)
	if len(c.Hooks) != 5 {
		t.Fatalf("hooks=%#v warnings=%v", c.Hooks, c.Warnings)
	}
	var node, shell *projectconfig.Hook
	for i := range c.Hooks {
		h := &c.Hooks[i]
		if h.TrustID == "" || h.PluginRoot != dir || h.Plugin != "demo" {
			t.Fatalf("identity: %#v", h)
		}
		if h.Event == "UserPromptSubmit" {
			node = h
		}
		if h.Event == "SessionStart" {
			shell = h
		}
		if h.TrustID != c.Hooks[0].TrustID {
			t.Fatal("plugin definitions must share fingerprint")
		}
	}
	if node == nil || node.Command != "node" || !node.RequiresNode || node.Timeout != 10*time.Second || node.Env["PLUGIN_MODE"] != "test" || node.Args[0] != "${CLAUDE_PLUGIN_ROOT}/run.js" {
		t.Fatalf("node metadata: %#v", node)
	}
	if shell == nil || shell.Command != "/bin/sh" || shell.Args[0] != "-c" || shell.Timeout != 5*time.Second || shell.Matcher != "startup" {
		t.Fatalf("shell metadata: %#v", shell)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("hook ran during discovery: %v", err)
	}
	if strings.Contains(c.Prompt, "PLUGIN_MODE") || strings.Contains(c.Prompt, marker) {
		t.Fatal("hook source leaked into prompt")
	}
	write(t, root, ".agents/settings.local.json", `{"enabledPlugins":{"demo":false}}`)
	if got := load(t, root); len(got.Hooks) != 0 {
		t.Fatal("disabled plugin hooks loaded")
	}
}

func TestPluginHooksManifestSafetyAndUnsupported(t *testing.T) {
	for _, tc := range []struct {
		name, manifest, path, body string
		count                      int
		warning                    string
	}{
		{"inline", `{"name":"demo","hooks":{"hooks":{"Stop":[{"hooks":[{"type":"command","argv":["true"]}]}]}}}`, "", "", 1, ""},
		{"node-shell", `{"name":"demo","hooks":{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"node stop.js"}]}]}}}`, "", "", 1, ""},
		{"custom", `{"name":"demo","hooks":"./extra/hooks.json"}`, "extra/hooks.json", `{"hooks":{"SessionEnd":[{"hooks":[{"type":"command","command":"true","args":[]}]}]}}`, 1, ""},
		{"traversal", `{"name":"demo","hooks":"./../outside.json"}`, "", "", 0, "unsafe plugin hooks path"},
		{"unsupported", `{"name":"demo"}`, "hooks/hooks.json", `{"hooks":{"Unknown":[{"hooks":[]}],"Stop":[{"hooks":[{"type":"prompt","prompt":"unsupported"},{"type":"command","command":"true","timeout":31},{"type":"command","argv":["true"],"args":[]},{"type":"command","command":"true","async":true}]}]}}`, 0, "unsupported plugin hook"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, root := fixture(t)
			dir := plugin(t, root, ".agents/plugins/demo", tc.manifest)
			write(t, root, ".agents/settings.json", `{"enabledPlugins":{"demo@local":true}}`)
			if tc.path != "" {
				write(t, dir, tc.path, tc.body)
			}
			c := load(t, root)
			if len(c.Hooks) != tc.count {
				t.Fatalf("hooks=%#v warnings=%v", c.Hooks, c.Warnings)
			}
			if tc.name == "node-shell" && !c.Hooks[0].RequiresNode {
				t.Fatal("leading node shell command not detected")
			}
			if tc.warning != "" && !strings.Contains(strings.Join(c.Warnings, "\n"), tc.warning) {
				t.Fatalf("missing warning %s: %v", tc.warning, c.Warnings)
			}
		})
	}
}

func TestPluginHooksSymlinkRejected(t *testing.T) {
	_, root := fixture(t)
	dir := plugin(t, root, ".agents/plugins/demo", `{"name":"demo"}`)
	write(t, root, ".agents/settings.json", `{"enabledPlugins":{"demo":true}}`)
	outside := t.TempDir()
	write(t, outside, "hooks.json", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"true"}]}]}}`)
	if err := os.Symlink(outside, filepath.Join(dir, "hooks")); err != nil {
		t.Fatal(err)
	}
	c := load(t, root)
	if len(c.Hooks) != 0 || !strings.Contains(strings.Join(c.Warnings, "\n"), "unsafe plugin hooks") {
		t.Fatalf("hooks=%#v warnings=%v", c.Hooks, c.Warnings)
	}
}

func TestPluginHookFingerprintCacheAndTrust(t *testing.T) {
	_, root := fixture(t)
	dir := plugin(t, root, ".agents/plugins/demo", `{"name":"demo"}`)
	write(t, root, ".agents/settings.json", `{"enabledPlugins":{"demo":true}}`)
	hookPath := write(t, dir, "hooks/hooks.json", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"node","args":["one.js"],"env":{"A":"first"}}]}]}}`)
	cache := projectconfig.NewFoundationCache()
	first, err := cache.Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	id := first.Config.Hooks[0].TrustID
	first.Config.Hooks[0].Args[0] = "mutated"
	first.Config.Hooks[0].Env["A"] = "mutated"
	second, err := cache.Resolve(context.Background(), root)
	if err != nil || !second.Hit {
		t.Fatalf("cache=%#v err=%v", second, err)
	}
	if second.Config.Hooks[0].Args[0] != "one.js" || second.Config.Hooks[0].Env["A"] != "first" {
		t.Fatal("cached hooks alias caller")
	}
	store := filepath.Join(t.TempDir(), "state", "plugin-trust.json")
	grants, err := projectconfig.LoadHookTrust(store)
	if err != nil || len(grants) != 0 {
		t.Fatalf("missing trust: %v %v", grants, err)
	}
	if err := projectconfig.SetHookTrust(store, id, true); err != nil {
		t.Fatal(err)
	}
	grants, err = projectconfig.LoadHookTrust(store)
	if err != nil || !grants[id] {
		t.Fatalf("persisted trust: %v %v", grants, err)
	}
	info, err := os.Stat(store)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("trust permissions: %v %v", info, err)
	}
	if err := os.WriteFile(hookPath, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"node","args":["two.js"],"env":{"A":"first"}}]}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	third, err := cache.Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if third.Hit || third.Config.Hooks[0].TrustID == id || grants[third.Config.Hooks[0].TrustID] {
		t.Fatal("changed definitions retained trust")
	}
	if err := projectconfig.SetHookTrust(store, id, false); err != nil {
		t.Fatal(err)
	}
	grants, err = projectconfig.LoadHookTrust(store)
	if err != nil || len(grants) != 0 {
		t.Fatalf("revoke: %v %v", grants, err)
	}
	link := filepath.Join(filepath.Dir(store), "trust-link.json")
	if err := os.Symlink(store, link); err != nil {
		t.Fatal(err)
	}
	if _, err := projectconfig.LoadHookTrust(link); err == nil {
		t.Fatal("symlink trust read accepted")
	}
	if err := projectconfig.SetHookTrust(link, id, true); err == nil {
		t.Fatal("symlink trust write accepted")
	}
	if err := projectconfig.SetHookTrust(store, "demo", true); err == nil {
		t.Fatal("invalid identity accepted")
	}
	if err := os.WriteFile(store, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := projectconfig.LoadHookTrust(store); err == nil {
		t.Fatal("malformed trust accepted")
	}
}
