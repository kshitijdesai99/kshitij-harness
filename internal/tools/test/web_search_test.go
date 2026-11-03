package test

import (
	"context"
	"encoding/json"
	"errors"
	"kh/internal/provider"
	"kh/internal/tools"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWebActivationValidationAndEvidenceExclusion(t *testing.T) {
	var calls atomic.Int32
	activation := tools.EnableWebSearch(func() error { calls.Add(1); return nil })
	r, err := tools.NewRegistry([]tools.Tool{activation})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `[]`, `{"query":"secret"}`, `{} {}`} {
		if _, err := activation.Run(context.Background(), json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := activation.Run(ctx, json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("invalid/cancelled activation executed")
	}
	for _, call := range []provider.Call{{Name: tools.RegistryToolName, Input: json.RawMessage(`{"action":"describe","name":"enable_web_search"}`)}, {Name: tools.EnableWebSearchToolName, Input: json.RawMessage(`{}`)}, {Name: tools.InvokeToolName, Input: json.RawMessage(`{"name":"enable_web_search","arguments":{}}`)}} {
		if _, ok := r.LogicalCall(call); ok {
			t.Fatal("activation/discovery is learning execution evidence")
		}
	}
	out, err := r.Tools()[1].RunResult(context.Background(), json.RawMessage(`{"name":"enable_web_search","arguments":{}}`))
	if err != nil || calls.Load() != 1 || !strings.Contains(out.Output, "next model request") {
		t.Fatalf("activation: %+v %v calls=%d", out, err, calls.Load())
	}
}
