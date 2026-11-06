package test

import (
	"context"
	"net/http"
	"testing"

	"kh/internal/provider"
	"kh/internal/provider/codex"
)

var _ provider.LastStepResponder = (*codex.Client)(nil)

const currentFinal = `data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"current final"}]}}` + "\n\n"

func TestLastStepResponseNeverFallsBackToHistory(t *testing.T) {
	p := mockCodex(t, currentFinal+completeEvent)
	if _, err := p.Step(context.Background(), "first", nil); err != nil {
		t.Fatal(err)
	}
	if got := p.LastStepResponse(); got != "current final" {
		t.Fatalf("first=%q", got)
	}
	http.DefaultClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return retryResponse(200, completeEvent), nil })
	if _, err := p.Step(context.Background(), "", nil); err != nil {
		t.Fatal(err)
	}
	if got := p.LastStepResponse(); got != "" {
		t.Fatalf("empty continuation reused %q", got)
	}
	if p.LastResponse() != "current final" {
		t.Fatal("history unexpectedly altered")
	}
}

func TestLastStepResponseExcludesFailureCancellationAndTools(t *testing.T) {
	for _, mode := range []string{"failure", "cancel", "tools", "commentary"} {
		t.Run(mode, func(t *testing.T) {
			p := mockCodex(t, currentFinal+completeEvent)
			if _, err := p.Step(context.Background(), "first", nil); err != nil {
				t.Fatal(err)
			}
			stream := currentFinal + `data: {"type":"response.failed"}` + "\n\n"
			if mode == "tools" {
				stream = toolItem + completeEvent
			}
			if mode == "commentary" {
				stream = `data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"not final"}]}}` + "\n\n" + completeEvent
			}
			http.DefaultClient.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return retryResponse(200, stream), nil })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			_, err := p.Step(ctx, "next", nil)
			if (mode == "failure" || mode == "cancel") && err == nil {
				t.Fatal("expected failure")
			}
			if got := p.LastStepResponse(); got != "" {
				t.Fatalf("%s step retained %q", mode, got)
			}
		})
	}
}
