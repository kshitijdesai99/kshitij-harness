package test

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"kh/internal/agent"
	"kh/internal/provider"
	"kh/internal/tools"
)

// fake records what it is sent. step decides each reply.
type fake struct {
	got  []string
	step func(ctx context.Context, n int, results []provider.Result) ([]provider.Call, error)
}

func (f *fake) Step(ctx context.Context, user string, results []provider.Result) ([]provider.Call, error) {
	f.got = append(f.got, user)
	return f.step(ctx, len(f.got), results)
}
func (f *fake) Save() ([]byte, error)            { return nil, nil }
func (f *fake) Load([]byte) error                { return nil }
func (f *fake) Replay(io.Writer)                 {}
func (f *fake) Stats() provider.Stats            { return provider.Stats{} }
func (f *fake) Use(m, e string) (string, string) { return m, e }

var noop = tools.Tool{Name: "noop", Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}

// Typing while the model replies cuts the reply off and restarts with the line.
func TestSteerInterruptsReply(t *testing.T) {
	steer := make(chan string, 1)
	f := &fake{step: func(ctx context.Context, n int, _ []provider.Result) ([]provider.Call, error) {
		if n == 1 { // a slow reply, until cancelled
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return nil, nil
	}}
	go func() { time.Sleep(50 * time.Millisecond); steer <- "no, do it in Go" }()

	if err := agent.Run(context.Background(), f, nil, "write a script", steer); err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 2 || f.got[1] != "no, do it in Go" {
		t.Errorf("model was sent %q", f.got)
	}
}

// Typing while commands run is sent along with their results.
func TestSteerDuringCommand(t *testing.T) {
	steer := make(chan string, 2)
	typing := noop
	typing.Run = func(context.Context, json.RawMessage) (string, error) {
		steer <- "skip the slow tests"
		steer <- "and be quick"
		return "ok", nil
	}
	f := &fake{step: func(_ context.Context, n int, _ []provider.Result) ([]provider.Call, error) {
		if n == 1 {
			return []provider.Call{{ID: "1", Name: "noop", Input: json.RawMessage("{}")}}, nil
		}
		return nil, nil
	}}
	if err := agent.Run(context.Background(), f, []tools.Tool{typing}, "fix tests", steer); err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 2 || f.got[1] != "skip the slow tests\nand be quick" {
		t.Errorf("model was sent %q", f.got)
	}
}
