package test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"kh/internal/agent"
	"kh/internal/provider"
	"kh/internal/tools"
)

func TestAfterToolsFailurePairsResultsWithoutAnotherReply(t *testing.T) {
	sentinel := errors.New("archive write failed")
	paired := false
	f := &fake{step: func(ctx context.Context, n int, results []provider.Result) ([]provider.Call, error) {
		if n == 1 {
			return []provider.Call{{ID: "call", Name: "noop", Input: json.RawMessage(`{}`)}}, nil
		}
		if ctx.Err() == nil {
			t.Error("generated follow-up after archive error")
		}
		if len(results) == 1 && results[0].ID == "call" && results[0].Output == "done" {
			paired = true
		}
		return nil, ctx.Err()
	}}
	runner := agent.Runner{Model: f, Tools: []tools.Tool{{Name: "noop", Run: func(context.Context, json.RawMessage) (string, error) { return "done", nil }}}, AfterTools: func([]provider.Call, []provider.Result) error { return sentinel }}
	if err := runner.Run(context.Background(), "task"); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if !paired {
		t.Fatal("completed results not paired")
	}
}
