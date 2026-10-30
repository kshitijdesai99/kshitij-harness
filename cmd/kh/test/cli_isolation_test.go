package test

import (
	"os"
	"path/filepath"
	"testing"

	"kh/internal/projectconfig"
)

// Call after building so Go retains its normal module/cache environment. CLI
// sessions need both a private HOME and cwd: project discovery also reads every
// cwd ancestor, which can include the user's real HOME even after overriding it.
// Changing the test cwd keeps session.Save and memory.RepoScope setup aligned
// with child processes, including sessions subsequently spawned through tmux.
func isolateCLIFixture(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
}

func TestCLIFixtureIsolation(t *testing.T) {
	ambient := t.TempDir()
	cwd := filepath.Join(ambient, "project")
	if err := os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ambient, ".mcp.json"), []byte(`{"mcpServers":{"ambient-fixture":{"command":"must-not-run"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", ambient)
	t.Chdir(cwd)
	isolateCLIFixture(t)

	home, err := os.UserHomeDir()
	if err != nil || home == ambient {
		t.Fatalf("HOME not isolated: %q: %v", home, err)
	}
	isolated, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	config, err := projectconfig.Load(isolated)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Servers) != 0 || config.Prompt != "" {
		t.Fatalf("ambient project configuration leaked: servers=%v prompt=%q", config.Servers, config.Prompt)
	}
}
