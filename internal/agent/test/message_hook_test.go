package test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"kh/internal/agent"
	"kh/internal/image"
	"kh/internal/provider"
	"kh/internal/tools"
)

func TestBeforeMessageOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	input := make(chan string, 1)
	var events []string
	f := &fake{step: func(ctx context.Context, n int, results []provider.Result) ([]provider.Call, error) {
		switch n {
		case 1:
			events = append(events, "step:initial")
			input <- "steered"
			<-ctx.Done()
			events = append(events, "cancelled")
			return nil, ctx.Err()
		case 2:
			events = append(events, "step:steered")
		case 3, 4:
			text := ""
			if n == 3 {
				text = "held\nqueued"
			}
			events = append(events, "step:"+text)
			if len(results) != 1 || results[0].ID != "call" || results[0].Output != "ok" {
				t.Errorf("step %d results = %+v", n, results)
			}
		}
		if n == 4 {
			return nil, nil
		}
		return []provider.Call{{ID: "call", Name: "noop", Input: json.RawMessage("{}")}}, nil
	}}
	tool := tools.Tool{Name: "noop", Run: func(context.Context, json.RawMessage) (string, error) {
		events = append(events, "tool")
		return "ok", nil
	}}
	heldCalls := 0
	r := agent.Runner{
		Model: f, Input: input, Tools: []tools.Tool{tool},
		Held: func() []string {
			heldCalls++
			if heldCalls == 1 {
				input <- "queued"
				return []string{"held"}
			}
			return nil
		},
		BeforeMessage: func(got context.Context, text string) error {
			if got != ctx {
				t.Error("hook did not receive caller context")
			}
			events = append(events, "hook:"+text)
			return nil
		},
	}
	if err := r.Run(ctx, "initial"); err != nil {
		t.Fatal(err)
	}
	want := []string{"hook:initial", "step:initial", "cancelled", "hook:steered", "step:steered", "tool", "hook:held\nqueued", "step:held\nqueued", "tool", "step:"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %q, want %q", events, want)
	}
	if !reflect.DeepEqual(f.got, []string{"initial", "steered", "held\nqueued", ""}) {
		t.Fatalf("messages = %q", f.got)
	}
}

func TestBeforeMessageError(t *testing.T) {
	for _, stop := range []string{"initial", "steered", "queued"} {
		t.Run(stop, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			input := make(chan string, 1)
			f := &fake{step: func(ctx context.Context, _ int, results []provider.Result) ([]provider.Call, error) {
				if ctx.Err() != nil {
					if stop == "queued" && (len(results) != 1 || results[0].ID != "1") {
						t.Errorf("hook failure lost completed results: %+v", results)
					}
					return nil, ctx.Err()
				}
				if stop == "steered" {
					input <- "steered"
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return []provider.Call{{ID: "1", Name: "noop"}}, nil
			}}
			failure := errors.New("hook failed")
			r := agent.Runner{Model: f, Input: input, Tools: []tools.Tool{noop}, Held: func() []string { return []string{"queued"} }, BeforeMessage: func(_ context.Context, text string) error {
				if text == stop {
					return failure
				}
				return nil
			}}
			if err := r.Run(ctx, "initial"); !errors.Is(err, failure) {
				t.Fatalf("error = %v", err)
			}
			want := []string{"initial"}
			if stop == "initial" {
				want = nil
			}
			if stop == "queued" {
				want = []string{"initial", ""}
			}
			if !reflect.DeepEqual(f.got, want) {
				t.Fatalf("provider messages = %q, want %q", f.got, want)
			}
		})
	}
}

func TestBeforeMessageSkipped(t *testing.T) {
	for _, name := range []string{"cancelled", "invalid image", "empty"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			text := "initial"
			switch name {
			case "cancelled":
				cancel()
			case "invalid image":
				text = image.PasteMarker
			case "empty":
				text = ""
			}
			f := &fake{step: func(context.Context, int, []provider.Result) ([]provider.Call, error) { return nil, nil }}
			r := agent.Runner{Model: f, BeforeMessage: func(context.Context, string) error {
				t.Error("unexpected hook")
				return nil
			}}
			err := r.Run(ctx, text)
			if name == "empty" {
				if err != nil || len(f.got) != 1 {
					t.Fatalf("empty step: error=%v, messages=%q", err, f.got)
				}
			} else if err == nil || len(f.got) != 0 {
				t.Fatalf("error=%v, messages=%q", err, f.got)
			}
			if name == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestBeforeMessageCancelledAfterTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []string
	f := &fake{step: func(ctx context.Context, n int, results []provider.Result) ([]provider.Call, error) {
		if n == 2 {
			if ctx.Err() == nil || len(results) != 1 || results[0].ID != "1" || results[0].Output != "ok" {
				t.Errorf("cancelled flush lost results: %+v ctx=%v", results, ctx.Err())
			}
			return nil, ctx.Err()
		}
		return []provider.Call{{ID: "1", Name: "cancel"}}, nil
	}}
	r := agent.Runner{
		Model: f,
		Tools: []tools.Tool{{Name: "cancel", Run: func(context.Context, json.RawMessage) (string, error) {
			cancel()
			events = append(events, "tool finished")
			return "ok", nil
		}}},
		Observe: func(_ []provider.Call, results []provider.Result) {
			if len(results) != 1 || results[0].Output != "ok" {
				t.Errorf("results = %+v", results)
			}
			events = append(events, "observed")
		},
		Held: func() []string { return []string{"queued"} },
		BeforeMessage: func(_ context.Context, text string) error {
			events = append(events, "hook:"+text)
			return nil
		},
	}
	if err := r.Run(ctx, "initial"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if !reflect.DeepEqual(events, []string{"hook:initial", "tool finished", "observed"}) || !reflect.DeepEqual(f.got, []string{"initial", ""}) {
		t.Fatalf("events = %q, provider messages = %q", events, f.got)
	}
}

type hookAttachments struct {
	*fake
	image, memory string
}

func (f *hookAttachments) AttachImage(text string) { f.image = text }
func (f *hookAttachments) SetMemory(text string)   { f.memory = text }

func TestBeforeMessageAbandonsStagedInput(t *testing.T) {
	for _, cancelHook := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelHook), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &hookAttachments{fake: &fake{}, image: "data:image/png;base64,staged"}
			failure := errors.New("retrieval failed")
			r := agent.Runner{Model: f, BeforeMessage: func(context.Context, string) error {
				f.SetMemory("memory for abandoned query")
				if cancelHook {
					cancel()
					return nil
				}
				return failure
			}}
			if err := r.Run(ctx, "question"); err == nil || len(f.got) != 0 || f.image != "" || f.memory != "" {
				t.Fatalf("staged input leaked: image=%q memory=%q calls=%v error=%v", f.image, f.memory, f.got, err)
			}
		})
	}
}

func TestBeforeMessageCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fake{}
	r := agent.Runner{Model: f, BeforeMessage: func(got context.Context, _ string) error {
		if got != ctx {
			t.Error("hook did not receive caller context")
		}
		cancel()
		return nil
	}}
	if err := r.Run(ctx, "initial"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if len(f.got) != 0 {
		t.Fatalf("provider called after cancellation: %q", f.got)
	}
}
