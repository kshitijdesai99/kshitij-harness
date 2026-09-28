// Package agent is the loop: ask the model, run its tools, repeat.
package agent

import (
	"context"
	"strings"
	"sync"

	"kh/internal/provider"
	"kh/internal/tools"
)

// Run does one task. Lines arriving on steer while it works are sent with the
// next tool results, so the user can redirect it without stopping it.
func Run(ctx context.Context, p provider.Provider, ts []tools.Tool, task string, steer <-chan string) error {
	calls, err := p.Step(ctx, task, nil)
	for err == nil && len(calls) > 0 {
		results := runAll(ctx, ts, calls)
		calls, err = p.Step(ctx, queued(tools.Held(), steer), results)
	}
	return err
}

// queued takes whatever was typed so far, without waiting.
func queued(msgs []string, steer <-chan string) string {
	for {
		select {
		case l, ok := <-steer:
			if !ok {
				return strings.Join(msgs, "\n")
			}
			if l != "" {
				msgs = append(msgs, l)
			}
		default:
			return strings.Join(msgs, "\n")
		}
	}
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
