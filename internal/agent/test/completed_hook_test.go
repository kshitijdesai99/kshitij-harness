package test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"kh/internal/agent"
	"kh/internal/provider"
	"kh/internal/tools"
)

func TestCompletedContinuationBoundedWithTools(t *testing.T) {
	var hooks []bool
	var users []string
	f := &fake{step: func(ctx context.Context, n int, results []provider.Result) ([]provider.Call, error) {
		if n == 2 {
			return []provider.Call{{ID: "one", Name: "noop", Input: json.RawMessage(`{}`)}}, nil
		}
		if n == 3 && (len(results) != 1 || results[0].ID != "one") {
			t.Fatalf("unpaired continuation results: %+v", results)
		}
		return nil, nil
	}}
	r := agent.Runner{Model: f, Tools: []tools.Tool{noop}, BeforeMessage: func(_ context.Context, s string) error { users = append(users, s); return nil }, Completed: func(_ context.Context, active bool) (bool, error) { hooks = append(hooks, active); return true, nil }}
	if err := r.Run(context.Background(), "actual user"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hooks, []bool{false, true}) || !reflect.DeepEqual(users, []string{"actual user"}) || !reflect.DeepEqual(f.got, []string{"actual user", "", ""}) {
		t.Fatalf("hooks=%v users=%v steps=%v", hooks, users, f.got)
	}
}

func TestCompletedContinuationTimeout(t *testing.T) {
	completions := 0
	f := &fake{step: func(ctx context.Context, n int, _ []provider.Result) ([]provider.Call, error) {
		if n == 1 {
			return nil, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	r := agent.Runner{Model: f, ContinuationTimeout: 10 * time.Millisecond, Completed: func(context.Context, bool) (bool, error) { completions++; return true, nil }}
	if err := r.Run(context.Background(), "actual"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout=%v", err)
	}
	if completions != 1 {
		t.Fatalf("completed timed-out reply %d times", completions)
	}
}

func TestCompletedSteeringRestoresUserContext(t *testing.T) {
	input := make(chan string, 1)
	var activeFlags []bool
	f := &fake{step: func(ctx context.Context, n int, _ []provider.Result) ([]provider.Call, error) {
		if n == 2 {
			input <- "new actual user"
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return nil, nil
	}}
	r := agent.Runner{Model: f, Input: input, ContinuationTimeout: time.Second, Completed: func(_ context.Context, active bool) (bool, error) {
		activeFlags = append(activeFlags, active)
		return !active, nil
	}}
	if err := r.Run(context.Background(), "first user"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(activeFlags, []bool{false, false, true}) {
		t.Fatalf("new user remained continuation: %v", activeFlags)
	}
	if !reflect.DeepEqual(f.got, []string{"first user", "", "new actual user", ""}) {
		t.Fatalf("messages=%v", f.got)
	}
}

func TestCompletedNotCalledOnFailureOrCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{true: "cancel", false: "error"}[cancelled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &fake{step: func(context.Context, int, []provider.Result) ([]provider.Call, error) {
				if cancelled {
					cancel()
					return nil, nil
				}
				return nil, errors.New("model failed")
			}}
			r := agent.Runner{Model: f, Completed: func(context.Context, bool) (bool, error) { t.Error("completed failed reply"); return false, nil }}
			if err := r.Run(ctx, "hello"); err == nil {
				t.Fatal("expected failure")
			}
		})
	}
}

func TestCompletedContinuationCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fake{step: func(context.Context, int, []provider.Result) ([]provider.Call, error) { return nil, nil }}
	r := agent.Runner{Model: f, Completed: func(context.Context, bool) (bool, error) { cancel(); return true, nil }}
	if err := r.Run(ctx, "hello"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if len(f.got) != 1 {
		t.Fatalf("cancelled continuation executed: %v", f.got)
	}
}
