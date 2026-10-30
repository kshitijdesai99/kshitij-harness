// Package mcp_test checks cancellation while an HTTP response body is incomplete.
package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestHTTPJSONBodyCancellationPreservesContextError(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			f := newHTTPFixture(t, false, func(w http.ResponseWriter, r *http.Request, msg httpRPC) bool {
				if msg.Method != "tools/call" {
					return false
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"jsonrpc":"2.0","result":`)
				w.(http.Flusher).Flush()
				close(started)
				<-r.Context().Done()
				return true
			})
			ts, cleanup := connectHTTP(t, context.Background(), f)
			defer cleanup()
			ctx, cancel := context.WithCancel(context.Background())
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
			}
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := ts[0].Run(ctx, json.RawMessage(`{}`))
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("response body never started")
			}
			want := context.DeadlineExceeded
			if mode == "cancel" {
				// Let the client consume the flushed headers and wait for more body.
				time.Sleep(20 * time.Millisecond)
				cancel()
				want = context.Canceled
			}
			select {
			case err := <-done:
				if !errors.Is(err, want) {
					t.Fatalf("body cancellation error = %v, want %v", err, want)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("body read did not cancel")
			}
		})
	}
}
