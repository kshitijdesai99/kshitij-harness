package projectconfig_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"kh/internal/projectconfig"
)

func resolveFoundation(t *testing.T, cache *projectconfig.FoundationCache, dir string) projectconfig.FoundationSnapshot {
	t.Helper()
	snapshot, err := cache.Resolve(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	uncached, err := projectconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Config, uncached) {
		t.Fatal("cached and uncached discovery differ")
	}
	if snapshot.Ref.SchemaVersion != projectconfig.FoundationSchemaVersion || snapshot.Ref.ID == "" {
		t.Fatalf("invalid ref: %+v", snapshot.Ref)
	}
	return snapshot
}

func TestFoundationContentMembershipAndPrecedence(t *testing.T) {
	home, root := fixture(t)
	write(t, home, ".agents/rules/home.md", "home rule")
	write(t, home, ".claude/commands/order.md", "home")
	write(t, root, ".agent/commands/order.md", "parent")
	write(t, root, "child/.agents/commands/order.md", "child")
	path := write(t, root, "AGENTS.md", "first guidance")
	dir := filepath.Join(root, "child")
	cache := projectconfig.NewFoundationCache()
	first := resolveFoundation(t, cache, dir)
	if first.Hit || !strings.Contains(first.Config.Expand("/order"), "child") {
		t.Fatal("bad cold precedence")
	}
	warm := resolveFoundation(t, cache, dir)
	if !warm.Hit || warm.Ref != first.Ref {
		t.Fatal("unchanged sources missed cache")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "AGENTS.md", "other guidance") // same byte count
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	changed := resolveFoundation(t, cache, dir)
	if changed.Hit || changed.Ref.ID == first.Ref.ID || !strings.Contains(changed.Config.Prompt, "other guidance") {
		t.Fatal("same-size timestamp-preserving edit was stale")
	}
	if !strings.Contains(first.Config.Prompt, "first guidance") {
		t.Fatal("old snapshot changed")
	}
	write(t, root, "child/.agents/rules/new.md", "new nested rule")
	added := resolveFoundation(t, cache, dir)
	if added.Hit || added.Ref.ID == changed.Ref.ID || !strings.Contains(added.Config.Prompt, "new nested rule") {
		t.Fatal("addition was stale")
	}
	if err := os.Remove(filepath.Join(root, "child/.agents/rules/new.md")); err != nil {
		t.Fatal(err)
	}
	deleted := resolveFoundation(t, cache, dir)
	if deleted.Hit || strings.Contains(deleted.Config.Prompt, "new nested rule") {
		t.Fatal("deletion was stale")
	}
	write(t, root, "unrelated/.agents/rules/no.md", "UNRELATED")
	if next := resolveFoundation(t, cache, dir); !next.Hit || strings.Contains(next.Config.Prompt, "UNRELATED") {
		t.Fatal("unrelated path affected scoped foundation")
	}
	write(t, home, ".agents/rules/home.md", "new home rule")
	if next := resolveFoundation(t, cache, dir); next.Hit || !strings.Contains(next.Config.Prompt, "new home rule") {
		t.Fatal("home edit was stale")
	}
}

func TestFoundationCopiesAllMutableState(t *testing.T) {
	_, root := fixture(t)
	write(t, root, ".agents/commands/go.md", "command body")
	write(t, root, ".mcp.json", `{"mcpServers":{"x":{"command":"test","args":["arg"],"env":{"SECRET":"private-env"},"headers":{"Authorization":"private-header"}}}}`)
	cache := projectconfig.NewFoundationCache()
	first := resolveFoundation(t, cache, root)
	first.Config.Prompt = "mutated"
	first.Config.Commands["go"] = "changed"
	first.Config.CommandDirs["go"] = "/elsewhere"
	first.Config.Warnings = append(first.Config.Warnings, "changed")
	server := first.Config.Servers["x"]
	server.Args[0] = "changed"
	server.Env["SECRET"] = "changed"
	server.Headers["Authorization"] = "changed"
	delete(first.Config.Servers, "x")
	first.Ref.ID = "corrupt"
	first.Ref.SchemaVersion++
	warm := resolveFoundation(t, cache, root)
	if !warm.Hit || warm.Config.Servers["x"].Env["SECRET"] != "private-env" || warm.Config.Servers["x"].Headers["Authorization"] != "private-header" {
		t.Fatal("mutable aliases escaped snapshot")
	}
	public, err := json.Marshal(warm.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), "private-") || strings.Contains(warm.Config.Prompt, "private-") {
		t.Fatal("server credentials escaped into foundation identity/guidance")
	}
}

func TestFoundationSymlinkSwapsAndLazyAssetReads(t *testing.T) {
	_, root := fixture(t)
	outside := t.TempDir()
	write(t, outside, "secret.md", "OUTSIDE_CONTENT")
	write(t, root, ".agents/rules/rule.md", "safe rule")
	asset := write(t, root, ".agents/references/info.txt", "first asset body")
	cache := projectconfig.NewFoundationCache()
	first := resolveFoundation(t, cache, root)
	write(t, root, ".agents/references/info.txt", "second asset body")
	warm := resolveFoundation(t, cache, root)
	if !warm.Hit || warm.Ref.ID != first.Ref.ID {
		t.Fatal("lazy asset body should not be eagerly hashed")
	}
	read := func(config *projectconfig.Config, path string) (string, error) {
		args, _ := json.Marshal(map[string]string{"action": "read", "path": path})
		return config.Tools()[0].Run(context.Background(), args)
	}
	if text, err := read(warm.Config, asset); err != nil || !strings.Contains(text, "second asset body") {
		t.Fatalf("lazy read: %q %v", text, err)
	}
	rule := filepath.Join(root, ".agents/rules/rule.md")
	if err := os.Remove(rule); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.md"), rule); err != nil {
		t.Fatal(err)
	}
	swapped := resolveFoundation(t, cache, root)
	if swapped.Hit || strings.Contains(swapped.Config.Prompt, "safe rule") || strings.Contains(swapped.Config.Prompt, "OUTSIDE_CONTENT") {
		t.Fatal("symlink file swap was unsafe/stale")
	}
	references := filepath.Join(root, ".agents/references")
	if err := os.Rename(references, references+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, references); err != nil {
		t.Fatal(err)
	}
	if _, err := read(warm.Config, asset); err == nil {
		t.Fatal("old lazy snapshot followed replaced parent")
	}
	if next := resolveFoundation(t, cache, root); next.Hit || strings.Contains(next.Config.Prompt, "OUTSIDE_CONTENT") {
		t.Fatal("parent swap was unsafe/stale")
	}
}

func TestFoundationPluginChangesAndSettingsOverrides(t *testing.T) {
	_, root := fixture(t)
	write(t, root, ".claude/plugins/demo/.claude-plugin/plugin.json", `{"name":"demo"}`)
	command := write(t, root, ".claude/plugins/demo/commands/do.md", "---\ndescription: first\n---\nbody")
	write(t, root, ".claude/settings.json", `{"mcpServers":{"x":{"command":"base"}}}`)
	write(t, root, ".claude/settings.local.json", `{"mcpServers":{"x":{"command":"local"}}}`)
	cache := projectconfig.NewFoundationCache()
	first := resolveFoundation(t, cache, root)
	if first.Config.Servers["x"].Command != "local" || !strings.Contains(first.Config.Expand("/demo:do"), "body") {
		t.Fatal("plugin/local override missing")
	}
	if err := os.WriteFile(command, []byte("---\ndescription: other\n---\nbody"), 0600); err != nil {
		t.Fatal(err)
	}
	if next := resolveFoundation(t, cache, root); next.Hit || next.Ref.ID == first.Ref.ID {
		t.Fatal("plugin metadata edit was stale")
	}
	write(t, root, ".claude/settings.local.json", `{"enabledPlugins":{"demo":false},"mcpServers":{"x":{"command":"fresh"}}}`)
	next := resolveFoundation(t, cache, root)
	if next.Hit || next.Config.Servers["x"].Command != "fresh" || len(next.Config.CommandNames()) != 0 {
		t.Fatal("plugin disable/settings edit was stale")
	}
	write(t, root, ".claude/settings.local.json", `{"enabledPlugins":{"demo":true}}`)
	write(t, root, ".claude/plugins/demo/.claude-plugin/plugin.json", `{"name":"renamed"}`)
	next = resolveFoundation(t, cache, root)
	if next.Hit || !strings.Contains(next.Config.Expand("/renamed:do"), "body") {
		t.Fatal("manifest edit was stale")
	}
}

func TestFoundationReadOnlyDirectoryToolsAndFatalOversize(t *testing.T) {
	_, root := fixture(t)
	target := t.TempDir()
	config := load(t, root)
	for _, tool := range config.Tools() {
		action := "search"
		if tool.Name == "workspace" {
			action = "inspect"
		}
		args, _ := json.Marshal(map[string]string{"action": action, "directory": target})
		if _, err := tool.Run(context.Background(), args); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("read-only lookup wrote files: %v %v", entries, err)
	}
	cache := projectconfig.NewFoundationCache()
	first := resolveFoundation(t, cache, root)
	write(t, root, "AGENTS.md", strings.Repeat("x", 512*1024+1))
	if _, err := cache.Resolve(context.Background(), root); err == nil {
		t.Fatal("oversize guidance silently retained cached snapshot")
	}
	if err := os.Remove(filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	if fresh := resolveFoundation(t, cache, root); fresh.Ref.ID != first.Ref.ID {
		t.Fatal("recovery after fatal source did not rediscover")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.Resolve(ctx, root); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestFoundationConcurrentIndependentSnapshotsAndHomeKeys(t *testing.T) {
	_, root := fixture(t)
	write(t, root, ".agents/commands/go.md", "body")
	cache := projectconfig.NewFoundationCache()
	first := resolveFoundation(t, cache, root)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snapshot, err := cache.Resolve(context.Background(), root)
			if err != nil {
				t.Error(err)
				return
			}
			if !snapshot.Hit || snapshot.Ref != first.Ref {
				t.Error("concurrent hit/ref mismatch")
			}
			snapshot.Config.Commands["go"] = "private mutation"
		}()
	}
	wg.Wait()
	otherHome := t.TempDir()
	write(t, otherHome, ".agents/rules/home.md", "OTHER_HOME")
	t.Setenv("HOME", otherHome)
	if next := resolveFoundation(t, cache, root); next.Hit || !strings.Contains(next.Config.Prompt, "OTHER_HOME") {
		t.Fatal("HOME key leaked unrelated home config")
	}
}

func TestFoundationFIFOReplacementNeverBlocksValidationOrReload(t *testing.T) {
	_, root := fixture(t)
	path := filepath.Join(root, "AGENTS.md")
	cache := projectconfig.NewFoundationCache()
	for attempt := 0; attempt < 20; attempt++ {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		write(t, root, "AGENTS.md", "regular guidance")
		resolveFoundation(t, cache, root)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(path, 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		done := make(chan error, 1)
		go func() {
			_, err := cache.Resolve(ctx, root)
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil && err != context.DeadlineExceeded {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			cancel()
			t.Fatal("FIFO replacement blocked cache validation despite context deadline")
		}
		cancel()
		// The uncached loader must also skip the replacement; a cache miss
		// must not reintroduce blocking after the validator safely rejects it.
		if config := load(t, root); strings.Contains(config.Prompt, "regular guidance") {
			t.Fatal("FIFO retained stale guidance")
		}
		cancelled, stop := context.WithCancel(context.Background())
		stop()
		if _, err := cache.Resolve(cancelled, root); err != context.Canceled {
			t.Fatalf("cancelled resolve: %v", err)
		}
	}
}

// Benchmark compares the actual validation fast path to ordinary discovery;
// filesystem work is intentionally retained, so speedup is not guaranteed.
func BenchmarkFoundationResolve(b *testing.B) {
	home, root := b.TempDir(), b.TempDir()
	b.Setenv("HOME", home)
	dir := filepath.Join(root, ".agents/commands")
	if err := os.MkdirAll(dir, 0700); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		text := "---\ndescription: example command description\n---\n" + strings.Repeat("inert body\n", 100)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("c%d.md", i)), []byte(text), 0600); err != nil {
			b.Fatal(err)
		}
	}
	b.Run("uncached", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := projectconfig.Load(root); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("validated_warm", func(b *testing.B) {
		cache := projectconfig.NewFoundationCache()
		if _, err := cache.Resolve(context.Background(), root); err != nil {
			b.Fatal(err)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			result, err := cache.Resolve(context.Background(), root)
			if err != nil || !result.Hit {
				b.Fatalf("warm result hit=%v err=%v", result.Hit, err)
			}
		}
	})
}
