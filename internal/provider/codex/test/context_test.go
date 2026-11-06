package test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"kh/internal/config"
	"kh/internal/provider"
	"kh/internal/provider/codex"
)

var _ provider.ContextSetter = (*codex.Client)(nil)

func contextInput(t *testing.T, p *codex.Client) []map[string]any {
	t.Helper()
	b, err := p.Save()
	if err != nil {
		t.Fatal(err)
	}
	return toolImageInput(t, b)
}

func TestContextEmptyContinuationAndOrderedResults(t *testing.T) {
	p := mockCodex(t, toolImageCalls("first", "second"))
	if _, err := p.Step(context.Background(), "actual user", nil); err != nil {
		t.Fatal(err)
	}
	p.SetContext("Hook continuation (reference data): finish verification")
	results := []provider.Result{{ID: "first", Output: "one"}, {ID: "second", Output: "two"}}
	http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		input := toolImageInput(t, b)
		if len(input) != 6 || input[3]["call_id"] != "first" || input[4]["call_id"] != "second" || input[5]["role"] != "developer" {
			t.Fatalf("context displaced tool outputs: %v", input)
		}
		return retryResponse(200, completeEvent), nil
	})
	if _, err := p.Step(context.Background(), "", results); err != nil {
		t.Fatal(err)
	}
	saved, err := p.Save()
	if err != nil {
		t.Fatal(err)
	}
	resumed := codex.New(config.Defaults, "", nil, nil)
	if err := resumed.Load(saved); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(contextInput(t, p), contextInput(t, resumed)) {
		t.Fatal("resume changed context")
	}
	out := &retryOutput{}
	resumed.Replay(out)
	if strings.Contains(out.replies.String(), "Hook continuation") || len(out.actions) != 2 {
		t.Fatalf("reference context projected as reply/action: %+v", out)
	}
	http.DefaultClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return retryResponse(200, completeEvent), nil })
	if _, err := p.Step(context.Background(), "next user", nil); err != nil {
		t.Fatal(err)
	}
	input := contextInput(t, p)
	if len(input) != 7 || input[6]["role"] != "user" {
		t.Fatalf("context duplicated: %v", input)
	}
}

func TestContextDoesNotConsumeMemoryOnEmptyStep(t *testing.T) {
	p := mockCodex(t, completeEvent)
	p.SetMemory(provider.MemoryHeader + "\nremembered reference")
	p.SetContext("non-user continuation reference")
	if _, err := p.Step(context.Background(), "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Step(context.Background(), "actual user", nil); err != nil {
		t.Fatal(err)
	}
	input := contextInput(t, p)
	if len(input) != 3 || input[0]["role"] != "developer" || input[1]["role"] != "user" || input[2]["role"] != "user" {
		t.Fatalf("context consumed memory or invented user: %v", input)
	}
}

func TestContextFailureAndCancellationRollback(t *testing.T) {
	for _, mode := range []string{"failure", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			p := mockCodex(t, "")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			http.DefaultClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
				tail := `data: {"type":"response.failed"}` + "\n\n"
				body := io.ReadCloser(io.NopCloser(strings.NewReader(toolItem + tail)))
				if mode == "cancellation" {
					body = &contextCancelBody{Reader: strings.NewReader(toolItem), cancel: cancel}
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
			})
			p.SetContext("Stop continuation reference")
			calls, err := p.Step(ctx, "", nil)
			if err == nil || len(calls) != 0 {
				t.Fatalf("calls=%v err=%v", calls, err)
			}
			if mode == "cancellation" && !errors.Is(err, context.Canceled) {
				t.Fatalf("not canceled: %v", err)
			}
			input := contextInput(t, p)
			if len(input) != 1 || input[0]["role"] != "developer" {
				t.Fatalf("partial calls retained or context lost: %v", input)
			}
			b, err := p.Save()
			if err != nil {
				t.Fatal(err)
			}
			q := codex.New(config.Defaults, "", nil, nil)
			if err := q.Load(b); err != nil {
				t.Fatal(err)
			}
			http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				var request struct{ Input []map[string]any }
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(input, request.Input) {
					t.Fatalf("resume lost/duplicated context: %v", request.Input)
				}
				return retryResponse(200, completeEvent), nil
			})
			if _, err := q.Step(context.Background(), "", nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type contextCancelBody struct {
	*strings.Reader
	cancel context.CancelFunc
}

func (b *contextCancelBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		b.cancel()
		return 0, context.Canceled
	}
	return n, err
}
func (*contextCancelBody) Close() error { return nil }

func TestContextClearDoesNotLeakPendingState(t *testing.T) {
	p := mockCodex(t, completeEvent)
	p.SetContext("pending reference")
	if len(contextInput(t, p)) != 0 {
		t.Fatal("unconsumed context saved")
	}
	p.SetContext("")
	if _, err := p.Step(context.Background(), "unrelated user", nil); err != nil {
		t.Fatal(err)
	}
	input := contextInput(t, p)
	if len(input) != 1 || input[0]["role"] != "user" {
		t.Fatalf("cleared context leaked: %v", input)
	}
}
