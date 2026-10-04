package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"kh/internal/provider"
)

var errModelIdle = errors.New("codex: connection silent past model idle timeout")

// streamDisconnect distinguishes a recoverable transport failure from a bad
// response. Never replay once text or a server-side action has been displayed.
type streamDisconnect struct {
	err       error
	displayed bool
}

func (e *streamDisconnect) Error() string { return e.err.Error() }
func (e *streamDisconnect) Unwrap() error { return e.err }

func (c *Client) requestWithRetry(ctx context.Context, body []byte, access, account string, historyLen int) ([]provider.Call, error) {
	var recoveryDeadline time.Time
	stats := c.stats
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.input = c.input[:historyLen]
		c.stats = stats
		attemptCtx := ctx
		stopDeadline := func() {}
		if !recoveryDeadline.IsZero() {
			attemptCtx, stopDeadline = context.WithDeadline(ctx, recoveryDeadline)
		}
		requestCtx, watch := newIdleWatch(attemptCtx, c.idleTimeout)
		req, err := http.NewRequestWithContext(requestCtx, "POST", codexURL, bytes.NewReader(body))
		if err != nil {
			watch.stop()
			stopDeadline()
			return nil, err
		}
		setHeaders(req, access, account)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("session_id", c.session)
		resp, err := http.DefaultClient.Do(req)
		retry := err != nil
		delay := retryEvery
		var calls []provider.Call
		if err == nil {
			watch.touch()
			// Cancellation must interrupt a blocked body read as well as Do.
			stopClose := context.AfterFunc(requestCtx, func() { resp.Body.Close() })
			reader := &idleReader{Reader: resp.Body, watch: watch}
			if resp.StatusCode == http.StatusOK {
				calls, err = c.readStream(reader)
				var dropped *streamDisconnect
				retry = errors.As(err, &dropped) && !dropped.displayed
			} else {
				switch resp.StatusCode {
				case 408, 429, 500, 502, 503, 504:
					retry = true
					if recoveryDeadline.IsZero() {
						recoveryDeadline = time.Now().Add(retryFor)
					}
				}
				// Status is already known: do not let a trickling diagnostic
				// body postpone recovery indefinitely, even if it stays active.
				diagnosticLimit := min(5*time.Second, c.idleTimeout)
				if !recoveryDeadline.IsZero() {
					diagnosticLimit = min(diagnosticLimit, time.Until(recoveryDeadline))
				}
				diagnosticCtx, stopDiagnostic := context.WithTimeout(requestCtx, diagnosticLimit)
				stopDiagnosticClose := context.AfterFunc(diagnosticCtx, func() { resp.Body.Close() })
				text, readErr := io.ReadAll(io.LimitReader(reader, 4<<10))
				stopDiagnosticClose()
				stopDiagnostic()
				err = fmt.Errorf("codex: status %d: %s", resp.StatusCode, text)
				if readErr != nil {
					err = fmt.Errorf("%w: %v", err, readErr)
				}
				if seconds, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && seconds > 0 {
					delay = max(delay, time.Duration(seconds)*time.Second)
				} else if when, e := http.ParseTime(resp.Header.Get("Retry-After")); e == nil {
					delay = max(delay, time.Until(when))
				}
			}
			stopClose()
			resp.Body.Close()
		}
		cause := context.Cause(requestCtx)
		watch.stop()
		stopDeadline()
		if ctx.Err() != nil {
			c.stats = stats
			return nil, ctx.Err()
		}
		if err == nil {
			return calls, nil
		}
		if cause != nil {
			err = cause
		}
		c.input = c.input[:historyLen]
		c.stats = stats
		if !retry {
			return nil, err
		}
		if recoveryDeadline.IsZero() {
			recoveryDeadline = time.Now().Add(retryFor)
		}
		remaining := time.Until(recoveryDeadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("codex: connection recovery exhausted after %s: %w", retryFor, err)
		}
		c.out.Notice(fmt.Sprintf("(model connection interrupted: %v; retry %d in %s, Ctrl-C to stop)", err, attempt, min(delay, remaining).Round(time.Millisecond)))
		timer := time.NewTimer(min(delay, remaining))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		if !time.Now().Before(recoveryDeadline) {
			return nil, fmt.Errorf("codex: connection recovery exhausted after %s: %w", retryFor, err)
		}
	}
}

// A silence deadline, not a total request deadline: streamed bytes extend it,
// allowing long reasoning while bounding both headers and stalled SSE bodies.
type idleWatch struct {
	mu      sync.Mutex
	last    time.Time
	idle    time.Duration
	timer   *time.Timer
	cancel  context.CancelCauseFunc
	stopped bool
}

func newIdleWatch(parent context.Context, idle time.Duration) (context.Context, *idleWatch) {
	ctx, cancel := context.WithCancelCause(parent)
	w := &idleWatch{last: time.Now(), idle: idle, cancel: cancel}
	w.timer = time.AfterFunc(idle, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.stopped {
			return
		}
		if left := w.idle - time.Since(w.last); left > 0 {
			w.timer.Reset(left)
			return
		}
		w.cancel(errModelIdle)
	})
	return ctx, w
}
func (w *idleWatch) touch() { w.mu.Lock(); w.last = time.Now(); w.mu.Unlock() }
func (w *idleWatch) stop() {
	w.mu.Lock()
	w.stopped = true
	w.timer.Stop()
	w.mu.Unlock()
	w.cancel(context.Canceled)
}

type idleReader struct {
	io.Reader
	watch *idleWatch
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.watch.touch()
	}
	return n, err
}
