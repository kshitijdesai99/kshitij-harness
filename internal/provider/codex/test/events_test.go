package test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"kh/internal/provider"
)

func TestConversationEventsPhasesToolsAndNoReplayOrCompaction(t *testing.T) {
	stream := `data: {"type":"response.output_item.added","item":{"type":"message","id":"m1","phase":"commentary"}}` + "\n\n" +
		`data: {"type":"response.output_text.delta","delta":"Checking."}` + "\n\n" +
		`data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","id":"m1","phase":"commentary","content":[{"type":"output_text","text":"Checking."}]}}` + "\n\n" +
		`data: {"type":"response.output_item.done","item":{"type":"reasoning","encrypted_content":"PRIVATE_REASONING"}}` + "\n\n" + toolItem + completeEvent
	p := mockCodex(t, stream)
	var events []provider.Event
	p.SetEventObserver(func(e provider.Event) error { events = append(events, e); return nil })
	calls, err := p.Step(context.Background(), "question", nil)
	if err != nil || len(calls) != 1 {
		t.Fatalf("%v %v", calls, err)
	}
	var types []string
	for _, e := range events {
		types = append(types, e.Type)
		if e.AttemptID == "" {
			t.Fatal("missing attempt id")
		}
	}
	if strings.Join(types, ",") != "attempt_start,assistant_delta,assistant_end,tool_call,attempt_end" {
		t.Fatal(types)
	}
	if events[1].MessageID != "m1" || events[1].Phase != "commentary" || events[1].Text != "Checking." || events[2].Status != "completed" || events[4].Status != "completed" {
		t.Fatalf("%+v", events)
	}
	before := len(events)
	p.Replay(nilOutput{})
	if len(events) != before {
		t.Fatal("replay archived twice")
	}
	// Reuse the public compaction fixture, which returns a short text-only reply.
	p2 := mockCodex(t, summaryStream("Short summary."))
	compactState(t, p2)
	p2.SetEventObserver(func(e provider.Event) error { t.Errorf("synthetic compaction event: %+v", e); return nil })
	if err := p2.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type nilOutput struct{}

func (nilOutput) Reply(string)  {}
func (nilOutput) Query(string)  {}
func (nilOutput) Action(string) {}
func (nilOutput) Notice(string) {}

func TestConversationEventsFailureSuppressesCallsAndRollsBack(t *testing.T) {
	stream := summaryStream("partial")
	stream = strings.TrimSuffix(stream, completeEvent) + toolItem + completeEvent
	p := mockCodex(t, stream)
	sentinel := errors.New("archive blocked")
	p.SetEventObserver(func(e provider.Event) error {
		if e.Type == "tool_call" {
			return sentinel
		}
		return nil
	})
	calls, err := p.Step(context.Background(), "question", nil)
	if !errors.Is(err, sentinel) || len(calls) != 0 {
		t.Fatalf("%v %v", calls, err)
	}
	state, _ := p.Save()
	if strings.Contains(string(state), "partial") || strings.Contains(string(state), "call-1") || !strings.Contains(string(state), "question") {
		t.Fatalf("failed step retained output: %s", state)
	}
}

func TestConversationEventsInterruptedAttemptAndPartialText(t *testing.T) {
	p := mockCodex(t, summaryStream("partial"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []provider.Event
	p.SetEventObserver(func(e provider.Event) error {
		events = append(events, e)
		if e.Type == "assistant_delta" {
			cancel()
		}
		return nil
	})
	_, err := p.Step(ctx, "question", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if events[len(events)-1].Type != "attempt_end" || events[len(events)-1].Status != "interrupted" {
		t.Fatalf("%+v", events)
	}
}
