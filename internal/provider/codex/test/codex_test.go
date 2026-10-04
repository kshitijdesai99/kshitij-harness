package test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kh/internal/config"
	"kh/internal/provider/codex"
	"kh/internal/terminal"
)

// A failed step (here: not logged in) must leave history resumable.
func TestSaveLoadAfterFailedStep(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := codex.New(config.Defaults, "", nil, nil)
	if _, err := p.Step(context.Background(), "hi", nil); err == nil {
		t.Fatal("want not-logged-in error")
	}
	b, err := p.Save()
	if err != nil {
		t.Fatal(err)
	}

	q := codex.New(config.Defaults, "", nil, nil)
	if err := q.Load(b); err != nil {
		t.Fatal(err)
	}
	b2, _ := q.Save()
	if string(b) != string(b2) {
		t.Errorf("round trip changed state:\n%s\n%s", b, b2)
	}
	var s struct{ Input []map[string]any }
	json.Unmarshal(b, &s)
	if len(s.Input) != 1 || s.Input[0]["role"] != "user" {
		t.Errorf("want just the user message, got %v", s.Input)
	}
}

func TestReplay(t *testing.T) {
	state := `{"session":"s","input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"count files"}]},
		{"type":"reasoning","encrypted_content":"x"},
		{"type":"function_call","call_id":"1","name":"bash","arguments":"{\"command\":\"ls | wc -l\"}"},
		{"type":"function_call_output","call_id":"1","output":"3"},
		{"type":"function_call","call_id":"2","name":"edit","arguments":"{\"path\":\"a.go\",\"old\":\"\",\"new\":\"x\"}"},
		{"type":"web_search_call","action":{"type":"search","query":"go 1.27"}},
		{"type":"web_search_call","action":{"type":"open_page","url":"https://go.dev"}},
		{"type":"web_search_call","action":{"type":"find","pattern":"release","url":"https://go.dev"}},
		{"type":"web_search_call","action":{"type":"open_page"}},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"There are 3."}]}
	]}`
	p := codex.New(config.Defaults, "", nil, nil)
	if err := p.Load([]byte(state)); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	p.Replay(terminal.Renderer{Out: &b, Err: &b})
	want := "> count files\n$ ls | wc -l\nedit a.go\nsearch: go 1.27\nopen: https://go.dev\nfind: release in https://go.dev\nThere are 3.\n"
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestReplayBatchEdits(t *testing.T) {
	state := `{"session":"s","input":[{"type":"function_call","call_id":"1","name":"edit","arguments":"{\"edits\":[{\"path\":\"a.go\",\"old\":\"x\",\"new\":\"y\"},{\"path\":\"a.go\",\"old\":\"y\",\"new\":\"z\"},{\"path\":\"b.go\",\"old\":\"\",\"new\":\"new\"}]}"}]}`
	p := codex.New(config.Defaults, "", nil, nil)
	if err := p.Load([]byte(state)); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	p.Replay(terminal.Renderer{Out: &out, Err: &out})
	if got := out.String(); got != "edit a.go\nedit b.go\n" {
		t.Fatalf("batch replay: %q", got)
	}
}

// Resuming keeps the session's repo map, so a changed repo can't break the cache.
func TestLoadKeepsSavedMap(t *testing.T) {
	b, _ := codex.New(config.Defaults, "old map", nil, nil).Save()
	p := codex.New(config.Defaults, "new map", nil, nil)
	p.Load(b)
	if got, _ := p.Save(); !strings.Contains(string(got), `"map":"old map"`) {
		t.Errorf("resumed with the new map: %s", got)
	}
}

func TestReplayImageRedacted(t *testing.T) {
	state := `{"session":"s","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"What's this?"},{"type":"input_image","image_url":"data:image/png;base64,SECRET"}]}]}`
	p := codex.New(config.Defaults, "", nil, nil)
	if err := p.Load([]byte(state)); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	p.Replay(terminal.Renderer{Out: &out, Err: &out})
	if got := out.String(); got != "> What's this?\n[image attached]\n" {
		t.Errorf("replay: %q", got)
	}
	b, err := p.Save()
	if err != nil || !strings.Contains(string(b), "data:image/png;base64,SECRET") {
		t.Errorf("image lost on save: %v", err)
	}
}

func TestLastResponseUsesLatestAssistantMessage(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"empty history", `[]`, ""},
		{"no assistant", `[{"type":"message","role":"user","content":[{"type":"input_text","text":"question"}]}]`, ""},
		{"consecutive assistant messages", `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"earlier"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"latest"}]}]`, "latest"},
		{"multipart", `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first part"},{"type":"output_text","text":"second part"}]}]`, "first part\nsecond part"},
		{"trailing query and actions", `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"next question"}]},{"type":"function_call","name":"bash","arguments":"{}"},{"type":"function_call_output","output":"tool output"},{"type":"web_search_call","action":{"type":"search","query":"query"}},{"type":"reasoning","encrypted_content":"secret"}]`, "answer"},
		{"latest empty content", `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"stale"}]},{"type":"message","role":"assistant","content":[]}]`, ""},
		{"latest empty text", `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"stale"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}]`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := codex.New(config.Defaults, "", nil, nil)
			if err := p.Load([]byte(`{"session":"s","input":` + tc.input + `}`)); err != nil {
				t.Fatal(err)
			}
			before, err := p.Save()
			if err != nil {
				t.Fatal(err)
			}
			if got := p.LastResponse(); got != tc.want {
				t.Fatalf("LastResponse=%q want=%q", got, tc.want)
			}
			after, err := p.Save()
			if err != nil || string(after) != string(before) {
				t.Fatalf("inspection changed saved history: err=%v before=%s after=%s", err, before, after)
			}
		})
	}
}

func TestReplayDescribesToolsByName(t *testing.T) {
	state := `{"session":"s","input":[
		{"type":"function_call","name":"memory","arguments":"{\"action\":\"search\",\"path\":\"not-an-edit\"}"},
		{"type":"function_call","name":"agents","arguments":"{\"action\":\"send\",\"address\":\"kh:main\",\"path\":\"not-an-edit\"}"},
		{"type":"function_call","name":"agents","arguments":"{\"action\":\"spawn\",\"name\":\"review\"}"},
		{"type":"function_call","name":"custom","arguments":"{\"command\":\"not-bash\",\"edits\":[{\"path\":\"not-an-edit\"}]}"}
	]}`
	p := codex.New(config.Defaults, "", nil, nil)
	if err := p.Load([]byte(state)); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	p.Replay(terminal.Renderer{Out: &out, Err: &out})
	want := "memory search\nagents send kh:main\nagents spawn review\ntool custom\n"
	if got := out.String(); got != want {
		t.Fatalf("named tool replay: got %q want %q", got, want)
	}
}

func TestUseSwitchesModelAndEffort(t *testing.T) {
	p := codex.New(config.Defaults, "", nil, nil)
	if m, e := p.Use("", ""); m != config.Defaults.Model || e != config.Defaults.Effort {
		t.Errorf("show = %s %s", m, e)
	}
	p.Use("gpt-5.5", "")
	if m, e := p.Use("", "high"); m != "gpt-5.5" || e != "high" {
		t.Errorf("after switch = %s %s", m, e)
	}
}
