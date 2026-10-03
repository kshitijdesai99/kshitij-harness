package test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"kh/internal/config"
	"kh/internal/session"
)

func TestSessionsLastResponseAndSavedOrdering(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	t.Setenv("HOME", t.TempDir())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(wd); err == nil {
		wd = resolved
	}
	dir := filepath.Join(config.Dir(), "sessions", strings.ReplaceAll(wd, string(filepath.Separator), "-"))
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	save := func(id, state string, offset time.Duration) {
		t.Helper()
		if err := session.Save(id, []byte(state)); err != nil {
			t.Fatal(err)
		}
		stamp := base.Add(offset)
		if err := os.Chtimes(filepath.Join(dir, id+".json"), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 21; i++ {
		save(fmt.Sprintf("z%02d", i), `{"session":"s","input":[]}`, time.Duration(i)*time.Minute)
	}
	save("a-resumed", `{"version":1,"provider":"codex","state":{"session":"s","input":[
  {"type":"message","role":"user","content":[{"type":"input_text","text":"opening question"}]},
  {"type":"message","role":"assistant","content":[{"type":"output_text","text":"obsolete answer"}]},
  {"type":"function_call","arguments":"{\"command\":\"tool noise\"}"},
  {"type":"message","role":"assistant","content":[{"type":"output_text","text":"Final answer\n"},{"type":"output_text","text":"second part"}]},
  {"type":"message","role":"user","content":[{"type":"input_text","text":"unanswered follow-up"}]}
 ]}}`, 30*time.Minute)
	save("b-unicode", fmt.Sprintf(`{"session":"s","input":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]}`, strings.Repeat("界", 170)), 29*time.Minute)
	save("c-broken", `{broken`, 28*time.Minute)
	save("d-unsupported", `{"version":1,"provider":"missing","state":{}}`, 27*time.Minute)
	// Equal modification times have a stable descending-ID tie break.
	save("e-tie", `{"session":"s","input":[]}`, 26*time.Minute)
	save("f-tie", `{"session":"s","input":[]}`, 26*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--sessions")
	cmd.Env = append(os.Environ(), "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list: %s: %v", out, err)
	}
	text := string(out)
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 20 {
		t.Fatalf("got %d lines: %s", len(lines), text)
	}
	if !strings.Contains(lines[0], "a-resumed  Final answer second part") || !strings.HasPrefix(lines[0], "2026-01-01 00:30") {
		t.Fatalf("last reply/order: %s", text)
	}
	for _, noise := range []string{"opening question", "obsolete answer", "tool noise", "unanswered follow-up"} {
		if strings.Contains(text, noise) {
			t.Errorf("preview contains %q", noise)
		}
	}
	if !utf8.Valid(out) || !strings.HasSuffix(lines[1], strings.Repeat("界", 67)+"...") {
		t.Errorf("unicode preview: %s", lines[1])
	}
	if !strings.Contains(lines[2], "c-broken  [unavailable:") || !strings.Contains(lines[3], "d-unsupported  [unavailable:") {
		t.Errorf("unavailable: %s", text)
	}
	if !strings.Contains(lines[4], "f-tie  [no assistant response yet]") || !strings.Contains(lines[5], "e-tie  [no assistant response yet]") {
		t.Errorf("ties/empty: %s", text)
	}
	if strings.Contains(text, "z00") {
		t.Errorf("old saves should not displace resumed session: %s", text)
	}
	if latest, err := session.Latest(); err != nil || latest != "z20" {
		t.Errorf("creation ordering changed: %q %v", latest, err)
	}
}
