package test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"kh/internal/terminal"
)

func TestConsolesHaveIndependentInput(t *testing.T) {
	a := terminal.NewConsole(strings.NewReader("one\ntwo"), io.Discard, io.Discard)
	b := terminal.NewConsole(strings.NewReader("other\n"), io.Discard, io.Discard)
	defer a.Close()
	defer b.Close()
	a.Start()
	b.Start()
	collect := func(c *terminal.Console) []string {
		var lines []string
		for line := range c.Lines {
			lines = append(lines, line)
		}
		return lines
	}
	if got := collect(a); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatal(got)
	}
	if got := collect(b); !reflect.DeepEqual(got, []string{"other"}) {
		t.Fatal(got)
	}
}

func TestApprovalKeepsSteering(t *testing.T) {
	c := terminal.NewConsole(strings.NewReader("change direction\nyes\n"), io.Discard, io.Discard)
	defer c.Close()
	c.Start()
	ok, err := c.Approve(context.Background(), "command", false)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got := c.Held(); !reflect.DeepEqual(got, []string{"change direction"}) {
		t.Fatal(got)
	}
	if len(c.Held()) != 0 {
		t.Fatal("steering was not consumed")
	}
}

type promptSignal struct {
	ready chan struct{}
	once  sync.Once
}

func (w *promptSignal) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "run it?") {
		w.once.Do(func() { close(w.ready) })
	}
	return len(p), nil
}

func TestApprovalAndQueuedPromptAreCancellable(t *testing.T) {
	in, writer := io.Pipe()
	defer in.Close()
	defer writer.Close()
	prompt := &promptSignal{ready: make(chan struct{})}
	c := terminal.NewConsole(in, io.Discard, prompt)
	defer c.Close()
	c.Start()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := c.Approve(ctx, "first", false); first <- err }()
	select {
	case <-prompt.ready:
	case <-time.After(time.Second):
		t.Fatal("no prompt")
	}
	queued, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	ok, err := c.Approve(queued, "second", false)
	if ok || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued ok=%v err=%v", ok, err)
	}
	cancel()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("approval ignored cancellation")
	}
}

func TestRendererUsesInjectedWriters(t *testing.T) {
	var out, err strings.Builder
	r := terminal.Renderer{Out: &out, Err: &err}
	r.Query("question")
	r.Reply("answer")
	r.Reply("\n")
	r.Action("$ command")
	r.Notice("notice")
	if got := out.String(); got != "> question\nanswer\n" {
		t.Fatalf("out=%q", got)
	}
	if got := err.String(); got != "$ command\nnotice\n" {
		t.Fatalf("err=%q", got)
	}
}
