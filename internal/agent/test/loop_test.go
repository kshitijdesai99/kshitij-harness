package test

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"kh/internal/agent"
	"kh/internal/provider"
	"kh/internal/tools"
)

// fake asks for one tool call, then records what it was sent next.
type fake struct{ got []string }

func (f *fake) Step(_ context.Context, user string, results []provider.Result) ([]provider.Call, error) {
	f.got = append(f.got, user)
	if results == nil {
		return []provider.Call{{ID: "1", Name: "noop", Input: json.RawMessage("{}")}}, nil
	}
	return nil, nil
}
func (f *fake) Save() ([]byte, error)            { return nil, nil }
func (f *fake) Load([]byte) error                { return nil }
func (f *fake) Replay(io.Writer)                 {}
func (f *fake) Stats() provider.Stats            { return provider.Stats{} }
func (f *fake) Use(m, e string) (string, string) { return m, e }

// A line typed while the task runs reaches the model with the next tool results.
func TestSteerMidTask(t *testing.T) {
	noop := tools.Tool{Name: "noop", Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}
	steer := make(chan string, 2)
	steer <- "skip the slow tests"
	steer <- "and be quick"

	f := &fake{}
	if err := agent.Run(context.Background(), f, []tools.Tool{noop}, "fix tests", steer); err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 2 || f.got[0] != "fix tests" || f.got[1] != "skip the slow tests\nand be quick" {
		t.Errorf("model was sent %q", f.got)
	}
}
