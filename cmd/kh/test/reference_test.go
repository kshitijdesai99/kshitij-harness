package test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kh/internal/memory"
)

func TestReferenceCLITransportAndSoundDemo(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "kh")
	build := exec.Command("go", "build", "-o", binary, "./cmd/kh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %s %v", out, err)
	}
	isolateCLIFixture(t)
	home := t.TempDir()
	home2 := t.TempDir()
	run := func(home, input string, args ...string) ([]byte, error) {
		cmd := exec.Command(binary, append([]string{"memory", "reference"}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home, "KH_AUTO_REBUILD=0", "KH_AUTO=", "TMUX=", "TMUX_PANE=")
		cmd.Stdin = strings.NewReader(input)
		return cmd.CombinedOutput()
	}
	if out, err := run(home, "", "demo"); err != nil || !strings.Contains(string(out), "victory-chime") {
		t.Fatalf("demo: %s %v", out, err)
	}
	data, err := run(home, "", "export", "victory-chime")
	if err != nil {
		t.Fatalf("export: %s %v", data, err)
	}
	var r memory.Reference
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("transport: %s %v", data, err)
	}
	if r.ReferenceID == "" || r.Version != 1 || len(r.Assets) != 1 || !strings.Contains(r.Assets[0].Source, "afplay") {
		t.Fatalf("demo payload: %+v", r)
	}
	source, err := run(home, "", "asset", "victory-chime", r.Assets[0].Name)
	if err != nil || string(source) != r.Assets[0].Source {
		t.Fatalf("asset source: %s %v", source, err)
	}
	if out, err := run(home2, string(data), "import", "repo"); err != nil {
		t.Fatalf("import: %s %v", out, err)
	}
	imported, err := run(home2, "", "export", "cool sound")
	if err != nil {
		t.Fatalf("import lookup: %s %v", imported, err)
	}
	var got memory.Reference
	if err := json.Unmarshal(imported, &got); err != nil {
		t.Fatal(err)
	}
	if got.ReferenceID != r.ReferenceID || got.Version != r.Version || got.Procedure != r.Procedure || got.Note.Source != "import" || got.Note.ContextSummary != r.Note.ContextSummary || got.Origin == nil || got.Origin.Source != r.Note.Source {
		t.Fatalf("import provenance: %+v", got)
	}
	if out, err := run(home2, string(data), "import", "repo"); err == nil {
		t.Fatalf("import overwrote existing item: %s", out)
	}
	r.Note.Key = "new-procedure"
	r.Note.ID = 0
	r.ReferenceID = ""
	r.Version = 0
	r.Aliases = nil
	r.Note.Text = "A new independent procedure"
	r.Procedure = "Safe instructions"
	r.Assets = nil
	encoded, _ := json.Marshal(r)
	if out, err := run(home, string(encoded), "put", "repo"); err != nil {
		t.Fatalf("put: %s %v", out, err)
	}
	if out, err := run(home, "", "get", "new-procedure"); err != nil || !strings.Contains(string(out), "Safe instructions") {
		t.Fatalf("get: %s %v", out, err)
	}
	bad := `{"Note":{"Key":"bad-unicode","Text":"Bad transport"},"Procedure":"Safe instructions","Assets":[{"Name":"bad.sh","Interpreter":"sh","Source":"printf '\ud800'"}]}`
	if out, err := run(home, bad, "put", "repo"); err == nil {
		t.Fatalf("accepted lossy script transport: %s", out)
	}
	low := strings.ReplaceAll(bad, `\ud800`, `\udc00`)
	if out, err := run(home, low, "put", "repo"); err == nil {
		t.Fatalf("accepted unpaired low surrogate: %s", out)
	}
	for i, source := range []string{`printf '\\ud800'`, `printf '\ud83c\udfb6'`} {
		valid := `{"Note":{"Key":"valid-unicode-` + string(rune('a'+i)) + `","Text":"Valid transport"},"Procedure":"Safe instructions","Assets":[{"Name":"valid.sh","Interpreter":"sh","Source":"` + source + `"}]}`
		if out, err := run(home, valid, "put", "repo"); err != nil {
			t.Fatalf("rejected valid Unicode transport: %s %v", out, err)
		}
	}
	r.Note.Key = "long-provenance"
	r.Note.ContextSummary = strings.Repeat("x", 780) + "PROVENANCE-END"
	r.Version = 7
	r.Note.StatedAt = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	beforePut := time.Now()
	encoded, _ = json.Marshal(r)
	if out, err := run(home, string(encoded), "put", "repo"); err != nil {
		t.Fatalf("put long provenance: %s %v", out, err)
	}
	transport, err := run(home, "", "export", "long-provenance")
	if err != nil {
		t.Fatal(err)
	}
	var original memory.Reference
	if err = json.Unmarshal(transport, &original); err != nil {
		t.Fatal(err)
	}
	if original.Version != 1 {
		t.Fatalf("ordinary creation used supplied version: %d", original.Version)
	}
	if original.Note.StatedAt.Before(beforePut) {
		t.Fatalf("ordinary put reused old statement date: %s", original.Note.StatedAt)
	}
	home3 := t.TempDir()
	if out, err := run(home3, string(transport), "import", "repo"); err != nil {
		t.Fatalf("import long provenance: %s %v", out, err)
	}
	transport, err = run(home3, "", "export", "long-provenance")
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip memory.Reference
	if err = json.Unmarshal(transport, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if roundtrip.Note.ContextSummary != original.Note.ContextSummary || roundtrip.Origin == nil || roundtrip.Origin.ContextSummary != original.Note.ContextSummary || !roundtrip.Note.StatedAt.Equal(original.Note.StatedAt) {
		t.Fatalf("lost provenance: %+v", roundtrip)
	}
}
