package test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"kh/internal/config"
	"kh/internal/provider/codex"
	"kh/internal/terminal"
	"strings"
)

type boundaryOutput struct {
	terminal.Renderer
	boundaries int
}

func (o *boundaryOutput) FlushReply() { o.boundaries++ }

func TestStreamFlushesCompletedAndFailedReplies(t *testing.T) {
	for _, ending := range []string{completeEvent, "data: {\"type\":\"response.failed\"}\n\n"} {
		t.Run(ending, func(t *testing.T) {
			mockCodex(t, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"**partial\"}\n\n"+ending)
			var text strings.Builder
			out := &boundaryOutput{Renderer: terminal.Renderer{Out: &text, Err: &text}}
			p := codex.New(config.Defaults, "", nil, out)
			_, _ = p.Step(context.Background(), "hello", nil)
			if out.boundaries != 1 || text.String() != "**partial\n" {
				t.Fatalf("boundaries=%d text=%q", out.boundaries, text.String())
			}
		})
	}
}

func TestCancelledStreamFlushesPartialReply(t *testing.T) {
	mockCodex(t, "")
	var text strings.Builder
	out := &boundaryOutput{Renderer: terminal.Renderer{Out: &text, Err: &text}}
	p := codex.New(config.Defaults, "", nil, out)
	http.DefaultClient.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		resp := retryResponse(200, "")
		partial := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
		resp.Body = io.NopCloser(io.MultiReader(strings.NewReader(partial), retryBlockedBody{r.Context()}))
		return resp, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := p.Step(ctx, "hello", nil)
	if !errors.Is(err, context.DeadlineExceeded) || out.boundaries != 1 || text.String() != "partial\n" {
		t.Fatalf("err=%v boundaries=%d text=%q", err, out.boundaries, text.String())
	}
	if p.LastResponse() != "" {
		t.Fatal("cancelled partial reply retained in history")
	}
}

func TestReplayFlushesAssistantMessageBoundaries(t *testing.T) {
	p := codex.New(config.Defaults, "", nil, nil)
	if err := p.Load([]byte(`{"input":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first"},{"type":"output_text","text":"part"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"second"}]}]}`)); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	out := &boundaryOutput{Renderer: terminal.Renderer{Out: &text, Err: &text}}
	p.Replay(out)
	if out.boundaries != 2 || text.String() != "first\npart\nsecond\n" {
		t.Fatalf("boundaries=%d text=%q", out.boundaries, text.String())
	}
}
