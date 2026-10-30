package test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rivo/uniseg"
	"kh/internal/session"
	"kh/internal/terminal"
)

// TestSessionIdentityLifecycle exercises actual CLI saves without credentials.
func TestSessionIdentityLifecycle(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	isolateCLIFixture(t)
	for _, tc := range []struct {
		name, parent, agent, saved string
		want                       *session.Identity
	}{
		{name: "main", want: &session.Identity{Role: "main"}},
		{name: "named main", agent: "kh:new", want: &session.Identity{Role: "main", Name: "new"}},
		{name: "worker", parent: "kh:main", agent: "kh:review", want: &session.Identity{Role: "subagent", Name: "review", Parent: "kh:main"}},
		{name: "resume worker in main", saved: `{"version":1,"provider":"codex","identity":{"role":"subagent","name":"original","parent":"kh:parent"},"state":{"session":"s","input":[]}}`, want: &session.Identity{Role: "subagent", Name: "original", Parent: "kh:parent"}},
		{name: "resume main in worker", parent: "kh:main", agent: "kh:review", saved: `{"version":1,"provider":"codex","identity":{"role":"main","name":"original"},"state":{"session":"s","input":[]}}`, want: &session.Identity{Role: "main", Name: "original"}},
		{name: "legacy unknown in worker", parent: "kh:main", agent: "kh:review", saved: `{"session":"s","input":[]}`},
		{name: "envelope unknown in worker", parent: "kh:main", agent: "kh:review", saved: `{"version":1,"provider":"codex","state":{"session":"s","input":[]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			args := []string{"-p"}
			if tc.saved != "" {
				if err := session.Save("existing", []byte(tc.saved)); err != nil {
					t.Fatal(err)
				}
				args = append(args, "-s", "existing")
			}
			args = append(args, "question")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Env = append(os.Environ(), "KH_AUTO_REBUILD=0", "KH_PROVIDER=codex", "KH_MODEL=", "KH_EFFORT=", "KH_PARENT="+tc.parent, "KH_AGENT="+tc.agent, "TMUX=", "TMUX_PANE=")
			cmd.Stdin = strings.NewReader("")
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil || !strings.Contains(string(out), "not logged in") {
				t.Fatalf("expected unauthenticated turn: %s: %v", out, err)
			}
			ids := session.List()
			if len(ids) != 1 {
				t.Fatalf("saved sessions: %v, output: %s", ids, out)
			}
			b, err := session.Load(ids[0])
			if err != nil {
				t.Fatal(err)
			}
			r, err := session.Decode(b)
			if err != nil || !reflect.DeepEqual(r.Identity, tc.want) {
				t.Fatalf("identity %+v, want %+v: %v", r.Identity, tc.want, err)
			}
		})
	}
}

// A private tmux server supplies a genuine stdout PTY without touching the
// user's server. Conflicting COLUMNS must not override its measured pane width.
func TestSessionsTTYWidth(t *testing.T) {
	t.Setenv("KH_AUTO_REBUILD", "0")
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	isolateCLIFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	id := "20260101-123400.123456789"
	preview := strings.Repeat("界e\u0301👩‍💻", 30)
	if err := session.Save(id, []byte(fmt.Sprintf(`{"session":"s","input":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]}`, preview))); err != nil {
		t.Fatal(err)
	}
	entries := session.Recent()
	if len(entries) != 1 {
		t.Fatalf("saved entries: %v", entries)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("kh_sessions_%d_%d", os.Getpid(), time.Now().UnixNano())
	runTmux := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("tmux", append([]string{"-L", socket, "-f", "/dev/null"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %s: %v", args, out, err)
		}
		return strings.TrimRight(string(out), "\n")
	}
	defer func() {
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	}()
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	for _, width := range []int{80, 30, 12} {
		name := fmt.Sprintf("w%d", width)
		ready := filepath.Join(t.TempDir(), "ready")
		command := "cd " + quote(wd) + " && env HOME=" + quote(home) +
			" KH_PROVIDER= KH_MODEL= KH_EFFORT= COLUMNS=1 " + quote(binary) +
			" --sessions; status=$?; printf '%s' \"$status\" > " + quote(ready) + "; sleep 60"
		runTmux("new-session", "-d", "-s", name, "-x", fmt.Sprint(width), "-y", "10", "/bin/sh -c "+quote(command))
		deadline := time.Now().Add(5 * time.Second)
		for {
			status, err := os.ReadFile(ready)
			if err == nil && len(status) > 0 {
				if string(status) != "0" {
					t.Fatalf("TTY listing exited %s", status)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("TTY listing did not complete: %q", runTmux("capture-pane", "-p", "-t", name))
			}
			time.Sleep(10 * time.Millisecond)
		}
		if got := runTmux("display-message", "-p", "-t", name, "#{pane_width}"); got != fmt.Sprint(width) {
			t.Fatalf("PTY width: got %q want %d", got, width)
		}
		out := runTmux("capture-pane", "-p", "-t", name)
		want := terminal.SessionRow(entries[0].SavedAt, id, "[unknown] "+preview, width)
		if out != want {
			t.Errorf("TTY width %d (COLUMNS=1): got %q want %q", width, out, want)
		}
	}
}

func TestSessionsLastResponseAndSavedOrdering(t *testing.T) {
	t.Setenv("KH_AUTO_REBUILD", "0")
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	isolateCLIFixture(t)
	t.Setenv("HOME", t.TempDir())
	dir := session.Folder()
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
		state := `{"session":"s","input":[]}`
		if i == 20 {
			state = `{"version":1,"provider":"codex","identity":{"role":"subagent","name":"review\u001b[31m","parent":"kh:main"},"state":{"session":"s","input":[]}}`
		}
		save(fmt.Sprintf("z%02d", i), state, time.Duration(i)*time.Minute)
	}
	save("a-resumed", `{"version":1,"provider":"codex","identity":{"role":"main"},"state":{"session":"s","input":[
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
	save("g-controls", `{"session":"s","input":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"\u001b[31mSafe\u001b[0m\n\u001b]0;hidden\u0007\tpreview"}]}]}`, 25*time.Minute)
	save("h-graphemes", fmt.Sprintf(`{"session":"s","input":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]}`, strings.Repeat("e\u0301👩‍💻", 100)), 24*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--sessions")
	cmd.Env = append(os.Environ(), "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", "COLUMNS=")
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list: %s: %v", out, err)
	}
	text := string(out)
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 29 {
		t.Fatalf("got %d lines: %s", len(lines), text)
	}
	if !strings.Contains(lines[0], "a-resumed  [main] Final answer second part") || !strings.HasPrefix(lines[0], "2026-01-01 00:30") {
		t.Fatalf("last reply/order: %s", text)
	}
	for _, noise := range []string{"opening question", "obsolete answer", "tool noise", "unanswered follow-up"} {
		if strings.Contains(text, noise) {
			t.Errorf("preview contains %q", noise)
		}
	}
	if !utf8.Valid(out) || !strings.HasSuffix(lines[1], strings.Repeat("界", 28)+"...") {
		t.Errorf("unicode preview: %s", lines[1])
	}
	if !strings.Contains(lines[2], "c-broken  [unknown] [unavailable:") || !strings.Contains(lines[3], "d-unsupported  [unknown] [unavailable:") {
		t.Errorf("unavailable: %s", text)
	}
	if !strings.Contains(lines[4], "f-tie  [unknown] [no assistant response yet]") || !strings.Contains(lines[5], "e-tie  [unknown] [no assistant response yet]") {
		t.Errorf("ties/empty: %s", text)
	}
	if !strings.HasSuffix(lines[6], "g-controls  [unknown] Safe preview") || strings.Contains(text, "\x1b") || strings.Contains(text, "hidden") {
		t.Errorf("terminal controls leaked: %q", text)
	}
	if !strings.HasSuffix(lines[7], strings.Repeat("e\u0301👩‍💻", 19)+"...") {
		t.Errorf("grapheme preview: %q", lines[7])
	}
	for i := 0; i < 21; i++ {
		if !strings.Contains(text, fmt.Sprintf("z%02d", i)) {
			t.Errorf("listing omitted saved row z%02d: %s", i, text)
		}
	}
	if !strings.Contains(text, "z20  [subagent: review] [no assistant response yet]") {
		t.Errorf("worker label missing: %s", text)
	}
	if latest, err := session.Latest(); err != nil || latest != "z20" {
		t.Errorf("creation ordering changed: %q %v", latest, err)
	}
	for _, width := range []int{120, 80, 30, 12, 3, 1} {
		cmd := exec.CommandContext(ctx, binary, "--sessions")
		cmd.Env = append(os.Environ(), "KH_PROVIDER=", "KH_MODEL=", "KH_EFFORT=", fmt.Sprintf("COLUMNS=%d", width))
		cmd.Stdin = strings.NewReader("")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("width %d: %s: %v", width, out, err)
		}
		rows := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
		if len(rows) != 29 || !utf8.Valid(out) {
			t.Fatalf("width %d: invalid listing: %q", width, out)
		}
		for _, row := range rows {
			if cells := uniseg.StringWidth(row); cells > width {
				t.Errorf("width %d: %d cells: %q", width, cells, row)
			}
		}
		if width >= len("a-resumed") && !strings.Contains(rows[0], "a-resumed") {
			t.Errorf("width %d lost exact ID: %q", width, rows[0])
		}
		if width == 12 && strings.HasPrefix(rows[0], "2026-01-01") {
			t.Errorf("narrow listing should omit time: %q", rows[0])
		}
	}
}
