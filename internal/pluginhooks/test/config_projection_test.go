package pluginhooks_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"kh/internal/pluginhooks"
	"kh/internal/projectconfig"
)

func TestProjectionPreservesClaudeRuntimeConfig(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "default"
		if custom {
			name = "custom"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			if err := os.Unsetenv("CLAUDE_CONFIG_DIR"); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(home, ".claude")
			if custom {
				config = filepath.Join(home, "custom config")
				t.Setenv("CLAUDE_CONFIG_DIR", config)
			}
			if err := os.MkdirAll(config, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(config, "auth-sentinel"), []byte("fixture, not a credential"), 0600); err != nil {
				t.Fatal(err)
			}
			tr, err := pluginhooks.OpenTranscript("", t.TempDir(), "auth-session")
			if err != nil {
				t.Fatal(err)
			}
			if tr.ConfigDir() != config {
				t.Fatal(tr.ConfigDir())
			}
			if err := tr.AppendUser("receipt", "actual user", time.Now()); err != nil {
				t.Fatal(err)
			}
			in := input(t)
			in.SessionID = "auth-session"
			in.TranscriptPath = tr.Path()
			r := pluginhooks.Runner{}
			if _, overridden := r.Environment(in, "")["CLAUDE_CONFIG_DIR"]; overridden {
				t.Fatal("runtime config overridden")
			}
			// Like Cogit's detached side recorder, the second process inherits the
			// hook environment and scans projects by session ID rather than input path.
			h := hook(`read line; /bin/sh -c 'root=${CLAUDE_CONFIG_DIR:-$HOME/.claude}; test -f "$root/auth-sentinel" || exit 11; found=0; for p in "$root"/projects/*/"$CLAUDE_SESSION_ID.jsonl"; do test ! -f "$p" || found=1; done; test "$found" = 1' || exit $?; printf '%s' '{"additionalContext":"auth-and-transcript-ok"}'`)
			got := r.Run(context.Background(), []projectconfig.Hook{h}, in)
			if len(got.Diagnostics) != 0 || strings.Join(got.AdditionalContext, "") != "auth-and-transcript-ok" {
				t.Fatalf("%+v", got)
			}
			info, err := os.Stat(config)
			if err != nil || info.Mode().Perm() != 0755 {
				t.Fatalf("shared config permissions changed: %v %v", info, err)
			}
		})
	}
}

func TestProjectionNamespaceDoesNotTouchNativeData(t *testing.T) {
	config := t.TempDir()
	projects := filepath.Join(config, "projects")
	if err := os.Mkdir(projects, 0755); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(projects, "native-project")
	if err := os.Mkdir(native, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(native, "session.jsonl")
	if err := os.WriteFile(file, []byte("native data\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	a, err := pluginhooks.OpenTranscript(config, filepath.Join(cwd, "a.b"), "session")
	if err != nil {
		t.Fatal(err)
	}
	b, err := pluginhooks.OpenTranscript(config, filepath.Join(cwd, "a-b"), "session")
	if err != nil {
		t.Fatal(err)
	}
	if a.Path() == b.Path() || !strings.HasPrefix(filepath.Base(filepath.Dir(a.Path())), "kh-plugin-") {
		t.Fatal("namespace collision")
	}
	if err := a.AppendUser("id", "question", time.Now()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "native data\n" {
		t.Fatal("native data modified", err)
	}
	for _, p := range []string{projects, native} {
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != 0755 {
			t.Fatalf("shared permissions changed: %v %v", info, err)
		}
	}
}

func TestProjectionMigratesPrivateHistoryWithoutRewriting(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "runtime"))
	project := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(cwd, "-")
	legacy := filepath.Join(home, ".kh", "plugin-transcripts", "config", "projects", project, "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"type": "user", "uuid": "stable-id", "sessionId": "session", "cwd": cwd, "timestamp": "2026-11-07T00:00:00Z", "message": map[string]string{"role": "user", "content": "previous question"}})
	data = append(data, '\n')
	if err := os.WriteFile(legacy, data, 0600); err != nil {
		t.Fatal(err)
	}
	tr, err := pluginhooks.OpenTranscript("", cwd, "session")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(tr.Path())
	if err != nil || string(got) != string(data) {
		t.Fatalf("migration changed history: %s %v", got, err)
	}
	if err := tr.AppendAssistant("reply", "new reply", time.Now()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(tr.Path())
	if _, err := pluginhooks.OpenTranscript("", cwd, "session"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(tr.Path())
	original, _ := os.ReadFile(legacy)
	if string(before) != string(after) || string(original) != string(data) {
		t.Fatal("reopen overwrote projection or legacy source")
	}
}

func TestProjectionRejectsUnsafeLegacyHistory(t *testing.T) {
	for _, kind := range []string{"symlink", "wrong-session", "partial"} {
		t.Run(kind, func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			project := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(cwd, "-")
			legacy := filepath.Join(home, ".kh", "plugin-transcripts", "config", "projects", project, "session.jsonl")
			if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
				t.Fatal(err)
			}
			if kind == "symlink" {
				if err := os.Symlink(filepath.Join(home, "outside"), legacy); err != nil {
					t.Fatal(err)
				}
			} else {
				data := "{\"type\":\"user\",\"uuid\":\"id\",\"sessionId\":\"other\"}\n"
				if kind == "partial" {
					data = "{\"type\":\"user\"}"
				}
				if err := os.WriteFile(legacy, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := pluginhooks.OpenTranscript("", cwd, "session"); err == nil {
				t.Fatal("accepted unsafe legacy projection")
			}
		})
	}
}

func TestProjectionRejectsHardLinkedNativeFile(t *testing.T) {
	config, cwd := t.TempDir(), t.TempDir()
	tr, err := pluginhooks.OpenTranscript(config, cwd, "session")
	if err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(config, "native.jsonl")
	if err := os.WriteFile(native, []byte("native data\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(tr.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(native, tr.Path()); err != nil {
		t.Fatal(err)
	}
	if err := tr.AppendUser("id", "question", time.Now()); err == nil {
		t.Fatal("accepted hard link")
	}
	data, err := os.ReadFile(native)
	info, statErr := os.Stat(native)
	if err != nil || statErr != nil || string(data) != "native data\n" || info.Mode().Perm() != 0644 {
		t.Fatal("native hard-linked data or mode changed")
	}
}

func TestProjectionRejectsDirectoryReplacement(t *testing.T) {
	config, cwd := t.TempDir(), t.TempDir()
	tr, err := pluginhooks.OpenTranscript(config, cwd, "session")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(tr.Path())
	if err := os.Rename(dir, dir+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir+"-saved", dir); err != nil {
		t.Fatal(err)
	}
	if err := tr.AppendUser("id", "question", time.Now()); err == nil {
		t.Fatal("followed replaced project symlink")
	}
	if _, err := pluginhooks.OpenTranscript(config, cwd, "session"); err == nil {
		t.Fatal("accepted project symlink")
	}
	config = t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(config, "projects")); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginhooks.OpenTranscript(config, cwd, "session"); err == nil {
		t.Fatal("accepted projects symlink")
	}
}
