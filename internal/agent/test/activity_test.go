package test

import (
	"context"
	"encoding/json"
	"errors"
	"kh/internal/agent"
	"kh/internal/provider"
	"kh/internal/tools"
	"reflect"
	"testing"
	"time"
)

func TestRunnerActivityPhases(t *testing.T) {
	var phases []string
	f := &fake{step: func(_ context.Context, n int, _ []provider.Result) ([]provider.Call, error) {
		if n == 1 {
			return []provider.Call{{ID: "1", Name: "noop", Input: json.RawMessage("{}")}}, nil
		}
		return nil, nil
	}}
	r := agent.Runner{Model: f, Tools: []tools.Tool{noop}, Activity: func(s string) { phases = append(phases, s) }}
	if err := r.Run(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	want := []string{"waiting for model", "running noop", "waiting for model"}
	if !reflect.DeepEqual(phases, want) {
		t.Fatalf("phases=%q", phases)
	}
}
func TestRunnerActivityCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	f := &fake{step: func(ctx context.Context, _ int, _ []provider.Result) ([]provider.Call, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	done := make(chan error, 1)
	go func() { done <- (agent.Runner{Model: f, Activity: func(string) {}}).Run(ctx, "test") }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation hung")
	}
}
