// Package agent is the loop: ask the model, run its tools, repeat.
package agent

import (
	"context"
	"sync"

	"kh/internal/provider"
	"kh/internal/tools"
)

func Run(ctx context.Context, p provider.Provider, ts []tools.Tool, task string) error {
	calls, err := p.Step(ctx, task, nil)
	for err == nil && len(calls) > 0 {
		calls, err = p.Step(ctx, "", runAll(ctx, ts, calls))
	}
	return err
}

// runAll runs every call at once; results keep the model's order.
func runAll(ctx context.Context, ts []tools.Tool, calls []provider.Call) []provider.Result {
	byName := map[string]tools.Tool{}
	for _, t := range ts {
		byName[t.Name] = t
	}
	out := make([]provider.Result, len(calls))
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = provider.Result{ID: c.ID, Output: "unknown tool: " + c.Name, IsError: true}
			if t, ok := byName[c.Name]; ok {
				s, err := t.Run(ctx, c.Input)
				if err != nil {
					s = err.Error()
				}
				out[i] = provider.Result{ID: c.ID, Output: s, IsError: err != nil}
			}
		}()
	}
	wg.Wait()
	return out
}
