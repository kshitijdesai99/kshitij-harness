package main

import (
	"context"
	"sync"
	"time"

	"kh/internal/terminal"
)

// Capture before any model/tool can spawn children. A new process intentionally
// skips old publications, including results from a resumed conversation.
func (e *ecosystemRuntime) captureResultWatermark(ctx context.Context) error {
	poll, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	seq, err := e.store.ResultWatermark(poll, e.self.ID)
	if err == nil {
		e.resultSeq = seq
	}
	return err
}

// The observer belongs to the console lifetime, never a cancellable model turn.
// Registry failures retain the cursor and retry quietly on the next tick.
func (e *ecosystemRuntime) startResults(ctx context.Context, ui terminal.Renderer) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				poll, stop := context.WithTimeout(ctx, 750*time.Millisecond)
				results, err := e.store.DisplayResultsAfter(poll, e.self.ID, e.resultSeq, 32)
				stop()
				if err != nil {
					continue
				}
				for _, result := range results {
					if ctx.Err() != nil {
						return
					}
					if result.Seq <= e.resultSeq {
						continue
					}
					ui.AgentTaskResult(result.Name, result.TaskID, result.Task, result.Conclusion, result.Truncated)
					e.resultSeq = result.Seq
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { cancel(); <-done }) }
}
