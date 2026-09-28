// Package agent is the loop: ask the model, run its tools, repeat.
package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"kh/internal/provider"
	"kh/internal/tools"
)

// Run does one task, and lets the user steer it by typing a line on steer:
//   - while the model replies, the reply is cut off and restarted with the line;
//   - while commands run, the line is sent with their results (cutting a
//     command off halfway could leave things broken).
func Run(ctx context.Context, p provider.Provider, ts []tools.Tool, task string, steer <-chan string) error {
	user, results := task, []provider.Result(nil)
	for {
		calls, line, err := step(ctx, p, user, results, steer)
		if err != nil {
			return err
		}
		if line != "" { // steered: the cut-off reply was dropped, go again with the line
			user, results = line, nil
			continue
		}
		if len(calls) == 0 {
			return nil
		}
		results = runAll(ctx, ts, calls)
		user = queued(tools.Held(), steer)
	}
}

// step runs one model reply, but gives up on it as soon as the user types a
// line, and returns that line instead.
func step(ctx context.Context, p provider.Provider, user string, results []provider.Result, steer <-chan string) ([]provider.Call, string, error) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type reply struct {
		calls []provider.Call
		err   error
	}
	done := make(chan reply, 1)
	go func() {
		calls, err := p.Step(sctx, user, results)
		done <- reply{calls, err}
	}()
	for {
		select {
		case r := <-done:
			return r.calls, "", r.err
		case line, ok := <-steer:
			if !ok { // Ctrl-D: stop listening, let the reply finish
				steer = nil
				continue
			}
			if line == "" {
				continue
			}
			cancel()
			<-done // the provider drops its partial reply on cancel
			fmt.Fprintln(os.Stderr, "\n(steered)")
			return nil, line, nil
		}
	}
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
