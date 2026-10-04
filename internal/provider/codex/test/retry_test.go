package test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"kh/internal/config"
	"kh/internal/provider/codex"
)

// Retry tests use the public adapter and the same isolated credentials as the
// stream tests. Keep these serial: mockCodex replaces http.DefaultClient.
func retryResponse(status int, text string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(text))}
}

func retryContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type retryOutput struct {
	replies strings.Builder
	actions []string
	notices []string
}

func (o *retryOutput) Reply(s string)  { o.replies.WriteString(s) }
func (o *retryOutput) Query(string)    {}
func (o *retryOutput) Action(s string) { o.actions = append(o.actions, s) }
func (o *retryOutput) Notice(s string) { o.notices = append(o.notices, s) }

func TestRetryTransientHTTPStatuses(t *testing.T) {
	for _, status := range []int{408, 429, 500, 502, 503, 504} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			p := mockCodex(t, "")
			attempts := 0
			var firstBody []byte
			http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					return nil, err
				}
				attempts++
				if attempts == 1 {
					firstBody = body
					return retryResponse(status, "temporary failure"), nil
				}
				if !bytes.Equal(firstBody, body) {
					t.Errorf("retry changed request: first=%s next=%s", firstBody, body)
				}
				return retryResponse(200, toolItem+completeEvent), nil
			})
			calls, err := p.Step(retryContext(t), "question", nil)
			if err != nil || attempts != 2 || len(calls) != 1 {
				t.Fatalf("attempts=%d calls=%v err=%v", attempts, calls, err)
			}
		})
	}
}

func TestRetryTransportAndUndisplayedStreamFailures(t *testing.T) {
	for _, failure := range []string{"transport", "empty EOF", "partial tool EOF", "network read"} {
		t.Run(failure, func(t *testing.T) {
			mockCodex(t, "")
			out := &retryOutput{}
			p := codex.New(config.Defaults, "", nil, out)
			attempts := 0
			http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				attempts++
				if attempts == 1 {
					switch failure {
					case "transport":
						return nil, io.ErrUnexpectedEOF
					case "empty EOF":
						return retryResponse(200, ""), nil
					case "partial tool EOF":
						return retryResponse(200, toolItem), nil
					case "network read":
						resp := retryResponse(200, "")
						resp.Body = io.NopCloser(io.MultiReader(strings.NewReader(toolItem), retryNetworkReader{}))
						return resp, nil
					}
				}
				return retryResponse(200, summaryStream("recovered")+toolItem), nil
			})
			// The successful response completes before the trailing item. This
			// also checks that calls from the abandoned reply are not returned.
			calls, err := p.Step(retryContext(t), "question", nil)
			if err != nil || attempts != 2 || len(calls) != 0 {
				t.Fatalf("attempts=%d calls=%v err=%v", attempts, calls, err)
			}
			if got := out.replies.String(); got != "recovered\n" {
				t.Fatalf("abandoned stream displayed output: %q", got)
			}
			state, err := p.Save()
			if err != nil || bytes.Contains(state, []byte("call-1")) {
				t.Fatalf("abandoned call retained: state=%s err=%v", state, err)
			}
		})
	}
}

type retryNetworkReader struct{}

func (retryNetworkReader) Read([]byte) (int, error) { return 0, retryNetworkError{} }

type retryNetworkError struct{}

func (retryNetworkError) Error() string   { return "connection reset by peer" }
func (retryNetworkError) Timeout() bool   { return false }
func (retryNetworkError) Temporary() bool { return true }

func TestRetryStopsForPermanentAndProtocolFailures(t *testing.T) {
	for name, response := range map[string]struct {
		status int
		stream string
	}{
		"bad request":         {400, "invalid request"},
		"unauthorized":        {401, "unauthorized"},
		"forbidden":           {403, "forbidden"},
		"malformed event":     {200, "data: {broken\n\n"},
		"failed response":     {200, "data: {\"type\":\"response.failed\"}\n\n"},
		"incomplete response": {200, "data: {\"type\":\"response.incomplete\"}\n\n"},
		"explicit error":      {200, "data: {\"type\":\"error\",\"message\":\"bad request\"}\n\n"},
		"invalid tool":        {200, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\"}}\n\n"},
	} {
		t.Run(name, func(t *testing.T) {
			p := mockCodex(t, "")
			attempts := 0
			http.DefaultClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
				attempts++
				return retryResponse(response.status, response.stream), nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			_, err := p.Step(ctx, "question", nil)
			if err == nil || attempts != 1 || ctx.Err() != nil {
				t.Fatalf("attempts=%d ctx=%v err=%v", attempts, ctx.Err(), err)
			}
		})
	}
}

func TestRetryDoesNotRepeatDisplayedReplyOrAction(t *testing.T) {
	for name, stream := range map[string]string{
		"reply":  "data: {\"type\":\"response.output_text.delta\",\"delta\":\"visible\"}\n\n",
		"action": "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"web_search_call\",\"action\":{\"type\":\"search\",\"query\":\"visible\"}}}\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			mockCodex(t, "")
			out := &retryOutput{}
			p := codex.New(config.Defaults, "", nil, out)
			attempts := 0
			http.DefaultClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
				attempts++
				return retryResponse(200, stream), nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			_, err := p.Step(ctx, "question", nil)
			if err == nil || attempts != 1 || ctx.Err() != nil {
				t.Fatalf("attempts=%d ctx=%v err=%v", attempts, ctx.Err(), err)
			}
			if name == "reply" && out.replies.String() != "visible\n" {
				t.Fatalf("reply=%q", out.replies.String())
			}
			if name == "action" && len(out.actions) != 1 {
				t.Fatalf("actions=%v", out.actions)
			}
		})
	}
}

func TestRetryCompletedStreamMakesOneRequest(t *testing.T) {
	p := mockCodex(t, "")
	attempts := 0
	http.DefaultClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		attempts++
		return retryResponse(200, toolItem+completeEvent), nil
	})
	calls, err := p.Step(retryContext(t), "question", nil)
	if err != nil || attempts != 1 || len(calls) != 1 {
		t.Fatalf("attempts=%d calls=%v err=%v", attempts, calls, err)
	}
}

func TestRetryCancellationInterruptsWait(t *testing.T) {
	p := mockCodex(t, "")
	attempts := 0
	http.DefaultClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		attempts++
		return retryResponse(503, "unavailable"), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := p.Step(ctx, "question", nil)
	if !errors.Is(err, context.DeadlineExceeded) || attempts != 1 || time.Since(start) > time.Second {
		t.Fatalf("attempts=%d elapsed=%v err=%v", attempts, time.Since(start), err)
	}
}

// A blocked body observes the request context, like a real HTTP transport. The
// adapter must cancel that context when no bytes arrive before the idle limit.
type retryBlockedBody struct{ ctx context.Context }

func (b retryBlockedBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (retryBlockedBody) Close() error { return nil }

func TestRetryIdleTimeoutRecoversHeadersAndBody(t *testing.T) {
	for _, phase := range []string{"headers", "body"} {
		t.Run(phase, func(t *testing.T) {
			mockCodex(t, "")
			cfg := config.Defaults
			cfg.ModelIdleTimeoutSec = 1
			p := codex.New(cfg, "", nil, nil)
			attempts := 0
			http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				attempts++
				if attempts == 1 {
					if phase == "headers" {
						<-r.Context().Done()
						return nil, r.Context().Err()
					}
					resp := retryResponse(200, "")
					resp.Body = retryBlockedBody{r.Context()}
					return resp, nil
				}
				return retryResponse(200, completeEvent), nil
			})
			start := time.Now()
			_, err := p.Step(retryContext(t), "question", nil)
			if err != nil || attempts != 2 || time.Since(start) < time.Second {
				t.Fatalf("attempts=%d elapsed=%v err=%v", attempts, time.Since(start), err)
			}
		})
	}
}

func TestRetryIdleTimeoutResetsOnBytes(t *testing.T) {
	mockCodex(t, "")
	cfg := config.Defaults
	cfg.ModelIdleTimeoutSec = 1
	p := codex.New(cfg, "", nil, nil)
	attempts := 0
	http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		attempts++
		reader, writer := io.Pipe()
		go func() {
			<-r.Context().Done()
			reader.CloseWithError(r.Context().Err())
		}()
		go func() {
			defer writer.Close()
			// SSE comments count as transport activity even though they do not
			// produce model events. Total response time exceeds the idle limit.
			for _, chunk := range []string{": heartbeat\n\n", ": heartbeat\n\n", completeEvent} {
				timer := time.NewTimer(600 * time.Millisecond)
				select {
				case <-r.Context().Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				if _, err := io.WriteString(writer, chunk); err != nil {
					return
				}
			}
		}()
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: reader}, nil
	})
	_, err := p.Step(retryContext(t), "question", nil)
	if err != nil || attempts != 1 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestRetryIdleTimeoutAfterDisplayedReplyDoesNotReplay(t *testing.T) {
	mockCodex(t, "")
	cfg := config.Defaults
	cfg.ModelIdleTimeoutSec = 1
	out := &retryOutput{}
	p := codex.New(cfg, "", nil, out)
	attempts := 0
	http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		attempts++
		resp := retryResponse(200, "")
		resp.Body = io.NopCloser(io.MultiReader(
			strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"visible\"}\n\n"),
			retryBlockedBody{r.Context()},
		))
		return resp, nil
	})
	ctx := retryContext(t)
	_, err := p.Step(ctx, "question", nil)
	if err == nil || ctx.Err() != nil || attempts != 1 || out.replies.String() != "visible\n" {
		t.Fatalf("attempts=%d reply=%q ctx=%v err=%v", attempts, out.replies.String(), ctx.Err(), err)
	}
}

func TestRetryDoesNotRetryOversizedStreamEvent(t *testing.T) {
	p := mockCodex(t, "")
	attempts := 0
	http.DefaultClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		attempts++
		return retryResponse(200, "data: "+strings.Repeat("x", 16<<20)), nil
	})
	ctx := retryContext(t)
	_, err := p.Step(ctx, "question", nil)
	if err == nil || ctx.Err() != nil || attempts != 1 {
		t.Fatalf("attempts=%d ctx=%v err=%v", attempts, ctx.Err(), err)
	}
}

func TestRetryParentCancellationStopsBlockedBody(t *testing.T) {
	p := mockCodex(t, "")
	attempts := 0
	http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		attempts++
		resp := retryResponse(200, "")
		resp.Body = retryBlockedBody{r.Context()}
		return resp, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := p.Step(ctx, "question", nil)
	if !errors.Is(err, context.DeadlineExceeded) || attempts != 1 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestCancellationRollsBackPartialStreamHistory(t *testing.T) {
	p := mockCodex(t, "")
	attempts := 0
	http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		attempts++
		resp := retryResponse(200, "")
		partial := toolItem + `data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"uncommitted"}]}}` + "\n\n"
		resp.Body = io.NopCloser(io.MultiReader(strings.NewReader(partial), retryBlockedBody{r.Context()}))
		return resp, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	calls, err := p.Step(ctx, "question", nil)
	if !errors.Is(err, context.DeadlineExceeded) || attempts != 1 || len(calls) != 0 {
		t.Fatalf("attempts=%d calls=%v err=%v", attempts, calls, err)
	}
	saved, err := p.Save()
	if err != nil {
		t.Fatal(err)
	}
	var state struct{ Input []map[string]any }
	if err := json.Unmarshal(saved, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Input) != 1 || state.Input[0]["role"] != "user" {
		t.Fatalf("cancelled stream retained partial history: %s", saved)
	}
	if got := p.LastResponse(); got != "" {
		t.Fatalf("cancelled stream published final response: %q", got)
	}
}

// An HTTP error body can stay active indefinitely. Closing the diagnostic
// reader must stop a trickling writer even though no idle timeout has elapsed.
type retryDiagnosticBody struct {
	*io.PipeReader
	closed chan struct{}
	once   sync.Once
}

func (b *retryDiagnosticBody) Close() error {
	err := b.PipeReader.Close()
	b.once.Do(func() { close(b.closed) })
	return err
}

func tricklingDiagnosticBody(ctx context.Context) (*retryDiagnosticBody, <-chan struct{}) {
	reader, writer := io.Pipe()
	body := &retryDiagnosticBody{PipeReader: reader, closed: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer writer.Close()
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-body.closed:
				return
			case <-ticker.C:
				if _, err := io.WriteString(writer, "."); err != nil {
					return
				}
			}
		}
	}()
	return body, done
}

func TestRetryBoundsTricklingHTTPDiagnosticBodies(t *testing.T) {
	for _, status := range []int{503, 400} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			mockCodex(t, "")
			cfg := config.Defaults
			cfg.ModelIdleTimeoutSec = 1
			p := codex.New(cfg, "", nil, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			attempts := 0
			var diagnostic *retryDiagnosticBody
			var writerDone <-chan struct{}
			http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				attempts++
				if attempts != 1 {
					return retryResponse(200, completeEvent), nil
				}
				diagnostic, writerDone = tricklingDiagnosticBody(r.Context())
				resp := retryResponse(status, "")
				resp.Body = diagnostic
				return resp, nil
			})
			start := time.Now()
			_, err := p.Step(ctx, "question", nil)
			elapsed := time.Since(start)
			if ctx.Err() != nil {
				t.Fatalf("diagnostic read only stopped at parent deadline: attempts=%d elapsed=%v err=%v", attempts, elapsed, err)
			}
			if status == 503 {
				// One second diagnostic bound plus one second retry interval.
				if err != nil || attempts != 2 || elapsed < 1800*time.Millisecond || elapsed > 3500*time.Millisecond {
					t.Fatalf("transient attempts=%d elapsed=%v err=%v", attempts, elapsed, err)
				}
			} else if err == nil || attempts != 1 || elapsed < 900*time.Millisecond || elapsed > 2500*time.Millisecond {
				t.Fatalf("permanent attempts=%d elapsed=%v err=%v", attempts, elapsed, err)
			}
			select {
			case <-diagnostic.closed:
			default:
				t.Fatal("diagnostic body was not closed")
			}
			select {
			case <-writerDone:
			case <-time.After(time.Second):
				t.Fatal("trickling writer did not stop after body close")
			}
		})
	}
}

func TestRetryDoesNotDuplicateUserHistory(t *testing.T) {
	p := mockCodex(t, "")
	attempts := 0
	http.DefaultClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return retryResponse(502, "unavailable"), nil
		}
		return retryResponse(200, completeEvent), nil
	})
	if _, err := p.Step(retryContext(t), "one question", nil); err != nil {
		t.Fatal(err)
	}
	state, err := p.Save()
	if err != nil {
		t.Fatal(err)
	}
	var saved struct{ Input []map[string]any }
	if err := json.Unmarshal(state, &saved); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || len(saved.Input) != 1 {
		t.Fatalf("attempts=%d history=%s", attempts, state)
	}
}
