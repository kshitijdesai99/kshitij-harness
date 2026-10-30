package test

import (
	"context"
	"encoding/json"
	"kh/internal/agent"
	"kh/internal/provider"
	"kh/internal/tools"
	"reflect"
	"testing"
)

func TestCommandExpansionRunsAfterHookForEveryUserMessage(t *testing.T) {
	input := make(chan string, 1)
	var expanded, hooks []string
	f := &fake{step: func(ctx context.Context, n int, results []provider.Result) ([]provider.Call, error) {
		switch n {
		case 1:
			input <- "/steered"
			<-ctx.Done()
			return nil, ctx.Err()
		case 2:
			return []provider.Call{{ID: "one", Name: "noop", Input: json.RawMessage(`{}`)}}, nil
		case 3:
			return []provider.Call{{ID: "two", Name: "noop", Input: json.RawMessage(`{}`)}}, nil
		}
		return nil, nil
	}}
	held := 0
	r := agent.Runner{Model: f, Input: input, Tools: []tools.Tool{{Name: "noop", Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}},
		Held: func() []string {
			held++
			if held == 1 {
				return []string{"/queued"}
			}
			return nil
		},
		BeforeMessage: func(_ context.Context, s string) error { hooks = append(hooks, s); return nil },
		ExpandMessage: func(s string) string { expanded = append(expanded, s); return s + " expanded" },
	}
	if err := r.Run(context.Background(), "/initial"); err != nil {
		t.Fatal(err)
	}
	want := []string{"/initial", "/steered", "/queued"}
	if !reflect.DeepEqual(expanded, want) || !reflect.DeepEqual(hooks, want) {
		t.Fatalf("expanded=%q hooks=%q", expanded, hooks)
	}
	if !reflect.DeepEqual(f.got, []string{"/initial expanded", "/steered expanded", "/queued expanded", ""}) {
		t.Fatal(f.got)
	}
}
