package test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kh/internal/learn"
	"kh/internal/memory"
	"kh/internal/provider"
)

const repo = "github.com/me/app"

// fake answers each hook call from a function of (system prompt, user prompt).
type fake struct {
	out    provider.Output
	system string
	answer func(system, prompt string) string
}

func (f *fake) Step(_ context.Context, user string, _ []provider.Result) ([]provider.Call, error) {
	f.out.Reply(f.answer(f.system, user))
	return nil, nil
}
func (f *fake) Save() ([]byte, error)                     { return nil, nil }
func (f *fake) Load([]byte) error                         { return nil }
func (f *fake) Replay(provider.Output)                    {}
func (f *fake) Stats() provider.Stats                     { return provider.Stats{} }
func (f *fake) Use(model, effort string) (string, string) { return "", "" }

type calls struct {
	sync.Mutex
	prompts []string
}

func model(c *calls, answer func(system, prompt string) string) learn.Model {
	return func(system string, out provider.Output) (provider.Provider, error) {
		return &fake{out: out, system: system, answer: func(s, p string) string {
			c.Lock()
			c.prompts = append(c.prompts, s)
			c.Unlock()
			return answer(s, p)
		}}, nil
	}
}

func is(system, kind string) bool { return strings.Contains(system, "memory of "+kind) }

func open(t *testing.T) *memory.Store {
	s, err := memory.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPreHookSavesThenReplacesARule(t *testing.T) {
	s, c := open(t), &calls{}
	text := "Use Tailwind over plain CSS."
	answer := func(system, prompt string) string {
		if is(system, "lasting user instructions") {
			return "```json\n{\"save\":[{\"key\":\"css-framework\",\"text\":\"" + text + "\",\"scope\":\"repo\"}]}\n```"
		}
		return `{"overview":"Web app.","keys":["css-framework"]}`
	}
	l := learn.New(s, repo, "me@example.com", model(c, answer), 10000)
	l.Message("always use tailwind")
	notes := strings.Join(l.Close(5*time.Second), "\n")
	if !strings.Contains(notes, "Saved instruction css-framework") {
		t.Fatalf("notices: %q", notes)
	}
	text = "Use plain CSS."
	l = learn.New(s, repo, "me@example.com", model(c, answer), 10000)
	l.Message("actually, plain CSS")
	notes = strings.Join(l.Close(5*time.Second), "\n")
	if !strings.Contains(notes, `Replaced instruction css-framework: "Use Tailwind over plain CSS." -> "Use plain CSS."`) {
		t.Fatalf("notices: %q", notes)
	}
	cur, _ := s.Current(context.Background(), repo, "instruction")
	if len(cur) != 1 || cur[0].Text != "Use plain CSS." || cur[0].Owner != "me@example.com" || cur[0].Source != "pre-hook" {
		t.Fatalf("current: %+v", cur)
	}
}

func TestPostHookRunsOnlyWhenAFailureWasFixed(t *testing.T) {
	s, c := open(t), &calls{}
	answer := func(system, prompt string) string {
		if is(system, "gotchas") {
			return `{"save":[{"key":"test-db","text":"Tests need the database running first."}]}`
		}
		return `{"save":[]}`
	}
	l := learn.New(s, repo, "", model(c, answer), 10000)
	l.Finished([]learn.Step{{Name: "bash", Output: "ok"}}, "done")
	l.Finished([]learn.Step{{Name: "bash", Output: "boom\nexit status 1", Failed: true}, {Name: "edit"}}, "gave up")
	l.Close(5 * time.Second)
	for _, p := range c.prompts {
		if is(p, "gotchas") {
			t.Fatal("post-hook ran without a fixed failure")
		}
	}
	l = learn.New(s, repo, "", model(c, answer), 10000)
	l.Finished([]learn.Step{{Name: "bash", Output: "connection refused\nexit status 1", Failed: true}, {Name: "bash", Output: "PASS"}}, "fixed")
	if notes := strings.Join(l.Close(5*time.Second), "\n"); !strings.Contains(notes, "Saved gotcha test-db") {
		t.Fatalf("notices: %q", notes)
	}
}

func TestHookDecisionOnOldStateIsDropped(t *testing.T) {
	s, c := open(t), &calls{}
	answer := func(system, prompt string) string {
		if is(system, "lasting user instructions") {
			// Another kh process saves the same topic while this hook thinks.
			s.Add(context.Background(), memory.Note{Repo: repo, Kind: "instruction", Key: "css-framework", Text: "Use Bootstrap.", Source: "you"}, memory.Any)
			return `{"save":[{"key":"css-framework","text":"Use Tailwind."}]}`
		}
		return `{"overview":"x","keys":[]}`
	}
	l := learn.New(s, repo, "", model(c, answer), 10000)
	l.Message("use tailwind")
	l.Close(5 * time.Second)
	cur, _ := s.Current(context.Background(), repo, "instruction")
	if len(cur) != 1 || cur[0].Text != "Use Bootstrap." {
		t.Fatalf("stale hook overwrote a newer change: %+v", cur)
	}
}

func TestSummaryRebuildsOnlyWhenChangedAndFitsLimit(t *testing.T) {
	s, c, ctx := open(t), &calls{}, context.Background()
	for _, k := range []string{"one", "two", "three"} {
		s.Add(ctx, memory.Note{Repo: repo, Kind: "instruction", Key: k, Text: strings.Repeat(k+" ", 20), Source: "you"}, memory.Any)
	}
	answer := func(system, prompt string) string {
		return `{"overview":"Short overview.","keys":["one","two","missing","three"]}`
	}
	l := learn.New(s, repo, "", model(c, answer), 200) // room for about two notes
	l.Close(5 * time.Second)
	sum, ok, _ := s.Summary(ctx, repo)
	if !ok || sum.Text != "Short overview." || len(sum.Refs) == 0 || len(sum.Refs) > 2 {
		t.Fatalf("summary: %+v", sum)
	}
	for _, r := range sum.Refs {
		if r == "missing" {
			t.Fatal("summary kept an unknown key")
		}
	}
	before := len(c.prompts)
	l = learn.New(s, repo, "", model(c, answer), 200)
	l.Finished(nil, "")
	l.Close(5 * time.Second)
	if len(c.prompts) != before {
		t.Fatal("summary rebuilt without a change")
	}
}

func TestFailed(t *testing.T) {
	for out, want := range map[string]bool{
		"ok\n":                     false,
		"FAIL\nexit status 1":      true,
		"killed\nsignal: killed\n": true,
		"exit status 1 was printed earlier\nall good": false,
	} {
		if got := learn.Failed(false, out); got != want {
			t.Errorf("%q: got %v", out, got)
		}
	}
	if !learn.Failed(true, "") {
		t.Error("tool error not a failure")
	}
}
